package eprun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// formSource answers FormSource for any script with one form source.
type formSource struct{ found bool }

func (f formSource) FormSource(context.Context, string) (whsource.Source, bool) {
	return whsource.Source{Name: "form-contact", Config: whsource.Config{Input: whsource.InputForm}}, f.found
}

type deliveries struct {
	events []whevent.Event
	err    error
}

func (d *deliveries) Deliver(_ context.Context, _ whsource.Source, ev whevent.Event, _ []byte) error {
	if d.err != nil {
		return d.err
	}
	d.events = append(d.events, ev)
	return nil
}

// retryLaterError is a store refusal that says when to send it again.
type retryLaterError struct{}

func (retryLaterError) Error() string   { return "the receiver's spool is full" }
func (retryLaterError) RetryAfter() int { return 7 }

func storingFixture(t *testing.T) (*fixture, *time.Time, *deliveries) {
	t.Helper()
	f, now := formFixture(t)
	d := &deliveries{}
	f.runner.SetCollector(&epcollect.Collector{Sources: formSource{found: true}, Writer: d})
	return f, now, d
}

// #2178: an accepted submission is stored as one event before it is
// answered, with the checked values, and a signed-in one records who sent it
// from the session and never from a field.
func TestStore_AnAcceptedSubmissionIsStored(t *testing.T) {
	f, now, d := storingFixture(t)
	_, token := showForm(t, f, map[string][]string{"customer": {"c-7"}})
	*now = now.Add(5 * time.Second)

	resp := f.serve(t, Request{
		Method: "POST", Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"email":"ann@example.com","plan":"pro","customer":"c-7","form_token":"` + token + `"}`),
		Caller: &Caller{UserID: "u-ann", Email: "ann@corp.example.com", AuthType: "oidc"},
	})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	require.Len(t, d.events, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(d.events[0].Payload, &payload))
	assert.Equal(t, map[string]any{"email": "ann@example.com", "plan": "pro", "customer": "c-7"}, payload["values"])
	submitter, ok := payload["submitter"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ann@corp.example.com", submitter["email"],
		"the submitter is the session's identity, not the email field")
	assert.Equal(t, "orders", payload["endpoint"], "the fixture binds the script at orders")
}

// #2178: a submission refused field by field or by form_submit stores nothing.
func TestStore_ARefusedSubmissionStoresNothing(t *testing.T) {
	f, now, d := storingFixture(t)
	_, token := showForm(t, f, map[string][]string{"customer": {"c-7"}})
	*now = now.Add(5 * time.Second)
	resp, _ := submitForm(t, f, "application/json", `{"email":"taken@example.com","customer":"c-7","form_token":"`+token+`"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	resp, _ = submitForm(t, f, "application/json", `{"email":"x","customer":"c-7","form_token":"`+token+`"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	resp, _ = submitForm(t, f, "application/json", `{"email":"ann@example.com","customer":"c-7"}`)
	assert.Equal(t, http.StatusBadRequest, resp.Status)
	assert.Empty(t, d.events)
}

// #2178: a submission that could not be stored is not confirmed.
func TestStore_ASubmissionNotStoredIs503(t *testing.T) {
	f, now, d := storingFixture(t)
	_, token := showForm(t, f, map[string][]string{"customer": {"c-7"}})
	*now = now.Add(5 * time.Second)
	body := `{"email":"ann@example.com","customer":"c-7","form_token":"` + token + `"}`

	d.err = retryLaterError{}
	resp, out := submitForm(t, f, "application/json", body)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status)
	assert.Equal(t, "7", resp.Header.Get("Retry-After"))
	assert.Contains(t, out["error"], "could not be stored")

	d.err = errors.New("disk gone")
	resp, _ = submitForm(t, f, "application/json", body)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status)
	assert.Empty(t, resp.Header.Get("Retry-After"))

	f.runner.SetCollector(&epcollect.Collector{Sources: formSource{}, Writer: d})
	resp, _ = submitForm(t, f, "application/json", body)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status, "a form with no source is not confirmed")
	require.Len(t, f.bindings.failures, 3, "each is kept for the owner")
	assert.Contains(t, f.bindings.failures[2].Error, "bind it again")
}

const sensitiveForm = `
def form_spec(req):
    """An application with an SSN."""
    return {"fields": [{"name": "ssn", "type": "text", "sensitive": True}]}

def form_submit(req, values):
    """Accepts."""
    return None
`

// #2179: a form declaring a sensitive field is shown only where the platform
// can encrypt it.
func TestStore_ASensitiveFieldNeedsAnEncryptionKey(t *testing.T) {
	f := newFixture(t, sensitiveForm)
	f.runner.FormKey = formKey
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusInternalServerError, resp.Status, "no collector: nowhere to encrypt")
	require.Len(t, f.bindings.failures, 1)
	assert.Contains(t, f.bindings.failures[0].Error, "ENCRYPTION_KEY")

	enc, err := fieldcrypt.NewFieldEncryptor(formKey)
	require.NoError(t, err)
	f.runner.SetCollector(&epcollect.Collector{Sources: formSource{found: true}, Writer: &deliveries{}, Sealer: fieldcrypt.NewRestFieldEncryptor(enc)})
	resp = f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
}

const failingSensitiveForm = `
def form_spec(req):
    """An application with an SSN."""
    return {"fields": [{"name": "ssn", "type": "text", "sensitive": True}, {"name": "note", "type": "text"}]}

def form_submit(req, values):
    """Reads the SSN as a number, which fails on a dashed one and names it."""
    return {"confirmation": str(int(values["ssn"]))}
`

// #2360: a form_submit that fails on a sensitive value records the failure
// with the value replaced, in the owner's failure row and the audit row.
func TestStore_AFailureNeverRecordsASensitiveValue(t *testing.T) {
	f := newFixture(t, failingSensitiveForm)
	now := time.Unix(1_800_000_000, 0)
	f.runner.FormKey, f.runner.Now = formKey, func() time.Time { return now }
	enc, err := fieldcrypt.NewFieldEncryptor(formKey)
	require.NoError(t, err)
	f.runner.SetCollector(&epcollect.Collector{Sources: formSource{found: true}, Writer: &deliveries{}, Sealer: fieldcrypt.NewRestFieldEncryptor(enc)})
	_, token := showForm(t, f, nil)
	now = now.Add(5 * time.Second)

	resp, _ := submitForm(t, f, "application/json", `{"ssn":"123-45-6789","note":"hello","form_token":"`+token+`"}`)
	assert.Equal(t, http.StatusInternalServerError, resp.Status)
	require.Len(t, f.bindings.failures, 1)
	failure := f.bindings.failures[0].Error
	assert.NotContains(t, failure, "123-45-6789")
	assert.Contains(t, failure, "[REDACTED:ssn]", "the failure still says which field it was")
	assert.Contains(t, failure, "invalid literal", "and what went wrong")
	last := f.audit.events[len(f.audit.events)-1]
	assert.NotContains(t, last.ErrorMessage, "123-45-6789")
}

// #2360: every shape a submitted value takes is recorded for redaction.
func TestSensitiveValuesRecordsEveryShape(t *testing.T) {
	red := sensitiveValues([]string{"ssn", "picks", "tags", "income", "empty"}, map[string]any{
		"ssn": "123-45-6789", "picks": []any{"red", "blue"}, "tags": []string{"vip"}, "income": 52000, "empty": nil,
	})
	got := red.String("123-45-6789 red blue vip 52000")
	for _, v := range []string{"123-45-6789", "red", "blue", "vip", "52000"} {
		assert.NotContains(t, got, v)
	}
	assert.Nil(t, sensitiveValues(nil, map[string]any{"a": "b"}))
	plain := errors.New("x")
	assert.Same(t, plain, redactFailure(nil, plain), "nothing to replace keeps the error")
	inner := errors.New("bad 123-45-6789")
	wrapped := redactFailure(red, inner)
	assert.Equal(t, "bad [REDACTED:ssn]", wrapped.Error())
	assert.ErrorIs(t, wrapped, inner, "the failure it came from is still reachable")
}
