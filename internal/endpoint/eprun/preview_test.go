package eprun

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// starlarkExec runs a preview's function from its source in a bare Starlark
// thread, as a draft would.
func starlarkExec(t *testing.T, src string) Executor {
	t.Helper()
	return func(_ context.Context, name string, args starlark.Tuple) (starlark.Value, error) {
		thread := &starlark.Thread{}
		globals, err := starlark.ExecFileOptions(scriptdialect.Options, thread, "form", src, nil)
		if err != nil {
			return nil, fmt.Errorf("loading the source: %w", err)
		}
		v, err := starlark.Call(thread, globals[name], args, nil)
		if err != nil {
			return nil, fmt.Errorf("calling %s: %w", name, err)
		}
		return v, nil
	}
}

// previewSensitiveForm fails form_submit with the sensitive value in its message.
const previewSensitiveForm = `
def form_spec(req):
    return {"fields": [{"name": "ssn", "type": "text", "sensitive": True}]}

def form_submit(req, values):
    fail("refused " + values["ssn"])
`

// TestServePreview is #2494's preview answered by the endpoint's own rules:
// the spec and its token, a field error, the confirmation and the redirect,
// and nothing recorded: no audit row, no failed request, nothing stored.
func TestServePreview(t *testing.T) {
	f, now, stored := storingFixture(t)
	p := Preview{ID: "fp1", Script: f.scripts.sc, Version: 1, Source: contactForm, Exec: starlarkExec(t, contactForm)}
	ctx := context.Background()

	resp, err := f.runner.ServePreview(ctx, p, Request{Method: "GET", Query: map[string][]string{"customer": {"c-7"}}})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	token, _ := decode(t, resp.Body)["form_token"].(string)
	_, _, err = epform.Open(formKey, PreviewPrefix+"fp1", token, *now)
	require.NoError(t, err, "the token is bound to the preview")
	*now = now.Add(5 * time.Second)

	post := func(body string) (Response, error) {
		return f.runner.ServePreview(ctx, p, Request{Method: "POST", Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)})
	}
	resp, err = post(`{"email":"taken@example.com","customer":"c-7","form_token":"` + token + `"}`)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status, "form_submit's field error")
	resp, err = post(`{"email":"own@example.com","customer":"c-7","form_token":"` + token + `"}`)
	require.NoError(t, err)
	assert.Equal(t, "/welcome", decode(t, resp.Body)["redirect"])
	resp, err = post(`{"email":"ann@example.com","customer":"c-7","form_token":"` + token + `"}`)
	require.NoError(t, err)
	assert.Equal(t, "Thanks, we will write back.", decode(t, resp.Body)["confirmation"])
	resp, err = post(`{"email":"bad@example.com","customer":"c-7","form_token":"` + token + `"}`)
	require.Error(t, err, "a return that is not an answer is the author's to read")
	assert.Equal(t, http.StatusInternalServerError, resp.Status)

	// An endpoint's token never opens at a preview.
	other, err := epform.Sign(formKey, "orders", epform.Spec{Fields: []epform.Field{{Name: "email", Type: "email"}}}, now.Add(-time.Minute))
	require.NoError(t, err)
	resp, _ = post(`{"email":"ann@example.com","form_token":"` + other + `"}`)
	assert.Equal(t, http.StatusBadRequest, resp.Status)

	assert.Empty(t, stored.events, "an accepted preview submission reaches no form source")
	assert.Empty(t, f.audit.events, "a preview writes no endpoint audit row")
	assert.Empty(t, f.bindings.failures, "and keeps no failed request")
}

func TestServePreviewRefusals(t *testing.T) {
	f, now := formFixture(t)
	ctx := context.Background()
	notForm := Preview{ID: "fp2", Script: f.scripts.sc, Source: lookupSource, Exec: starlarkExec(t, lookupSource)}
	resp, err := f.runner.ServePreview(ctx, notForm, Request{Method: "GET"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)

	failing := Preview{ID: "fp3", Script: f.scripts.sc, Source: contactForm, Exec: func(context.Context, string, starlark.Tuple) (starlark.Value, error) {
		return nil, errors.New("boom")
	}}
	resp, err = f.runner.ServePreview(ctx, failing, Request{Method: "GET"})
	require.Error(t, err)
	assert.Contains(t, string(resp.Body), "form_spec failed: boom")

	// A sensitive value never reaches the failure the author reads.
	enc, err := fieldcrypt.NewFieldEncryptor(formKey)
	require.NoError(t, err)
	f.runner.SetCollector(&epcollect.Collector{Sources: formSource{found: true}, Writer: &deliveries{}, Sealer: fieldcrypt.NewRestFieldEncryptor(enc)})
	p := Preview{ID: "fp4", Script: f.scripts.sc, Source: previewSensitiveForm, Exec: starlarkExec(t, previewSensitiveForm)}
	resp, err = f.runner.ServePreview(ctx, p, Request{Method: "GET"})
	require.NoError(t, err, string(resp.Body))
	token, _ := decode(t, resp.Body)["form_token"].(string)
	*now = now.Add(5 * time.Second)
	resp, err = f.runner.ServePreview(ctx, p, Request{
		Method: "POST", Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"ssn":"123-45-6789","form_token":"` + token + `"}`),
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "123-45-6789")
	assert.NotContains(t, string(resp.Body), "123-45-6789")

	f.runner.FormKey = nil
	resp, err = f.runner.ServePreview(ctx, Preview{ID: "fp5", Script: f.scripts.sc, Source: contactForm}, Request{Method: "GET"})
	require.ErrorIs(t, err, errNoForm)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status)
}

func TestDescribeUnsaved(t *testing.T) {
	values := map[string]any{"cv": []any{map[string]any{"name": "a.pdf"}, map[string]any{"name": "b.pdf", epform.FileKeyURI: "kept"}}}
	describeUnsaved(values, []epform.Upload{{Field: "cv"}, {Field: "missing"}})
	list, ok := values["cv"].([]any)
	require.True(t, ok)
	first, ok := list[0].(map[string]any)
	require.True(t, ok)
	second, ok := list[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", first[epform.FileKeyURI])
	assert.Equal(t, "kept", second[epform.FileKeyURI])
}

// TestPublicFormServesItsApprovedVersion is #2494's sixth requirement: a
// public form serves a caller with no session the version approved for it,
// a signed-in caller the version the script runs, and with no approved
// version serves the former nothing.
func TestPublicFormServesItsApprovedVersion(t *testing.T) {
	f, _ := formFixture(t)
	f.bindings.byName["orders"].AuthMode = epbinding.AuthPublic
	f.scripts.save(strings.Replace(contactForm, "Thanks, we will write back.", "Version two thanks.", 1))
	ctx := context.Background()
	anon := Request{Method: "GET"}
	signedIn := Request{Method: "GET", Caller: &Caller{UserID: "u1", Email: "ann@example.com"}}

	resp := f.serve(t, anon)
	assert.Equal(t, http.StatusNotFound, resp.Status, "no approved version serves a caller with no session nothing")
	assert.Contains(t, string(resp.Body), "waiting for an administrator's approval")
	assert.Equal(t, http.StatusOK, f.serve(t, signedIn).Status)

	approve := func(v int) {
		f.bindings.mu.Lock()
		st := f.bindings.stamps["orders"]
		st.ApprovedVersion = v
		f.bindings.stamps["orders"] = st
		f.bindings.mu.Unlock()
	}
	approve(1)
	tg, err := f.runner.Resolve(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, 1, tg.PublicVersion())
	assert.Equal(t, 2, tg.Version.Version)
	assert.Equal(t, "Thanks, we will write back.", spec(t, f, anon)["confirmation"], "the approved version")
	assert.Equal(t, "Version two thanks.", spec(t, f, signedIn)["confirmation"], "the version the script runs")

	// A held module of the approved version is kept across a reload.
	f.bindings.touch("orders")
	again, err := f.runner.Resolve(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, tg.public.program, again.public.program)

	approve(2)
	tg, err = f.runner.Resolve(ctx, "orders")
	require.NoError(t, err)
	assert.Same(t, tg, tg.public, "an approved version that is the one run is the same target")

	approve(9)
	tg, err = f.runner.Resolve(ctx, "orders")
	require.NoError(t, err)
	assert.Zero(t, tg.PublicVersion(), "an approved version that no longer exists serves none")
}

func spec(t *testing.T, f *fixture, req Request) map[string]any {
	t.Helper()
	resp := f.serve(t, req)
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	s, _ := decode(t, resp.Body)["spec"].(map[string]any)
	return s
}

func TestTargetFor(t *testing.T) {
	auth := &Target{Binding: &epbinding.Binding{AuthMode: epbinding.AuthAuthenticated}, Form: true, Version: &script.Version{}}
	assert.Same(t, auth, auth.For(nil), "an authenticated form serves its version to everyone it admits")
	handler := &Target{Binding: &epbinding.Binding{AuthMode: epbinding.AuthPublic}, Version: &script.Version{}}
	assert.Same(t, handler, handler.For(nil), "an endpoint that is not a form has no approved version")
}

// #2494: a form's owner whose approved form is public cannot save a version
// that is not a form and have it served publicly: a caller with no session
// keeps the approved form, a signed-in caller gets the version the script runs.
func TestAnApprovedFormThatBecomesAHandlerKeepsServingTheForm(t *testing.T) {
	f, _ := formFixture(t)
	f.bindings.byName["orders"].AuthMode = epbinding.AuthPublic
	f.bindings.mu.Lock()
	st := f.bindings.stamps["orders"]
	st.ApprovedVersion = 1
	f.bindings.stamps["orders"] = st
	f.bindings.mu.Unlock()
	f.scripts.save(lookupSource)

	tg, err := f.runner.Resolve(context.Background(), "orders")
	require.NoError(t, err)
	assert.False(t, tg.Form, "the version the script runs is a handler")
	assert.True(t, tg.ServesPublicly())
	assert.Equal(t, 1, tg.PublicVersion())
	assert.Equal(t, "Thanks, we will write back.", spec(t, f, Request{Method: "GET"})["confirmation"],
		"a caller with no session is served the approved form, never the handler")
}
