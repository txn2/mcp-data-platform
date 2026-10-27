//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2176: a form is a bound script defining form_spec(req) and
// form_submit(req, values). Its GET answers the spec form_spec computed (here,
// choices from a Trino query keyed by the request's customer_id) and a signed
// form token; a submission is checked against the spec its token carries, in
// a urlencoded and a JSON body alike, and refused field by field before
// form_submit runs; a token another form issued, or one altered, is refused;
// form_submit's own field error is returned beside its field; and a token the
// spec handler minted, carried as a hidden field, verifies in the submit
// handler.
//
// Wire forms: the GET carries customer_id as a query string; a submission is
// sent once as application/x-www-form-urlencoded and once as an
// application/json object with the same fields; manage_script create and
// endpoint_set take their arguments as the literal tools/call params.
//
// The forms here are public, so nothing but the form contract stands between
// a request and the script. That the submission is stored is #2178's; here
// form_submit answers with what it accepted, so a refusal the platform made is
// told apart from one form_submit made by its message.

const issue2176PlansSQL = "SELECT code, name FROM (VALUES ('c-1', 'basic', 'Basic'), ('c-1', 'pro', 'Pro'), ('c-2', 'team', 'Team')) " +
	"AS t(customer, code, name) WHERE customer = :c ORDER BY code"

// issue2176Form is the form script, signing with the stored secret named
// secret.
func issue2176Form(secret string) string {
	return fmt.Sprintf(`PLANS_SQL = %q
SECRET = %q

def form_spec(req):
    """The plan request form for one customer."""
    customer = req["query"].get("customer_id", "")
    plans = platform.query(connection = "acme", sql = PLANS_SQL, params = {"c": customer})
    return {
        "fields": [
            {"name": "email", "type": "email", "required": True, "label": "Email"},
            {"name": "nickname", "type": "text", "max_length": 10},
            {"name": "plan", "type": "single_choice", "required": True,
             "choices": [{"value": r["code"], "label": r["name"]} for r in plans["rows"]]},
            {"name": "customer_id", "type": "hidden"},
            {"name": "application", "type": "hidden"},
        ],
        "prefill": {
            "customer_id": customer,
            "application": platform.mint_token({"customer_id": customer}, 3600, secret = SECRET),
        },
        "confirmation": "Thanks.",
    }

def form_submit(req, values):
    """Checks the application token, refuses an address already used, and accepts the rest."""
    checked = platform.verify_token(values["application"], secret = SECRET)
    if not checked["ok"]:
        return {"errors": {"application": "start the form again"}}
    if values["email"] == "taken@example.com":
        return {"errors": {"email": "this address already asked"}}
    return {"confirmation": "Thanks, " + checked["claims"]["customer_id"] + ", for asking about " + values["plan"]}

def main():
    """Drafts the spec and each answer form_submit gives."""
    spec = form_spec({"query": {"customer_id": "c-1"}, "caller": None})
    token = spec["prefill"]["application"]
    platform.result({
        "plans": [c["value"] for c in spec["fields"][2]["choices"]],
        "good": form_submit({"caller": None}, {"email": "ann@example.com", "plan": "pro", "customer_id": "c-1", "application": token}),
        "taken": form_submit({"caller": None}, {"email": "taken@example.com", "plan": "pro", "customer_id": "c-1", "application": token}),
        "bad": form_submit({"caller": None}, {"email": "ann@example.com", "application": "not-a-token"}),
    })
`, issue2176PlansSQL, secret)
}

// issue2176Bind saves the form under a fresh name, binds it public, and
// returns the name.
func issue2176Bind(t *testing.T, admin *client, prefix, secret string) string {
	t.Helper()
	name := endpointName(prefix)
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2176Form(secret)}, nil)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "public", "methods": []string{"GET", "POST"}, "connection": formsConn})
	return name
}

// issue2176Shown is a GET of the form: its spec and token.
type issue2176Shown struct {
	spec  map[string]any
	token string
	at    time.Time
}

func issue2176Show(t *testing.T, admin *client, name, customer string) issue2176Shown {
	t.Helper()
	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name+"?customer_id="+url.QueryEscape(customer), nil, "")
	if got.status != http.StatusOK {
		t.Fatalf("GET the form: %d %s", got.status, got.body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("the form's GET: %v %s", err, got.body)
	}
	spec, _ := out["spec"].(map[string]any)
	token, _ := out["form_token"].(string)
	if spec == nil || token == "" || out["token_field"] != "form_token" || out["honeypot_field"] != "website" {
		t.Fatalf("the form's GET carried no spec or token: %v", out)
	}
	return issue2176Shown{spec: spec, token: token, at: time.Now()}
}

// prefilled is a hidden field's value as the spec carries it.
func (s issue2176Shown) prefilled(field string) string {
	prefill, _ := s.spec["prefill"].(map[string]any)
	return fmt.Sprint(prefill[field])
}

// waitToFill waits out the spec's minimum fill time from the GET.
func (s issue2176Shown) waitToFill() {
	time.Sleep(time.Until(s.at.Add(2*time.Second + 200*time.Millisecond)))
}

// issue2176Submit posts fields to the form, as a urlencoded body when asJSON
// is false and as a JSON object when it is true.
func issue2176Submit(t *testing.T, admin *client, name string, fields map[string]string, asJSON bool) (int, map[string]any) {
	t.Helper()
	header, body := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, ""
	if asJSON {
		raw, _ := json.Marshal(fields)
		header, body = http.Header{"Content-Type": {"application/json"}}, string(raw)
	} else {
		form := url.Values{}
		for k, v := range fields {
			form.Set(k, v)
		}
		body = form.Encode()
	}
	got := endpointRequest(t, admin.base, http.MethodPost, "/endpoints/"+name, header, body)
	var out map[string]any
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("the submission's answer: %v %s", err, got.body)
	}
	return got.status, out
}

// valid is a submission the spec and form_submit accept.
func (s issue2176Shown) valid() map[string]string {
	return map[string]string{
		"email": "ann@example.com", "plan": "pro", "customer_id": s.prefilled("customer_id"),
		"application": s.prefilled("application"), "form_token": s.token,
	}
}

// TestIssue2176_TheSpecHandlerReadsItsChoicesFromAQuery: the plans offered
// are the ones the Trino query returned for the request's customer_id.
func TestIssue2176_TheSpecHandlerReadsItsChoicesFromAQuery(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{})
	name := issue2176Bind(t, admin, "acc-2176-spec", secret)
	for customer, want := range map[string]string{
		"c-1": `[map[label:Basic value:basic] map[label:Pro value:pro]]`,
		"c-2": `[map[label:Team value:team]]`,
	} {
		shown := issue2176Show(t, admin, name, customer)
		fields, _ := shown.spec["fields"].([]any)
		if len(fields) != 5 {
			t.Fatalf("the spec for %s: %v", customer, shown.spec)
		}
		plan, _ := fields[2].(map[string]any)
		if got := fmt.Sprint(plan["choices"]); got != want {
			t.Fatalf("the plans offered to %s: %s, want %s", customer, got, want)
		}
		if shown.prefilled("customer_id") != customer || shown.prefilled("application") == "" {
			t.Fatalf("the prefill for %s: %v", customer, shown.spec["prefill"])
		}
	}
}

// TestIssue2176_ASubmissionThatBreaksTheSpecIsRefusedFieldByField, in a
// urlencoded and a JSON body alike, before form_submit runs.
func TestIssue2176_ASubmissionThatBreaksTheSpecIsRefusedFieldByField(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{})
	name := issue2176Bind(t, admin, "acc-2176-fields", secret)
	shown := issue2176Show(t, admin, name, "c-1")

	if status, out := issue2176Submit(t, admin, name, shown.valid(), true); status != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(out["error"]), "sooner than a person fills it in") {
		t.Fatalf("a submission at once: want 400, got %d %v", status, out)
	}
	shown.waitToFill()

	bad := shown.valid()
	delete(bad, "plan")
	bad["email"], bad["nickname"], bad["color"] = "not-an-email", "much-too-long-a-name", "red"
	want := map[string]string{
		"plan": "this field is required", "email": "this is not an email address",
		"nickname": "this is longer than 10 characters", "color": "this form has no such field",
	}
	for _, asJSON := range []bool{false, true} {
		status, out := issue2176Submit(t, admin, name, bad, asJSON)
		errs, _ := out["errors"].(map[string]any)
		if status != http.StatusUnprocessableEntity || len(errs) != len(want) {
			t.Fatalf("a submission breaking the spec (JSON %v): want 422 with %d errors, got %d %v", asJSON, len(want), status, out)
		}
		for field, msg := range want {
			if errs[field] != msg {
				t.Errorf("JSON %v: %s's error is %v, want %q", asJSON, field, errs[field], msg)
			}
		}
	}
	honeypot := shown.valid()
	honeypot["website"] = "https://spam.example.com"
	if status, out := issue2176Submit(t, admin, name, honeypot, false); status != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(out["error"]), "people do not see") {
		t.Fatalf("a filled honeypot: want 400, got %d %v", status, out)
	}
}

// TestIssue2176_ATokenAnotherFormIssuedOrOneAlteredIsRefused.
func TestIssue2176_ATokenAnotherFormIssuedOrOneAlteredIsRefused(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{})
	first, second := issue2176Bind(t, admin, "acc-2176-first", secret), issue2176Bind(t, admin, "acc-2176-second", secret)
	shown := issue2176Show(t, admin, first, "c-1")
	shown.waitToFill()

	if status, out := issue2176Submit(t, admin, second, shown.valid(), true); status != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(out["error"]), "issued by another form") {
		t.Fatalf("%s's token on %s: want 400, got %d %v", first, second, status, out)
	}
	altered := shown.valid()
	body, sig, _ := strings.Cut(shown.token, ".")
	altered["form_token"] = body[:len(body)-2] + "AA." + sig
	if status, out := issue2176Submit(t, admin, first, altered, false); status != http.StatusBadRequest ||
		!strings.Contains(fmt.Sprint(out["error"]), "was changed") {
		t.Fatalf("an altered token: want 400, got %d %v", status, out)
	}
	changed := shown.valid()
	changed["customer_id"] = "c-2"
	if status, out := issue2176Submit(t, admin, first, changed, true); status != http.StatusUnprocessableEntity ||
		fmt.Sprint(out["errors"]) != "map[customer_id:this field was changed]" {
		t.Fatalf("a changed hidden field: want 422, got %d %v", status, out)
	}
}

// TestIssue2176_TheSubmitHandlersFieldErrorIsReturnedBesideItsField.
func TestIssue2176_TheSubmitHandlersFieldErrorIsReturnedBesideItsField(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{})
	name := issue2176Bind(t, admin, "acc-2176-taken", secret)
	shown := issue2176Show(t, admin, name, "c-1")
	shown.waitToFill()
	taken := shown.valid()
	taken["email"] = "taken@example.com"
	for _, asJSON := range []bool{false, true} {
		if status, out := issue2176Submit(t, admin, name, taken, asJSON); status != http.StatusUnprocessableEntity ||
			fmt.Sprint(out["errors"]) != "map[email:this address already asked]" {
			t.Fatalf("form_submit's own error (JSON %v): want 422 beside email, got %d %v", asJSON, status, out)
		}
	}
}

// TestIssue2176_ATokenTheSpecHandlerMintedVerifiesInTheSubmitHandler: the
// accepted submission's confirmation is built from the claims form_submit
// read off the token form_spec minted.
func TestIssue2176_ATokenTheSpecHandlerMintedVerifiesInTheSubmitHandler(t *testing.T) {
	admin := connect(t)
	secret, _ := issue2175Secret(t, admin, []string{})
	name := issue2176Bind(t, admin, "acc-2176-token", secret)
	shown := issue2176Show(t, admin, name, "c-1")
	shown.waitToFill()
	for _, asJSON := range []bool{false, true} {
		status, out := issue2176Submit(t, admin, name, shown.valid(), asJSON)
		if status != http.StatusOK || out["ok"] != true || out["confirmation"] != "Thanks, c-1, for asking about pro" {
			t.Fatalf("an accepted submission (JSON %v): %d %v", asJSON, status, out)
		}
	}
}

// TestIssue2176_TheHelpDescribesTheFormContract.
func TestIssue2176_TheHelpDescribesTheFormContract(t *testing.T) {
	c := connect(t)
	help := c.call("manage_script", map[string]any{"command": "help"})
	text, _ := help["dialect"].(string)
	for _, want := range []string{"FORMS", "def form_spec(req):", "def form_submit(req, values):", "form_token", "422"} {
		if !strings.Contains(text, want) {
			t.Errorf("manage_script help does not say %q", want)
		}
	}
}
