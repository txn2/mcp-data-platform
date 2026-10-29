package epcollect

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func sealer(t *testing.T) *fieldcrypt.RestFieldEncryptor {
	t.Helper()
	enc, err := fieldcrypt.NewFieldEncryptor(testKey)
	require.NoError(t, err)
	return fieldcrypt.NewRestFieldEncryptor(enc)
}

type fakeSources struct{ src *whsource.Source }

func (f fakeSources) FormSource(context.Context, string) (whsource.Source, bool) {
	if f.src == nil {
		return whsource.Source{}, false
	}
	return *f.src, true
}

type fakeWriter struct {
	events []whevent.Event
	err    error
}

func (f *fakeWriter) Deliver(_ context.Context, _ whsource.Source, ev whevent.Event, _ []byte) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, ev)
	return nil
}

func formSrc() *whsource.Source {
	return &whsource.Source{
		Name: "form-apply", Connection: "scratch",
		Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "scr_1", Endpoint: "apply", DecryptPersonas: []string{"credit"}}},
	}
}

func spec() epform.Spec {
	return epform.Spec{Fields: []epform.Field{
		{Name: "name", Type: epform.TypeText},
		{Name: "ssn", Type: epform.TypeText, Sensitive: true},
		{Name: "income", Type: epform.TypeNumber, Sensitive: true},
	}}
}

func TestStore_SealsSensitiveFieldsAndRecordsTheSubmitter(t *testing.T) {
	w := &fakeWriter{}
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	c := &Collector{Sources: fakeSources{src: formSrc()}, Writer: w, Sealer: sealer(t), Now: func() time.Time { return at }}
	err := c.Store(t.Context(), Submission{
		ScriptID: "scr_1", Endpoint: "apply", Version: 3, Spec: spec(),
		Values:    map[string]any{"name": "Ana", "ssn": "123-45-6789", "income": int64(52000)},
		Submitter: &Submitter{UserID: "u1", Email: "ana@example.com", AuthType: "oidc"},
	})
	require.NoError(t, err)
	require.Len(t, w.events, 1)
	ev := w.events[0]
	assert.Equal(t, EventType, ev.EventType)
	assert.Equal(t, "ana@example.com", ev.Key)
	assert.Equal(t, at, ev.ReceivedAt)
	assert.Len(t, ev.EventID, 32)
	assert.NotContains(t, string(ev.Payload), "123-45-6789", "the payload holds the ciphertext alone")
	assert.NotContains(t, string(ev.Payload), "52000")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(ev.Payload, &payload))
	assert.Equal(t, []any{"name", "ssn", "income"}, payload[KeyFields])
	assert.Equal(t, []any{"ssn", "income"}, payload[KeySensitive])
	assert.Equal(t, map[string]any{"user_id": "u1", "email": "ana@example.com", "auth_type": "oidc"}, payload[KeySubmitter])

	values, sensitive := PayloadValues(payload)
	opened, err := Reveal(sealer(t), "form-apply", values, sensitive)
	require.NoError(t, err)
	assert.Equal(t, "Ana", opened["name"])
	assert.Equal(t, "123-45-6789", opened["ssn"])
	assert.InDelta(t, 52000, opened["income"], 0, "a number reads back as a number")
}

func TestStore_Refusals(t *testing.T) {
	sub := Submission{ScriptID: "scr_1", Endpoint: "apply", Spec: spec(), Values: map[string]any{"name": "Ana", "ssn": "1", "income": 1}}
	c := &Collector{Sources: fakeSources{}, Writer: &fakeWriter{}, Sealer: sealer(t)}
	require.ErrorIs(t, c.Store(t.Context(), sub), ErrNoSource)

	c.Sources = fakeSources{src: formSrc()}
	c.Sealer = fieldcrypt.NewRestFieldEncryptor(nil)
	require.ErrorIs(t, c.Store(t.Context(), sub), ErrCannotSeal)
	assert.False(t, c.CanSeal())

	c.Sealer = sealer(t)
	c.Writer = &fakeWriter{err: errors.New("spool full")}
	err := c.Store(t.Context(), sub)
	require.ErrorContains(t, err, "form-apply")
	require.ErrorContains(t, err, "spool full")

	var nilc *Collector
	assert.False(t, nilc.CanSeal())
}

func TestStore_APublicFormWithNoSensitiveField(t *testing.T) {
	w := &fakeWriter{}
	c := &Collector{Sources: fakeSources{src: formSrc()}, Writer: w}
	require.NoError(t, c.Store(t.Context(), Submission{
		ScriptID: "scr_1", Spec: epform.Spec{Fields: []epform.Field{{Name: "email", Type: epform.TypeEmail}}},
		Values: map[string]any{"email": "a@example.com"},
	}))
	require.Len(t, w.events, 1)
	assert.Empty(t, w.events[0].Key, "a public form records no submitter")
	assert.Contains(t, string(w.events[0].Payload), "a@example.com")
	assert.NotContains(t, string(w.events[0].Payload), KeySubmitter)
}

func TestReveal_Refusals(t *testing.T) {
	s := sealer(t)
	same, err := Reveal(nil, "form-apply", map[string]any{"a": 1}, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"a": 1}, same)

	_, err = Reveal(fieldcrypt.NewRestFieldEncryptor(nil), "form-apply", map[string]any{"ssn": "encb:x"}, []string{"ssn"})
	require.ErrorContains(t, err, "ENCRYPTION_KEY")
	_, err = Reveal(s, "form-apply", map[string]any{"ssn": 5}, []string{"ssn"})
	require.ErrorContains(t, err, "not encrypted")
	_, err = Reveal(s, "form-apply", map[string]any{"ssn": "encb:AAAA"}, []string{"ssn"})
	require.ErrorContains(t, err, "not sealed by this form")
	notJSON, err := s.SealBound(Binding("form-apply", "ssn"), "Not json")
	require.NoError(t, err)
	_, err = Reveal(s, "form-apply", map[string]any{"ssn": notJSON}, []string{"ssn"})
	require.ErrorContains(t, err, "does not hold a submitted value")
	assert.NotContains(t, err.Error(), "N", "the refusal never quotes what it opened")
	missing, err := Reveal(s, "form-apply", map[string]any{"a": 1}, []string{"ssn"})
	require.NoError(t, err, "a sensitive field a submission did not hold is skipped")
	assert.Equal(t, map[string]any{"a": 1}, missing)
}

// #2355: a value opens only for the form and field it was sealed for, and a
// value the platform sealed for anything else (a stored secret, another
// form, a value sealed before fields were bound) opens nothing through
// Reveal; RevealStored, for rows read from the form's own source, shows one
// sealed before fields were bound as unreadable.
func TestReveal_OpensOnlyWhatTheFormSealed(t *testing.T) {
	s := sealer(t)
	ours, err := s.SealBound(Binding("form-apply", "ssn"), `"123-45-6789"`)
	require.NoError(t, err)
	got, err := Reveal(s, "form-apply", map[string]any{"ssn": ours}, []string{"ssn"})
	require.NoError(t, err)
	assert.Equal(t, "123-45-6789", got["ssn"])

	otherForm, err := s.SealBound(Binding("form-other", "ssn"), `"999-99-9999"`)
	require.NoError(t, err)
	otherField, err := s.SealBound(Binding("form-apply", "income"), `52000`)
	require.NoError(t, err)
	storedSecret, err := s.Seal(`{"private_key":"-----BEGIN"}`)
	require.NoError(t, err)
	for name, sealed := range map[string]string{"another form": otherForm, "another field": otherField, "a stored secret": storedSecret} {
		_, err := Reveal(s, "form-apply", map[string]any{"ssn": sealed}, []string{"ssn"})
		require.Error(t, err, name)
		assert.NotContains(t, err.Error(), "999", name)
		assert.NotContains(t, err.Error(), "BEGIN", name)
	}

	legacy, err := s.Seal(`"111-11-1111"`)
	require.NoError(t, err)
	_, err = Reveal(s, "form-apply", map[string]any{"ssn": legacy}, []string{"ssn"})
	require.Error(t, err, "a script cannot open a value sealed before fields were bound")
	old, err := RevealStored(s, "form-apply", map[string]any{"ssn": legacy}, []string{"ssn"})
	require.NoError(t, err)
	assert.Equal(t, SealedUnbound, old["ssn"], "the owner's view shows it as unreadable and opens nothing")
	ok, err := RevealStored(s, "form-apply", map[string]any{"ssn": ours}, []string{"ssn"})
	require.NoError(t, err)
	assert.Equal(t, "123-45-6789", ok["ssn"], "a value the form sealed reads in the owner's view")
}

func TestPayloadValues(t *testing.T) {
	v, s := PayloadValues(map[string]any{})
	assert.Equal(t, map[string]any{}, v)
	assert.Empty(t, s)
	_, s = PayloadValues(map[string]any{KeySensitive: []string{"a"}})
	assert.Equal(t, []string{"a"}, s)
}

// --- Decrypter ---

type listed struct{ list []whsource.Source }

func (l listed) List(context.Context) ([]whsource.Source, error) { return l.list, nil }

type recordAudit struct{ events []middleware.AuditEvent }

func (r *recordAudit) Log(_ context.Context, e middleware.AuditEvent) error {
	r.events = append(r.events, e)
	return nil
}

func storedPayload(t *testing.T) map[string]any {
	t.Helper()
	c := &Collector{Sealer: sealer(t)}
	_, body, err := c.Event(Submission{Spec: spec(), Values: map[string]any{"name": "Ana", "ssn": "123-45-6789", "income": 1}}, "form-apply")
	require.NoError(t, err)
	var p map[string]any
	require.NoError(t, json.Unmarshal(body, &p))
	return p
}

func TestDecrypt_ForAnAdmittedPersona(t *testing.T) {
	aud := &recordAudit{}
	d := &Decrypter{Sources: listed{list: []whsource.Source{*formSrc()}}, Sealer: sealer(t), Audit: aud}
	values, opened, err := d.Decrypt(t.Context(), scripttoken.DecryptRequest{
		Form: "apply", Payload: storedPayload(t), Persona: "credit", At: scripttoken.Binding{ScriptID: "scr_9"},
	})
	require.NoError(t, err)
	assert.Equal(t, "123-45-6789", values["ssn"])
	assert.Equal(t, []string{"ssn", "income"}, opened)
	require.Len(t, aud.events, 1)
	e := aud.events[0]
	assert.Equal(t, "script:scr_9", e.UserID)
	assert.Equal(t, "form_decrypt", e.EventKind)
	assert.Equal(t, []string{"ssn", "income"}, e.Parameters["fields"])
	assert.True(t, e.Success)
}

func TestDecrypt_Refusals(t *testing.T) {
	aud := &recordAudit{}
	d := &Decrypter{Sources: listed{list: []whsource.Source{*formSrc()}}, Sealer: sealer(t), Audit: aud}
	_, _, err := d.Decrypt(t.Context(), scripttoken.DecryptRequest{Form: "apply", Payload: storedPayload(t), Persona: "analyst", At: scripttoken.Binding{Endpoint: "lookup"}})
	require.ErrorContains(t, err, `persona "analyst"`)
	require.Len(t, aud.events, 1)
	assert.False(t, aud.events[0].Success)
	assert.False(t, aud.events[0].Authorized, "a refused read is recorded as refused")
	assert.Equal(t, "endpoint:lookup", aud.events[0].UserID)

	values, _, err := d.Decrypt(t.Context(), scripttoken.DecryptRequest{Form: "form-apply", Payload: storedPayload(t), Admin: true})
	require.NoError(t, err, "the administrator always may, and a form is named by its source too")
	assert.Equal(t, "Ana", values["name"])

	_, _, err = d.Decrypt(t.Context(), scripttoken.DecryptRequest{Form: "nope", Admin: true})
	require.ErrorContains(t, err, "no form named")

	broken := storedPayload(t)
	brokenValues, ok := broken[KeyValues].(map[string]any)
	require.True(t, ok)
	brokenValues["ssn"] = "enc:AAAA"
	_, _, err = d.Decrypt(t.Context(), scripttoken.DecryptRequest{Form: "apply", Payload: broken, Admin: true})
	require.Error(t, err)
	assert.False(t, aud.events[len(aud.events)-1].Success)
	assert.True(t, aud.events[len(aud.events)-1].Authorized)
}
