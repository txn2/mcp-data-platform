package scriptlayer

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// memBindings is an epbinding.Store over a map keyed by script, holding the
// name uniqueness the real table's constraint holds.
type memBindings struct {
	mu       sync.Mutex
	byScript map[string]epbinding.Binding
	failures []epbinding.Failure
	err      error
}

func (m *memBindings) Set(_ context.Context, b *epbinding.Binding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	for id, other := range m.byScript {
		if other.Name == b.Name && id != b.ScriptID {
			return epbinding.ErrNameTaken
		}
	}
	if b.ID == "" {
		b.ID = "ep_" + b.ScriptID
	}
	m.byScript[b.ScriptID] = *b
	return nil
}

func (m *memBindings) Get(_ context.Context, scriptID string) (*epbinding.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	b, ok := m.byScript[scriptID]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &b, nil
}

func (*memBindings) GetByName(context.Context, string) (*epbinding.Binding, error) {
	return nil, epbinding.ErrNotFound
}

func (m *memBindings) List(_ context.Context, f epbinding.Filter) ([]epbinding.Binding, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []epbinding.Binding{}
	for id, b := range m.byScript {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, id) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, len(out), nil
}

func (m *memBindings) SetEnabled(_ context.Context, scriptID string, enabled bool, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byScript[scriptID]
	if !ok {
		return epbinding.ErrNotFound
	}
	b.Enabled, b.UpdatedBy = enabled, actor
	m.byScript[scriptID] = b
	return nil
}

func (m *memBindings) RecordFailure(_ context.Context, f epbinding.Failure) error {
	m.failures = append(m.failures, f)
	return nil
}

func (m *memBindings) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	return append([]epbinding.Failure{}, m.failures...), nil
}

func (*memBindings) SweepFailures(context.Context, time.Time) (int64, error) { return 0, nil }

func (*memBindings) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{}, nil
}

const handlerSource = `def handle(req):
    """Answers with the request's id."""
    return {"json": req}

def test_handle():
    """Echoes the request."""
    assert.eq(handle(7)["json"], 7)
`

// endpointHandle is a script layer whose deployment can bind endpoints, with
// the author's script "lookup" saved.
func endpointHandle(t *testing.T, public bool) (*Handle, *memBindings) {
	t.Helper()
	bindings := &memBindings{byScript: map[string]epbinding.Binding{}}
	h := New(Config{Store: newMemStore(), Endpoints: &epwire.Endpoints{Commands: &epcmd.Handler{
		Store: bindings, BaseURL: "https://data.example.com",
		PublicAllowed: func(context.Context) (bool, error) { return public, nil },
	}}})
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "lookup", Source: handlerSource})
	require.False(t, res.IsError, resultText(res))
	return h, bindings
}

func endpointSet(in epcmd.Input) manageScriptInput {
	return manageScriptInput{
		Command: epcmd.CmdSet, Name: "lookup", OwnerEmail: "jane@example.com", TimeoutSeconds: 20, Input: in,
	}
}

// #2171: an administrator binds a script, and the binding is listed.
func TestEndpointSet_AnAdministratorBindsAndTheBindingIsListed(t *testing.T) {
	h, _ := endpointHandle(t, true)
	path := "/{id}"
	res := call(t, h, adminCtx(), endpointSet(epcmd.Input{
		Endpoint: "order-lookup", Methods: []string{"get", "post"}, Path: &path, AuthMode: epbinding.AuthSignedIn,
	}))
	require.False(t, res.IsError, resultText(res))
	out := resultFields(t, res)
	assert.Equal(t, "https://data.example.com/endpoints/order-lookup/{id}", out["url"])
	assert.Equal(t, float64(20), out["timeout_seconds"], "manage_script's timeout_seconds is the request's")
	assert.Contains(t, out["message"], "lookup now answers GET, POST at")

	listed := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: epcmd.CmdList, Name: "lookup"}))
	assert.Equal(t, true, listed["bound"])
	assert.Equal(t, "order-lookup", listed["endpoint"])
	assert.Equal(t, []any{}, listed["failures"], "no failed request is [] and never null")

	all := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: epcmd.CmdList}))
	assert.Equal(t, float64(1), all["count"], "the owner sees their own script's endpoint")
	everyone := resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: epcmd.CmdList}))
	assert.Equal(t, float64(1), everyone["count"])
}

// #2171: everyone but an administrator is refused, the owner included.
func TestEndpointCommands_RefuseEveryoneButAnAdministrator(t *testing.T) {
	h, bindings := endpointHandle(t, true)
	for _, cmd := range []string{epcmd.CmdSet, epcmd.CmdEnable, epcmd.CmdDisable} {
		in := endpointSet(epcmd.Input{Endpoint: "x", AuthMode: epbinding.AuthSignedIn})
		in.Command = cmd
		res := call(t, h, authorCtx(), in)
		require.True(t, res.IsError, cmd)
		assert.Contains(t, resultText(res), "only an administrator", cmd)
	}
	assert.Empty(t, bindings.byScript)

	res := call(t, h, callerCtx("bob@example.com", "analyst"), endpointSet(epcmd.Input{Endpoint: "x", AuthMode: epbinding.AuthSignedIn}))
	require.True(t, res.IsError)
	assert.Empty(t, bindings.byScript)
}

// #2171: a public binding is refused where there is no bot check, and
// accepted once there is one (or the override).
func TestEndpointSet_PublicNeedsABotCheck(t *testing.T) {
	h, _ := endpointHandle(t, false)
	res := call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "contact", AuthMode: epbinding.AuthPublic}))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "needs a bot check")

	h, _ = endpointHandle(t, true)
	res = call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "contact", AuthMode: epbinding.AuthPublic}))
	require.False(t, res.IsError, resultText(res))
}

// #2171: binding a script with no handle(req), and saving a bound script
// without one, are both refused naming the missing function.
func TestEndpoint_ABoundScriptMustDefineHandle(t *testing.T) {
	h, _ := endpointHandle(t, true)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "daily", Source: tested(inMain("print(1)\n"))})
	require.False(t, res.IsError, resultText(res))
	in := endpointSet(epcmd.Input{Endpoint: "daily", AuthMode: epbinding.AuthSignedIn})
	in.Name = "daily"
	res = call(t, h, adminCtx(), in)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "defines no handle(req)")

	res = call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "order-lookup", AuthMode: epbinding.AuthSignedIn}))
	require.False(t, res.IsError, resultText(res))
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "lookup", Source: tested(inMain("print(1)\n"))})
	msg, _ := resultFields(t, res)["message"].(string)
	assert.Contains(t, msg, "was not saved")
	assert.Contains(t, msg, `answers the endpoint "order-lookup"`)
	assert.Contains(t, msg, "defines no handle(req)")
}

func TestEndpointEnableAndDisable(t *testing.T) {
	h, bindings := endpointHandle(t, true)
	in := endpointSet(epcmd.Input{})
	in.Command = epcmd.CmdDisable
	res := call(t, h, adminCtx(), in)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not bound to an endpoint")

	require.False(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "order-lookup", AuthMode: epbinding.AuthSignedIn})).IsError)
	res = call(t, h, adminCtx(), in)
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, false, resultFields(t, res)["enabled"])
	for _, b := range bindings.byScript {
		assert.False(t, b.Enabled)
		assert.Equal(t, "admin@example.com", b.UpdatedBy)
	}
	in.Command = epcmd.CmdEnable
	assert.Equal(t, true, resultFields(t, call(t, h, adminCtx(), in))["enabled"])
}

func TestEndpointSet_ANameAnotherScriptHasIsRefused(t *testing.T) {
	h, _ := endpointHandle(t, true)
	require.False(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "orders", AuthMode: epbinding.AuthSignedIn})).IsError)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "lookup-two", Source: handlerSource})
	require.False(t, res.IsError, resultText(res))
	in := endpointSet(epcmd.Input{Endpoint: "orders", AuthMode: epbinding.AuthSignedIn})
	in.Name = "lookup-two"
	res = call(t, h, adminCtx(), in)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), `"orders" is taken`)
}

func TestEndpointList_AnUnboundScriptSaysSo(t *testing.T) {
	h, bindings := endpointHandle(t, true)
	out := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: epcmd.CmdList, Name: "lookup"}))
	assert.Equal(t, false, out["bound"])

	bindings.err = errors.New("boom")
	res := call(t, h, authorCtx(), manageScriptInput{Command: epcmd.CmdList, Name: "lookup"})
	assert.True(t, res.IsError)
}

func TestEndpointCommands_WithoutEndpointsAreUnavailable(t *testing.T) {
	h, _ := newHandle()
	res := call(t, h, adminCtx(), manageScriptInput{Command: epcmd.CmdList})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "cannot bind scripts to endpoints")
}

// A script run may not bind a script: an endpoint is a route anyone may be
// able to reach.
func TestEndpointSet_IsRefusedInsideARun(t *testing.T) {
	h, bindings := endpointHandle(t, true)
	ctx := adminCtx()
	middleware.GetPlatformContext(ctx).Source = middleware.SourceScript
	res := call(t, h, ctx, endpointSet(epcmd.Input{Endpoint: "x", AuthMode: epbinding.AuthSignedIn}))
	require.True(t, res.IsError)
	assert.Empty(t, bindings.byScript)
}

func TestEndpointsIsTheAssembledFeature(t *testing.T) {
	var nilHandle *Handle
	assert.Nil(t, nilHandle.Endpoints())
	h, _ := endpointHandle(t, true)
	require.NotNil(t, h.Endpoints())
	assert.NotNil(t, h.Endpoints().Commands)
}
