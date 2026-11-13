package scriptlayer

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// memBindings is an epbinding.Store over a map keyed by binding id, holding
// the name uniqueness the real table's constraint holds and a replace to the
// binding's own script.
type memBindings struct {
	mu       sync.Mutex
	byID     map[string]epbinding.Binding
	failures []epbinding.Failure
	seq      int
	err      error
}

func (m *memBindings) Set(_ context.Context, b *epbinding.Binding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if b.ID != "" {
		if prev, ok := m.byID[b.ID]; !ok || prev.ScriptID != b.ScriptID {
			return epbinding.ErrNotFound
		}
	}
	for id, other := range m.byID {
		if other.Name == b.Name && id != b.ID {
			return epbinding.ErrNameTaken
		}
	}
	if b.ID == "" {
		m.seq++
		b.ID, b.CreatedAt = fmt.Sprintf("ep_%d", m.seq), time.Unix(int64(m.seq), 0)
	}
	m.byID[b.ID] = *b
	return nil
}

func (m *memBindings) GetByID(_ context.Context, id string) (*epbinding.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	b, ok := m.byID[id]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &b, nil
}

func (m *memBindings) ByScript(_ context.Context, scriptID string) ([]epbinding.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := []epbinding.Binding{}
	for _, b := range m.byID {
		if b.ScriptID == scriptID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memBindings) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.byID {
		if b.Name == name {
			return &b, nil
		}
	}
	return nil, epbinding.ErrNotFound
}

func (m *memBindings) List(_ context.Context, f epbinding.Filter) ([]epbinding.Binding, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []epbinding.Binding{}
	for _, b := range m.byID {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, b.ScriptID) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, len(out), nil
}

func (m *memBindings) Enable(_ context.Context, id string, enabled bool, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byID[id]
	if !ok {
		return epbinding.ErrNotFound
	}
	b.Enabled, b.UpdatedBy = enabled, actor
	m.byID[id] = b
	return nil
}

func (m *memBindings) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return epbinding.ErrNotFound
	}
	delete(m.byID, id)
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

// endpointSet is handler_set kind=endpoint on jane's lookup.
func endpointSet(in epcmd.Input) manageScriptInput {
	return manageScriptInput{
		Command: cmdHandlerSet, Name: "lookup", OwnerEmail: "jane@example.com", TimeoutSeconds: 20, Input: in,
		Kind: scripthandler.KindEndpoint,
	}
}

// byID is a handler command on one handler.
func byID(command, id string) manageScriptInput {
	return manageScriptInput{Command: command, Handler: id}
}

// #2171, #2441: an administrator adds an endpoint handler, and it is listed
// and read by its id.
func TestHandlerSet_AnAdministratorAddsAnEndpointAndItIsListed(t *testing.T) {
	h, _ := handlerHandle(t, "default")
	path := "/{id}"
	res := call(t, h, adminCtx(), endpointSet(epcmd.Input{
		Endpoint: "order-lookup", Methods: []string{"get", "post"}, Path: &path, AuthMode: epbinding.AuthAuthenticated,
	}))
	require.False(t, res.IsError, resultText(res))
	out := resultFields(t, res)
	assert.Equal(t, "https://data.example.com/endpoints/order-lookup/{id}", out["url"])
	assert.Equal(t, float64(20), out["timeout_seconds"], "manage_script's timeout_seconds is the request's")
	assert.Contains(t, out["message"], "lookup now answers GET, POST at")
	id, _ := out["handler"].(string)
	require.NotEmpty(t, id, "the result names the handler by its id")

	listed := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHandlerList, Name: "lookup"}))
	assert.Equal(t, float64(1), listed["count"])
	items, _ := listed["handlers"].([]any)
	require.Len(t, items, 1)
	one, _ := items[0].(map[string]any)
	assert.Equal(t, "order-lookup", one["endpoint"])
	assert.Equal(t, scripthandler.KindEndpoint, one["type"])
	assert.Equal(t, []any{}, one["failures"], "no failed request is [] and never null")

	got := resultFields(t, call(t, h, authorCtx(), byID(cmdHandlerGet, id)))
	assert.Equal(t, "order-lookup", got["endpoint"], "the owner reads their script's endpoint by its id")

	mine := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHandlerList}))
	assert.Equal(t, float64(1), mine["count"], "the owner sees their own script's endpoint")
	everyone := resultFields(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdHandlerList}))
	assert.Equal(t, float64(1), everyone["count"])
	nobody := resultFields(t, call(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{Command: cmdHandlerList}))
	assert.Equal(t, float64(0), nobody["count"], "a handler is as visible as the script it starts")
}

// #2441: a script holds several endpoint handlers; a set naming none of a
// script holding several is refused naming them, and one changes by its id.
func TestHandlerSet_AScriptHoldsSeveralEndpoints(t *testing.T) {
	h, w := handlerHandle(t, "default")
	first := resultFields(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated})))
	second := resultFields(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "orders-archive", AuthMode: epbinding.AuthAuthenticated})))
	require.NotEqual(t, first["handler"], second["handler"])
	assert.Len(t, w.bindings.byID, 2)

	res := call(t, h, adminCtx(), endpointSet(epcmd.Input{AuthMode: epbinding.AuthPublic}))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "script lookup has 2 endpoint handlers (orders, orders-archive)")

	rename := endpointSet(epcmd.Input{Endpoint: "orders-v2"})
	rename.Handler, _ = first["handler"].(string)
	out := resultFields(t, call(t, h, adminCtx(), rename))
	assert.Equal(t, first["handler"], out["handler"])
	assert.Equal(t, "orders-v2", w.bindings.byID[rename.Handler].Name)

	listed := resultFields(t, call(t, h, adminCtx(), manageScriptInput{
		Command: cmdHandlerList, Name: "lookup", OwnerEmail: "jane@example.com", Kind: scripthandler.KindEndpoint,
	}))
	assert.Equal(t, float64(2), listed["count"])
}

// #2171, narrowed by #2494: an endpoint that is not a form is an
// administrator's to add, change, turn on or off and delete; its owner is
// refused naming why, and another person's script is not found.
func TestEndpointHandlers_RefuseEveryoneButAnAdministrator(t *testing.T) {
	h, w := handlerHandle(t, "default")
	res := call(t, h, authorCtx(), endpointSet(epcmd.Input{Endpoint: "x", AuthMode: epbinding.AuthAuthenticated}))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "an administrator binds an endpoint that is not a form")
	assert.Empty(t, w.bindings.byID)

	added := resultFields(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "x", AuthMode: epbinding.AuthAuthenticated})))
	id, _ := added["handler"].(string)
	for _, cmd := range []string{cmdHandlerEnable, cmdHandlerDisable, cmdHandlerDelete} {
		res := call(t, h, authorCtx(), byID(cmd, id))
		require.True(t, res.IsError, cmd)
		assert.Contains(t, resultText(res), "an administrator binds an endpoint that is not a form", cmd)
	}
	assert.True(t, w.bindings.byID[id].Enabled)

	res = call(t, h, callerCtx("bob@example.com", "analyst"), endpointSet(epcmd.Input{Endpoint: "y", AuthMode: epbinding.AuthAuthenticated}))
	require.True(t, res.IsError)
	res = call(t, h, callerCtx("bob@example.com", "analyst"), byID(cmdHandlerGet, id))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "is not yours")
	assert.Len(t, w.bindings.byID, 1)
}

// #2259: a public endpoint needs no check; a bot_check one names a check that
// exists, and signed_in is no longer a mode.
func TestHandlerSet_EndpointModesAndTheChecksTheyName(t *testing.T) {
	h, _ := handlerHandle(t, "default")
	res := call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "contact", AuthMode: epbinding.AuthPublic}))
	require.False(t, res.IsError, resultText(res))

	res = call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "contact", AuthMode: epbinding.AuthBotCheck, BotCheck: "gone"}))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), `no bot check is named "gone"`)

	res = call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "contact", AuthMode: epbinding.AuthBotCheck, BotCheck: "default"}))
	require.False(t, res.IsError, resultText(res))

	res = call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "contact", AuthMode: "signed_in"}))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), `"signed_in" is not one of`)
}

// #2171, #2441: adding an endpoint to a script with no handle(req), and
// saving a script holding one without it, are both refused naming the
// missing function and the handler.
func TestEndpointHandlers_AScriptMustKeepDefiningHandle(t *testing.T) {
	h, _ := handlerHandle(t, "default")
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "daily", Source: tested(inMain("print(1)\n"))})
	require.False(t, res.IsError, resultText(res))
	in := endpointSet(epcmd.Input{Endpoint: "daily", AuthMode: epbinding.AuthAuthenticated})
	in.Name = "daily"
	res = call(t, h, adminCtx(), in)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "defines no handle(req)")

	added := resultFields(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "order-lookup", AuthMode: epbinding.AuthAuthenticated})))
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "lookup", Source: tested(inMain("print(1)\n"))})
	msg, _ := resultFields(t, res)["message"].(string)
	assert.Contains(t, msg, "was not saved")
	assert.Contains(t, msg, fmt.Sprintf(`answers the endpoint "order-lookup" (endpoint handler %s)`, added["handler"]))
	assert.Contains(t, msg, "defines no handle(req)")
}

func TestEndpointHandlers_DisableEnableDelete(t *testing.T) {
	h, w := handlerHandle(t, "default")
	res := call(t, h, adminCtx(), byID(cmdHandlerDisable, "ep_404"))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no handler ep_404")

	added := resultFields(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "order-lookup", AuthMode: epbinding.AuthAuthenticated})))
	id, _ := added["handler"].(string)
	res = call(t, h, adminCtx(), byID(cmdHandlerDisable, id))
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, false, resultFields(t, res)["enabled"])
	assert.False(t, w.bindings.byID[id].Enabled)
	assert.Equal(t, "admin@example.com", w.bindings.byID[id].UpdatedBy)
	assert.Equal(t, true, resultFields(t, call(t, h, adminCtx(), byID(cmdHandlerEnable, id)))["enabled"])

	gone := resultFields(t, call(t, h, adminCtx(), byID(cmdHandlerDelete, id)))
	assert.Equal(t, true, gone["deleted"])
	assert.Empty(t, w.bindings.byID)
}

func TestHandlerSet_AnEndpointNameAnotherScriptHasIsRefusedNamingItsHandler(t *testing.T) {
	h, _ := handlerHandle(t, "default")
	held := resultFields(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated})))
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "lookup-two", Source: handlerSource})
	require.False(t, res.IsError, resultText(res))
	in := endpointSet(epcmd.Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated})
	in.Name = "lookup-two"
	res = call(t, h, adminCtx(), in)
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), fmt.Sprintf(`endpoint name "orders" is taken by endpoint handler %s`, held["handler"]))
}

// A script run may not add, change, turn on or off or delete a handler: an
// endpoint is a route anyone may be able to reach, and a schedule is work
// nobody asked for.
func TestHandlerCommands_AreRefusedInsideARun(t *testing.T) {
	h, w := handlerHandle(t, "default")
	added := resultFields(t, call(t, h, adminCtx(), endpointSet(epcmd.Input{Endpoint: "x", AuthMode: epbinding.AuthAuthenticated})))
	id, _ := added["handler"].(string)
	ctx := adminCtx()
	middleware.GetPlatformContext(ctx).Source = middleware.SourceScript

	res := call(t, h, ctx, endpointSet(epcmd.Input{Endpoint: "y", AuthMode: epbinding.AuthAuthenticated}))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "manage_script handler_set cannot be called from inside a script run")
	for _, cmd := range []string{cmdHandlerEnable, cmdHandlerDisable, cmdHandlerDelete} {
		res := call(t, h, ctx, byID(cmd, id))
		require.True(t, res.IsError, cmd)
		assert.Contains(t, resultText(res), "manage_script "+cmd+" cannot be called from inside a script run")
	}
	assert.Len(t, w.bindings.byID, 1)
	assert.True(t, w.bindings.byID[id].Enabled)

	res = call(t, h, ctx, byID(cmdHandlerGet, id))
	assert.False(t, res.IsError, "reading a handler changes nothing: %s", resultText(res))
}

func TestEndpointsIsTheAssembledFeature(t *testing.T) {
	var nilHandle *Handle
	assert.Nil(t, nilHandle.Endpoints())
	h, _ := handlerHandle(t, "default")
	require.NotNil(t, h.Endpoints())
	assert.NotNil(t, h.Endpoints().Commands)
}

// The handler service and the save gate's handler check exist only where
// there is a database to keep handlers in; the gate an application import
// saves through is the layer's own (#2476), carrying that check.
func TestHandlersAndTheSaveGateAreTheLayersOwn(t *testing.T) {
	bare, _ := newHandle()
	assert.Nil(t, bare.Handlers(), "no database, no handler service")
	assert.Nil(t, bare.SaveGate().Handlers, "and no handler check on a save")

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	commands := &epcmd.Handler{}
	h := New(Config{Store: newMemStore(), DB: db, Endpoints: &epwire.Endpoints{Commands: commands}})
	require.NotNil(t, h.Handlers())
	assert.Same(t, commands, h.Handlers().Endpoints, "the endpoint kind is the deployment's endpoint commands")
	assert.Equal(t, scripthandler.View{DB: db}, h.Handlers().View)
	assert.NotNil(t, h.SaveGate().Handlers, "a save is checked against the script's handlers")

	noEndpoints := New(Config{Store: newMemStore(), DB: db})
	require.NotNil(t, noEndpoints.Handlers())
	assert.Nil(t, noEndpoints.Handlers().Endpoints, "a deployment that cannot bind endpoints has no endpoint kind")
}

// #2494: form_preview reaches the endpoint commands with the caller as the
// preview's opener and the source sent, and answers why it cannot preview.
func TestFormPreview_IsAnEndpointCommand(t *testing.T) {
	h, _ := handlerHandle(t, "default")
	res := call(t, h, authorCtx(), manageScriptInput{Command: epcmd.CmdFormPreview, Name: "lookup", Source: "def main():\n    pass\n"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "cannot preview a form", "a deployment with no previews says so")

	res = call(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{Command: epcmd.CmdFormPreview, Name: "lookup"})
	require.True(t, res.IsError, "a script the caller does not own is not previewed")

	bare, _ := newHandle()
	res = call(t, bare, authorCtx(), manageScriptInput{Command: epcmd.CmdFormPreview, Name: "lookup"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "cannot bind scripts to endpoints")
}
