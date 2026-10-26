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

// Acceptance for #2178: a form stores each submission its form_submit accepts
// in a webhook source made for it when it is bound. A public submission is a
// row of webhook_form_<endpoint>, read with trino_query; a submission whose
// bot check answer is missing, wrong, reused or expired is refused and stores
// nothing; on a signed-in form the stored event carries the caller the
// platform found, whatever a field claims; and the form input cannot be set on
// a source that is not a form's.
//
// Wire forms: manage_script create and endpoint_set take their arguments as
// literal tools/call params (endpoint_set's connection as a string); each
// form GET is a plain GET; a submission is sent once as
// application/x-www-form-urlencoded and once as an application/json object
// with the same fields; X-Bot-Check carries the answer as a header;
// trino_query takes string connection, sql and purpose; the admin webhook
// routes take one JSON object.
//
// The criteria run against the local stack: the platform, Trino with the
// acme-scratch-resources scratch catalog, the SeaweedFS store the receiver
// writes to, and Postgres.

const (
	formsConn    = "acme-scratch-resources"
	formsSchema  = "scratch_resources.uploads"
	formsPurpose = "Acceptance for #2177-#2180: forms."
)

// formSource is a form script: form_spec asks for fields, form_submit accepts
// every submission, and main() drafts both.
func formSource(fields string) string {
	return fmt.Sprintf(`FIELDS = %s

def form_spec(req):
    """The form, its fields, and the query it was opened with as a hidden field."""
    return {
        "fields": FIELDS + [{"name": "t", "type": "hidden"}],
        "prefill": {"t": req["query"].get("t", "")},
        "confirmation": "Thanks, we have it.",
    }

def form_submit(req, values):
    """Accepts every submission."""
    return None

def main():
    """Drafts the spec and an accepted submission."""
    spec = form_spec({"query": {"t": "x"}, "caller": None})
    platform.result({"fields": len(spec["fields"]), "accepted": form_submit({"caller": None}, {})})
`, fields)
}

// formFields are the fields of the forms here.
const formFields = `[
    {"name": "email", "type": "email", "required": True, "label": "Email"},
    {"name": "message", "type": "long_text", "label": "Message"},
]`

// bindForm saves a form under a fresh name and binds it with access args,
// storing on formsConn, and returns its name and the table it stores into.
func bindForm(t *testing.T, admin *client, prefix, fields string, args map[string]any) (name, table string) {
	t.Helper()
	name = endpointName(prefix)
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": formSource(fields)}, nil)
	params := map[string]any{"endpoint": name, "methods": []string{"GET", "POST"}, "connection": formsConn}
	for k, v := range args {
		params[k] = v
	}
	out := bindEndpoint(t, admin, name, "", params)
	form, _ := out["form"].(map[string]any)
	table, _ = form["table"].(string)
	if table != "webhook_form_"+strings.ReplaceAll(name, "-", "_") || form["connection"] != formsConn {
		t.Fatalf("binding form %s made no source, or another: %v", name, out)
	}
	t.Cleanup(func() {
		if st, del := admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+fmt.Sprint(form["source"]), http.NoBody); st != http.StatusNoContent {
			t.Logf("deleting the form's source %v: status %d: %v", form["source"], st, del)
		}
	})
	return name, table
}

// formShown is one GET of a form: its spec and token, and when.
type formShown struct {
	spec  map[string]any
	token string
	at    time.Time
}

func showForm(t *testing.T, base, name, query string, header http.Header) formShown {
	t.Helper()
	path := "/endpoints/" + name
	if query != "" {
		path += "?" + query
	}
	got := endpointRequest(t, base, http.MethodGet, path, header, "")
	if got.status != http.StatusOK {
		t.Fatalf("GET form %s: %d %s", name, got.status, got.body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("form %s's GET: %v %s", name, err, got.body)
	}
	spec, _ := out["spec"].(map[string]any)
	token, _ := out["form_token"].(string)
	if spec == nil || token == "" {
		t.Fatalf("form %s's GET carried no spec or token: %v", name, out)
	}
	return formShown{spec: spec, token: token, at: time.Now()}
}

// waitToFill waits out the form's minimum fill time from its GET.
func (s formShown) waitToFill() {
	time.Sleep(time.Until(s.at.Add(2*time.Second + 200*time.Millisecond)))
}

// fields are values with the form's token and its hidden field as shown.
func (s formShown) fields(values map[string]string) map[string]string {
	prefill, _ := s.spec["prefill"].(map[string]any)
	out := map[string]string{"form_token": s.token, "t": fmt.Sprint(prefill["t"])}
	for k, v := range values {
		out[k] = v
	}
	return out
}

// submitForm posts fields to the form, as a urlencoded body or a JSON
// object, with header added.
func submitForm(t *testing.T, base, name string, fields map[string]string, asJSON bool, header http.Header) (int, map[string]any) {
	t.Helper()
	h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	body := ""
	if asJSON {
		raw, _ := json.Marshal(fields)
		h.Set("Content-Type", "application/json")
		body = string(raw)
	} else {
		form := url.Values{}
		for k, v := range fields {
			form.Set(k, v)
		}
		body = form.Encode()
	}
	for k, v := range header {
		h[k] = v
	}
	got := endpointRequest(t, base, http.MethodPost, "/endpoints/"+name, h, body)
	var out map[string]any
	_ = json.Unmarshal([]byte(got.body), &out)
	return got.status, out
}

// formRow is one row of a form's table: the event's id and key, and its
// payload decoded.
type formRow struct {
	id, key string
	payload map[string]any
}

// formRows reads a form's table through trino_query until it holds want rows,
// or a minute passes; the window a submission lands in is registered moments
// after it is stored.
func formRows(t *testing.T, c *client, table string, want int) []formRow {
	t.Helper()
	sql := fmt.Sprintf("SELECT event_id, key, payload FROM %s.%s ORDER BY received_at", formsSchema, table)
	deadline := time.Now().Add(time.Minute)
	var rows []formRow
	for {
		rows = rows[:0]
		res, text, err := c.callRaw("trino_query", map[string]any{"connection": formsConn, "sql": sql, "purpose": formsPurpose})
		if err == nil && !res.IsError {
			got := c.call("trino_query", map[string]any{"connection": formsConn, "sql": sql, "purpose": formsPurpose})
			list, _ := got["rows"].([]any)
			for _, item := range list {
				m, _ := item.(map[string]any)
				var p map[string]any
				_ = json.Unmarshal([]byte(fmt.Sprint(m["payload"])), &p)
				rows = append(rows, formRow{id: fmt.Sprint(m["event_id"]), key: fmt.Sprint(m["key"]), payload: p})
			}
		}
		if len(rows) >= want {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s holds %d rows, want %d (last answer: %s)", table, len(rows), want, text)
		}
		time.Sleep(time.Second)
	}
}

// values is a row's stored values.
func (r formRow) values() map[string]any {
	v, _ := r.payload["values"].(map[string]any)
	return v
}

// TestIssue2178_APublicSubmissionIsARowOfItsTable: each accepted public
// submission, urlencoded and JSON, is a row of webhook_form_<endpoint> with
// its field values.
func TestIssue2178_APublicSubmissionIsARowOfItsTable(t *testing.T) {
	admin := connect(t)
	name, table := bindForm(t, admin, "acc-2178-public", formFields, map[string]any{"auth_mode": "public"})
	shown := showForm(t, admin.base, name, "t=link-7", nil)
	shown.waitToFill()
	for i, asJSON := range []bool{false, true} {
		status, out := submitForm(t, admin.base, name, shown.fields(map[string]string{
			"email": fmt.Sprintf("ann%d@example.com", i), "message": "Hello from the form",
		}), asJSON, nil)
		if status != http.StatusOK || out["confirmation"] != "Thanks, we have it." {
			t.Fatalf("submission (JSON %v): %d %v", asJSON, status, out)
		}
	}
	rows := formRows(t, admin, table, 2)
	for i, r := range rows {
		v := r.values()
		if v["email"] != fmt.Sprintf("ann%d@example.com", i) || v["message"] != "Hello from the form" || v["t"] != "link-7" {
			t.Errorf("row %d holds %v", i, v)
		}
		if r.payload["endpoint"] != name || r.payload["submitter"] != nil || r.key != "" {
			t.Errorf("row %d of a public form: endpoint %v, submitter %v, key %q", i, r.payload["endpoint"], r.payload["submitter"], r.key)
		}
	}
}

// TestIssue2178_ARefusedBotCheckStoresNothing: a submission with no answer, a
// wrong one, one used before or one past its challenge's lifetime is refused,
// and only the one with a fresh answer is stored.
func TestIssue2178_ARefusedBotCheckStoresNothing(t *testing.T) {
	admin := connect(t)
	check := putBotCheck(t, admin, map[string]any{"provider": "pow", "challenge_ttl_seconds": 5})
	name, table := bindForm(t, admin, "acc-2178-check", formFields, map[string]any{
		"auth_mode": "bot_check", "bot_check": check, "checked_methods": []string{"POST"},
	})
	shown := showForm(t, admin.base, name, "", nil)
	shown.waitToFill()
	body := shown.fields(map[string]string{"email": "bot@example.com"})
	answered := func(answer string) http.Header { return http.Header{"X-Bot-Check": {answer}} }

	if status, out := submitForm(t, admin.base, name, body, true, nil); status != http.StatusForbidden {
		t.Fatalf("no answer: want 403, got %d %v", status, out)
	}
	if status, out := submitForm(t, admin.base, name, body, false, answered("not-an-answer")); status != http.StatusForbidden {
		t.Fatalf("a wrong answer: want 403, got %d %v", status, out)
	}
	ch := challengeFor(t, admin.base, name)
	answer := solvePoW(ch.Token, ch.Difficulty)
	if status, out := submitForm(t, admin.base, name, body, true, answered(answer)); status != http.StatusOK {
		t.Fatalf("a fresh answer: want 200, got %d %v", status, out)
	}
	if status, out := submitForm(t, admin.base, name, body, false, answered(answer)); status != http.StatusForbidden {
		t.Fatalf("the same answer again: want 403, got %d %v", status, out)
	}
	stale := challengeFor(t, admin.base, name)
	staleAnswer := solvePoW(stale.Token, stale.Difficulty)
	time.Sleep(6 * time.Second) // the check's challenges live 5 seconds, the least a check takes
	if status, out := submitForm(t, admin.base, name, body, true, answered(staleAnswer)); status != http.StatusForbidden ||
		!strings.Contains(fmt.Sprint(out), "expired") {
		t.Fatalf("an expired answer: want 403 saying expired, got %d %v", status, out)
	}

	rows := formRows(t, admin, table, 1)
	time.Sleep(3 * time.Second)
	if again := formRows(t, admin, table, 1); len(again) != 1 || len(rows) != 1 {
		t.Fatalf("%s holds %d rows; only the submission with a fresh answer may be stored", table, len(again))
	}
}

// TestIssue2178_ASignedInFormRecordsTheCallerNotAField: the stored event's
// submitter is the API key's person, and a field naming somebody else is a
// value like any other.
func TestIssue2178_ASignedInFormRecordsTheCallerNotAField(t *testing.T) {
	admin := connect(t)
	fields := `[
    {"name": "email", "type": "email", "required": True},
    {"name": "submitter", "type": "text"},
]`
	name, table := bindForm(t, admin, "acc-2178-signed", fields, map[string]any{"auth_mode": "authenticated", "callers": []string{"*"}})
	owner := bearer(devOwnerAPIKey)
	shown := showForm(t, admin.base, name, "", owner)
	shown.waitToFill()
	status, out := submitForm(t, admin.base, name, shown.fields(map[string]string{
		"email": "someone.else@example.com", "submitter": "someone.else@example.com",
	}), true, owner)
	if status != http.StatusOK {
		t.Fatalf("the signed-in submission: %d %v", status, out)
	}
	if status, _ := submitForm(t, admin.base, name, shown.fields(map[string]string{"email": "x@example.com"}), false, nil); status != http.StatusUnauthorized {
		t.Fatalf("a submission with no credential: want 401, got %d", status)
	}
	rows := formRows(t, admin, table, 1)
	sub, _ := rows[0].payload["submitter"].(map[string]any)
	if sub["email"] != devOwnerEmail || rows[0].key != devOwnerEmail {
		t.Fatalf("the stored submitter is %v (key %q), want %s", sub, rows[0].key, devOwnerEmail)
	}
	if v := rows[0].values(); v["email"] != "someone.else@example.com" || v["submitter"] != "someone.else@example.com" {
		t.Fatalf("the fields are values like any other: %v", v)
	}
}

// TestIssue2178_TheFormInputIsOnlyAFormsSource: an administrator cannot make a
// form's source or turn another source into one, and a form's source is
// never posted to.
func TestIssue2178_TheFormInputIsOnlyAFormsSource(t *testing.T) {
	admin := connect(t)
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, map[string]any{
		"name": issue1870Name("acc-2178-form"), "connection": formsConn, "auth": map[string]any{},
		"config": map[string]any{"input": "form", "form": map[string]any{"script_id": "s", "endpoint": "e"}},
	}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), "made by the platform") {
		t.Fatalf("an administrator's form source: want 400, got %d %v", status, out)
	}

	src := issue1870Name("acc-2178-http")
	issue1870Create(t, admin, issue1870HMAC(src, "s3cret", map[string]any{}))
	status, out = admin.rest(http.MethodPut, "/api/v1/admin/webhooks/sources/"+src, jsonBody(t, map[string]any{
		"auth":   map[string]any{"mode": "hmac", "signature_header": "X-Signature", "prefix": "sha256="},
		"config": map[string]any{"input": "form", "form": map[string]any{"script_id": "s", "endpoint": "e"}},
	}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), "only for a form's source") {
		t.Fatalf("turning a posted source into a form's: want 400, got %d %v", status, out)
	}

	name, _ := bindForm(t, admin, "acc-2178-posted", formFields, map[string]any{"auth_mode": "public"})
	res, _ := issue1870Post(t, baseURL(), "form-"+name, "application/json", []byte(`{"values":{}}`), nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /hooks/form-%s: want 404, got %d", name, res.StatusCode)
	}
}
