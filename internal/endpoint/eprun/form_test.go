package eprun

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
)

// contactForm is a form script (#2176): its spec offers the plans the query
// names, and form_submit refuses one address and echoes what it accepted.
const contactForm = `
def form_spec(req):
    """The contact form."""
    plans = req["query"].get("plans", "basic,pro").split(",")
    return {
        "fields": [
            {"name": "email", "type": "email", "required": True, "label": "Email"},
            {"name": "plan", "type": "single_choice", "choices": [{"value": p, "label": p.title()} for p in plans]},
            {"name": "customer", "type": "hidden"},
        ],
        "prefill": {"customer": req["query"].get("customer", "")},
        "confirmation": "Thanks, we will write back.",
    }

def form_submit(req, values):
    """Refuses one address; accepts the rest."""
    if values["email"] == "taken@example.com":
        return {"errors": {"email": "this address already has an account"}}
    if values["email"] == "bad@example.com":
        return "not a response"
    if values["email"] == "odd@example.com":
        return {"color": "red"}
    if values["email"] == "own@example.com":
        return {"confirmation": "Welcome " + values["customer"], "redirect": "/welcome"}
    return None
`

var formKey = []byte("0123456789abcdef0123456789abcdef")

func formFixture(t *testing.T) (*fixture, *time.Time) {
	t.Helper()
	f := newFixture(t, contactForm)
	now := time.Unix(1_800_000_000, 0)
	f.runner.FormKey, f.runner.Now = formKey, func() time.Time { return now }
	return f, &now
}

// showForm GETs the form and returns its token.
func showForm(t *testing.T, f *fixture, query map[string][]string) (out map[string]any, token string) {
	t.Helper()
	resp := f.serve(t, Request{Method: "GET", Query: query})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	out = decode(t, resp.Body)
	token, _ = out["form_token"].(string)
	require.NotEmpty(t, token)
	return out, token
}

func submitForm(t *testing.T, f *fixture, contentType, body string) (resp Response, out map[string]any) {
	t.Helper()
	resp = f.serve(t, Request{Method: "POST", Header: http.Header{"Content-Type": {contentType}}, Body: []byte(body)})
	return resp, decode(t, resp.Body)
}

// #2176: GET answers the spec form_spec computed from the request, with the
// token and the platform's field names.
func TestForm_AGETAnswersTheSpecItsHandlerComputed(t *testing.T) {
	f, _ := formFixture(t)
	out, _ := showForm(t, f, map[string][]string{"plans": {"gold,silver"}, "customer": {"c-7"}})
	spec, _ := out["spec"].(map[string]any)
	fields, _ := spec["fields"].([]any)
	require.Len(t, fields, 3)
	plan, _ := fields[1].(map[string]any)
	assert.Equal(t, []any{map[string]any{"value": "gold", "label": "Gold"}, map[string]any{"value": "silver", "label": "Silver"}}, plan["choices"])
	assert.Equal(t, map[string]any{"customer": "c-7"}, spec["prefill"])
	assert.Equal(t, epform.TokenField, out["token_field"])
	assert.Equal(t, epform.HoneypotField, out["honeypot_field"])
	assert.InDelta(t, epform.DefaultMinFillSeconds, spec["min_fill_seconds"], 0)
}

// #2176: a submission passing the spec reaches form_submit, as a form body and
// as JSON alike, and form_submit's answers are the response.
func TestForm_ASubmissionThatPassesReachesFormSubmit(t *testing.T) {
	f, now := formFixture(t)
	_, token := showForm(t, f, map[string][]string{"customer": {"c-7"}})
	*now = now.Add(5 * time.Second)

	resp, out := submitForm(t, f, "application/x-www-form-urlencoded", "email=ann%40example.com&plan=pro&customer=c-7&form_token="+token)
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	assert.Equal(t, map[string]any{"ok": true, "confirmation": "Thanks, we will write back.", "redirect": ""}, out)

	resp, out = submitForm(t, f, "application/json", `{"email":"own@example.com","plan":"basic","customer":"c-7","form_token":"`+token+`"}`)
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	assert.Equal(t, "Welcome c-7", out["confirmation"])
	assert.Equal(t, "/welcome", out["redirect"])

	resp, out = submitForm(t, f, "application/json", `{"email":"taken@example.com","customer":"c-7","form_token":"`+token+`"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.Equal(t, map[string]any{"email": "this address already has an account"}, out["errors"])

	for _, email := range []string{"bad@example.com", "odd@example.com"} {
		resp, _ = submitForm(t, f, "application/json", `{"email":"`+email+`","customer":"c-7","form_token":"`+token+`"}`)
		assert.Equal(t, http.StatusInternalServerError, resp.Status, email)
	}
	assert.Len(t, f.bindings.failures, 2, "a return that is not a form answer is kept for the owner")
}

// #2176: a submission the spec refuses never reaches form_submit, whether its
// fields are wrong, its token is missing, altered or another form's, or it
// came back too soon or with the honeypot filled.
func TestForm_ASubmissionTheSpecRefusesNeverRunsTheScript(t *testing.T) {
	f, now := formFixture(t)
	_, token := showForm(t, f, map[string][]string{"customer": {"c-7"}})
	sessions := len(f.audit.events)

	resp, _ := submitForm(t, f, "application/json", `{"email":"ann@example.com","customer":"c-7","form_token":"`+token+`"}`)
	assert.Equal(t, http.StatusBadRequest, resp.Status, "within the minimum fill time")
	*now = now.Add(5 * time.Second)

	resp, out := submitForm(t, f, "application/x-www-form-urlencoded", "email=not-an-email&plan=gold&customer=c-8&color=red&form_token="+token)
	require.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.Equal(t, map[string]any{
		"email": "this is not an email address", "plan": "this is not one of the choices",
		"customer": "this field was changed", "color": "this form has no such field",
	}, out["errors"])

	other, err := epform.Sign(formKey, "signup", epform.Spec{Fields: []epform.Field{{Name: "email", Type: "email", MaxLength: 10}}}, now.Add(-time.Minute))
	require.NoError(t, err)
	for name, body := range map[string]string{
		"no token":       `{"email":"ann@example.com"}`,
		"altered token":  `{"email":"ann@example.com","form_token":"` + strings.Replace(token, token[:4], "AAAA", 1) + `"}`,
		"another form's": `{"email":"ann@example.com","form_token":"` + other + `"}`,
		"honeypot":       `{"email":"ann@example.com","customer":"c-7","website":"http://spam.example.com","form_token":"` + token + `"}`,
	} {
		resp, out := submitForm(t, f, "application/json", body)
		assert.Equal(t, http.StatusBadRequest, resp.Status, name)
		assert.Contains(t, out["error"], "the submission was refused", name)
	}
	resp, _ = submitForm(t, f, "text/plain", "email=a")
	assert.Equal(t, http.StatusBadRequest, resp.Status)

	assert.Empty(t, f.bindings.failures, "a refused submission is the caller's, not a failure")
	assert.Len(t, f.audit.events, sessions+7, "each request is audited")
}

func TestForm_AnInvalidSpecIs500AndKept(t *testing.T) {
	f := newFixture(t, `
def form_spec(req):
    """Declares a field twice."""
    return {"fields": [{"name": "a", "type": "text"}, {"name": "a", "type": "text"}]}

def form_submit(req, values):
    """Accepts."""
    return None
`)
	f.runner.FormKey = formKey
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusInternalServerError, resp.Status)
	require.Len(t, f.bindings.failures, 1)
	assert.Contains(t, f.bindings.failures[0].Error, "declared twice")
}

func TestForm_WithoutAKeyIs503(t *testing.T) {
	f := newFixture(t, contactForm)
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status)
	require.Len(t, f.bindings.failures, 1)
	assert.Contains(t, f.bindings.failures[0].Error, "signing_key")
}
