package epcmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

var contact = &script.Script{ID: "s9", Name: "contact", Source: `
def form_spec(req):
    """The form."""
    return {"fields": [{"name": "email", "type": "email"}]}

def form_submit(req, values):
    """Accepts."""
    return None
`}

type forms struct {
	got     []epsource.SourceRequest
	err     error
	found   bool
	findErr error
}

func (f *forms) Find(context.Context, string) (epsource.SourceResult, bool, error) {
	return epsource.SourceResult{Source: "form-contact", Table: "webhook_form_contact"}, f.found, f.findErr
}

func (*forms) ConnectionChoices() []string { return []string{"lake", "scratch"} }

func (f *forms) Ensure(_ context.Context, req epsource.SourceRequest) (epsource.SourceResult, error) {
	f.got = append(f.got, req)
	if f.err != nil {
		return epsource.SourceResult{}, f.err
	}
	return epsource.SourceResult{Source: "form-contact", Table: "webhook_form_contact", Connection: "scratch", Created: true}, nil
}

// #2178: binding a form makes the source its submissions are stored in.
func TestSet_AFormGetsItsSource(t *testing.T) {
	s := newStore()
	h := handler(s)
	f := &forms{}
	h.SetForms(f)
	req := setReq(Input{Endpoint: "contact", AuthMode: "public", Methods: []string{"GET", "POST"}, Connection: "scratch", DecryptPersonas: []string{"support"}})
	req.Script = contact
	out, err := h.Set(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, f.got, 1)
	assert.Equal(t, epsource.SourceRequest{
		ScriptID: "s9", Endpoint: "contact", Connection: "scratch", DecryptPersonas: []string{"support"}, Actor: "admin@example.com",
	}, f.got[0])
	res, ok := out["form"].(epsource.SourceResult)
	require.True(t, ok)
	assert.Equal(t, "webhook_form_contact", res.Table)
	assert.Contains(t, out["message"], "stored as a row of webhook_form_contact on connection scratch")

	_, err = h.Set(context.Background(), setReq(Input{Endpoint: "lookup", AuthMode: "public"}))
	require.NoError(t, err)
	assert.Len(t, f.got, 1, "a script that is not a form gets no source")
}

func TestSet_AFormWhoseSourceCannotBeMade(t *testing.T) {
	h := handler(newStore())
	h.SetForms(&forms{err: errors.New("name the connection")})
	req := setReq(Input{Endpoint: "contact", AuthMode: "public"})
	req.Script = contact
	_, err := h.Set(context.Background(), req)
	require.ErrorContains(t, err, "is bound at https://data.example.com/endpoints/contact")
	require.ErrorContains(t, err, "name the connection")
}

func TestSet_NoFormSourcesBindsAsBefore(t *testing.T) {
	h := handler(newStore())
	req := setReq(Input{Endpoint: "contact", AuthMode: "public"})
	req.Script = contact
	out, err := h.Set(context.Background(), req)
	require.NoError(t, err)
	assert.NotContains(t, out, "form")
}

// #2178: adding an endpoint to a form says which connections its source
// could be made on; reading a form's endpoint handler says where its
// submissions are stored.
func TestDescribe_AForm(t *testing.T) {
	s := newStore()
	h := handler(s)
	f := &forms{}
	h.SetForms(f)
	ctx := context.Background()
	out, err := h.Choices(ctx, contact)
	require.NoError(t, err)
	assert.Equal(t, true, out["is_form"])
	assert.Equal(t, []string{"lake", "scratch"}, out["form_connections"])
	assert.NotContains(t, out, "page_url", "an unbound form has no page")

	req := setReq(Input{Endpoint: "contact", AuthMode: "public"})
	req.Script = contact
	_, err = h.Set(ctx, req)
	require.NoError(t, err)
	b := s.only(t, "s9")
	f.found = true
	out, err = h.Describe(ctx, contact, &b)
	require.NoError(t, err)
	res, ok := out["form"].(epsource.SourceResult)
	require.True(t, ok)
	assert.Equal(t, "webhook_form_contact", res.Table)
	assert.NotContains(t, out, "form_connections")
	assert.Equal(t, "https://data.example.com/forms/contact", out["page_url"], "#2331: the hosted page a site frames")

	f.findErr = errors.New("db down")
	_, err = h.Describe(ctx, contact, &b)
	require.ErrorIs(t, err, ErrStore)
	_, err = h.Choices(ctx, contact)
	require.ErrorIs(t, err, ErrStore)

	out, err = h.Choices(ctx, lookup)
	require.NoError(t, err)
	assert.NotContains(t, out, "is_form", "a script that is not a form says nothing")

	_, err = h.Set(ctx, setReq(Input{Endpoint: "lookup", AuthMode: "public"}))
	require.NoError(t, err)
	l := s.only(t, "s1")
	out, err = h.Describe(ctx, lookup, &l)
	require.NoError(t, err)
	assert.NotContains(t, out, "page_url", "a bound endpoint that is not a form has no page")
	assert.NotContains(t, out, "is_form")
}
