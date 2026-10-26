package epwire

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const formSource = `
def form_spec(req):
    """The form."""
    return {"fields": [{"name": "email", "type": "email"}]}

def form_submit(req, values):
    """Accepts."""
    return None
`

// bindings serves "apply" (an authenticated form) and fails "broken".
type bindings struct{ epbinding.Store }

func (bindings) Stamp(_ context.Context, name string) (epbinding.Stamp, error) {
	switch name {
	case "apply":
		return epbinding.Stamp{}, nil
	case "broken":
		return epbinding.Stamp{}, errors.New("db down")
	}
	return epbinding.Stamp{}, epbinding.ErrNotFound
}

func (bindings) GetByName(context.Context, string) (*epbinding.Binding, error) {
	return &epbinding.Binding{Name: "apply", ScriptID: "s1", AuthMode: epbinding.AuthAuthenticated, Enabled: true}, nil
}

type scripts struct{}

func (scripts) GetByID(context.Context, string) (*script.Script, error) {
	return &script.Script{ID: "s1", Version: 1, OwnerEmail: "owner@example.com"}, nil
}

func (scripts) GetVersion(context.Context, string, int) (*script.Version, error) {
	return &script.Version{ID: "v1", Version: 1, Source: formSource}, nil
}

// #2177: an asset's declaration of mcp:form:<endpoint> reads whether the
// endpoint serves a form, who may embed it, and who owns it.
func TestForms(t *testing.T) {
	var none *Endpoints
	assert.Nil(t, none.Forms())

	forms := (&Endpoints{Runner: &eprun.Runner{Bindings: bindings{}, Scripts: scripts{}}}).Forms()
	f, err := forms.Form(context.Background(), "apply")
	require.NoError(t, err)
	assert.Equal(t, assetrefs.Form{Form: true, Authenticated: true, OwnerEmail: "owner@example.com"}, f)

	_, err = forms.Form(context.Background(), "missing")
	require.ErrorIs(t, err, assetrefs.ErrNoForm)
	_, err = forms.Form(context.Background(), "broken")
	require.ErrorContains(t, err, "db down")
}
