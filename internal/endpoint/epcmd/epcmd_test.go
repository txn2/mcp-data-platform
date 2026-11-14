package epcmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// store is an epbinding.Store over one map keyed by binding id, with an
// error a test sets per method. Set holds a replace to the binding's own
// script and a name to one binding, as the database does.
type store struct {
	byID                 map[string]epbinding.Binding
	seq                  int
	getErr, setErr       error
	byScriptErr          error
	byNameErr            error
	listErr, failuresErr error
	deleteErr            error
	enableErr            string
}

func newStore() *store { return &store{byID: map[string]epbinding.Binding{}} }

func (s *store) Set(_ context.Context, b *epbinding.Binding) error {
	if s.setErr != nil {
		return s.setErr
	}
	if b.ID != "" {
		if prev, ok := s.byID[b.ID]; !ok || prev.ScriptID != b.ScriptID {
			return epbinding.ErrNotFound
		}
	}
	for id, other := range s.byID {
		if other.Name == b.Name && id != b.ID {
			return epbinding.ErrNameTaken
		}
	}
	if b.ID == "" {
		s.seq++
		b.ID = fmt.Sprintf("ep_%d", s.seq)
	}
	s.byID[b.ID] = *b
	return nil
}

func (s *store) GetByID(_ context.Context, id string) (*epbinding.Binding, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	b, ok := s.byID[id]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &b, nil
}

func (s *store) ByScript(_ context.Context, scriptID string) ([]epbinding.Binding, error) {
	if s.byScriptErr != nil {
		return nil, s.byScriptErr
	}
	out := []epbinding.Binding{}
	for _, b := range s.byID {
		if b.ScriptID == scriptID {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b epbinding.Binding) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (s *store) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	if s.byNameErr != nil {
		return nil, s.byNameErr
	}
	for _, b := range s.byID {
		if b.Name == name {
			return &b, nil
		}
	}
	return nil, epbinding.ErrNotFound
}

func (s *store) List(_ context.Context, f epbinding.Filter) ([]epbinding.Binding, int, error) {
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	out := []epbinding.Binding{}
	for _, b := range s.byID {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, b.ScriptID) {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b epbinding.Binding) int { return strings.Compare(a.Name, b.Name) })
	total := len(out)
	if f.Limit > 0 && f.Limit < total {
		out = out[:f.Limit]
	}
	return out, total, nil
}

func (s *store) Enable(_ context.Context, id string, enabled bool, _ string) error {
	if s.enableErr != "" {
		return errors.New(s.enableErr)
	}
	b, ok := s.byID[id]
	if !ok {
		return epbinding.ErrNotFound
	}
	b.Enabled = enabled
	s.byID[id] = b
	return nil
}

func (s *store) Delete(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.byID[id]; !ok {
		return epbinding.ErrNotFound
	}
	delete(s.byID, id)
	return nil
}

func (*store) RecordFailure(context.Context, epbinding.Failure) error { return nil }

func (s *store) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	if s.failuresErr != nil {
		return nil, s.failuresErr
	}
	return []epbinding.Failure{{Status: 504}}, nil
}

func (*store) SweepFailures(context.Context, time.Time) (int64, error) { return 0, nil }
func (*store) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{}, nil
}

// handlerID is the handler a result names, failing the test when it names
// none.
func handlerID(t *testing.T, out map[string]any) string {
	t.Helper()
	id, ok := out["handler"].(string)
	require.True(t, ok, "the result names its handler: %v", out)
	return id
}

// only is the one binding a script holds; the test fails unless it holds
// exactly one.
func (s *store) only(t *testing.T, scriptID string) epbinding.Binding {
	t.Helper()
	held, err := s.ByScript(context.Background(), scriptID)
	require.NoError(t, err)
	require.Len(t, held, 1)
	return held[0]
}

var lookup = &script.Script{ID: "s1", Name: "lookup", Source: "def handle(req):\n    return {}\n"}

func handler(s *store) *Handler {
	return &Handler{
		Store: s, BaseURL: "https://data.example.com",
		CheckUsable: func(_ context.Context, name string) error {
			if name != "default" {
				return errors.New("no bot check is named " + name)
			}
			return nil
		},
	}
}

func setReq(in Input) Request {
	return Request{Input: in, Script: lookup, Admin: true, Actor: "admin@example.com"}
}

func TestSet_StoresTheBindingAndReportsItsAddress(t *testing.T) {
	s := newStore()
	out, err := handler(s).Set(context.Background(), setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated, RateLimit: 50, CallerRateLimit: 5}))
	require.NoError(t, err)
	b := s.only(t, "s1")
	assert.Equal(t, "https://data.example.com/endpoints/orders", out["url"])
	assert.Equal(t, "lookup now answers GET at https://data.example.com/endpoints/orders.", out["message"])
	assert.Equal(t, b.ID, out["handler"], "the result names the handler, by the id handler_get reads")
	assert.Equal(t, "endpoint", out["kind"])
	assert.Equal(t, 50, b.RateLimit)

	// A script holding one binding changes it when the call names none.
	out, err = handler(s).Set(context.Background(), setReq(Input{
		AuthMode: epbinding.AuthBotCheck, BotCheck: "default", Methods: []string{"GET", "POST"}, CheckedMethods: []string{"POST"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "default", out["bot_check"])
	assert.Equal(t, []string{"POST"}, out["checked_methods"])
	assert.Equal(t, []string{}, out["callers"])
	assert.Equal(t, b.ID, s.only(t, "s1").ID, "the one binding was changed, not joined by another")
	held, err := s.ByScript(context.Background(), "s2")
	require.NoError(t, err)
	assert.Empty(t, held)
}

// TestSet_ScriptHoldsSeveralEndpoints is the several-endpoints model
// (#2441): the binding a set changes is the one its id names, else the one
// with the name sent, else the script's only one; a new name adds one, up to
// MaxBindings, and a call that names none on a script holding several is
// refused naming them.
func TestSet_ScriptHoldsSeveralEndpoints(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	h := handler(s)
	in := func(name string) Input { return Input{Endpoint: name, AuthMode: epbinding.AuthAuthenticated} }

	first, err := h.Set(ctx, setReq(in("orders")))
	require.NoError(t, err)
	second, err := h.Set(ctx, setReq(in("archive")))
	require.NoError(t, err)
	require.NotEqual(t, first["handler"], second["handler"], "a new name adds a handler")
	held, err := s.ByScript(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, held, 2)

	t.Run("by name", func(t *testing.T) {
		req := setReq(in("orders"))
		req.Input.RateLimit, req.Input.CallerRateLimit = 700, 7
		out, err := h.Set(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, first["handler"], out["handler"])
		assert.Equal(t, 700, s.byID[handlerID(t, first)].RateLimit)
	})

	t.Run("by id, renaming it", func(t *testing.T) {
		req := setReq(in("orders-v2"))
		req.Binding = handlerID(t, first)
		out, err := h.Set(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, first["handler"], out["handler"])
		assert.Equal(t, "orders-v2", s.byID[handlerID(t, first)].Name)
		assert.Len(t, s.byID, 2, "a rename adds nothing")
	})

	t.Run("several and none named", func(t *testing.T) {
		_, err := h.Set(ctx, setReq(Input{AuthMode: epbinding.AuthAuthenticated}))
		require.EqualError(t, err, "script lookup has 2 endpoint handlers (archive, orders-v2): "+
			"name the one to change with endpoint, or handler with its id")
	})

	t.Run("an id the script does not hold", func(t *testing.T) {
		other := setReq(in("elsewhere"))
		other.Script = &script.Script{ID: "s2", Name: "other", Source: lookup.Source}
		theirs, err := h.Set(ctx, other)
		require.NoError(t, err)

		for _, id := range []string{"nope", handlerID(t, theirs)} {
			req := setReq(in("stolen"))
			req.Binding = id
			_, err := h.Set(ctx, req)
			require.ErrorIs(t, err, epbinding.ErrNotFound, id)
			assert.Contains(t, err.Error(), "script lookup has no endpoint handler "+id)
		}
		assert.Equal(t, "elsewhere", s.byID[handlerID(t, theirs)].Name, "another script's binding is untouched")
	})

	t.Run("at the bound", func(t *testing.T) {
		req := setReq(in("third"))
		req.MaxBindings = 2
		_, err := h.Set(ctx, req)
		require.ErrorIs(t, err, epbinding.ErrNotAllowed)
		assert.Contains(t, err.Error(), "script lookup already has 2 endpoint handlers, the most a script may hold")

		held := setReq(in("archive"))
		held.MaxBindings = 2
		_, err = h.Set(ctx, held)
		require.NoError(t, err, "changing a handler the script holds is not adding one")
	})
}

// TestSet_ANameTakenNamesItsHolder is setError (#2441): a name another
// binding holds is refused naming the handler holding it and its script.
func TestSet_ANameTakenNamesItsHolder(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	s.byID["ep_x"] = epbinding.Binding{ID: "ep_x", ScriptID: "s7", Name: "orders"}
	h := handler(s)
	in := Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated}

	_, err := h.Set(ctx, setReq(in))
	require.EqualError(t, err, `endpoint name "orders" is taken by endpoint handler ep_x of script s7; choose another`)
	require.NotErrorIs(t, err, ErrStore)

	h.ScriptName = func(_ context.Context, id string) string {
		if id == "s7" {
			return "orders-sync"
		}
		return ""
	}
	_, err = h.Set(ctx, setReq(in))
	require.EqualError(t, err, `endpoint name "orders" is taken by endpoint handler ep_x of script orders-sync; choose another`)

	s.byNameErr = errors.New("boom")
	_, err = h.Set(ctx, setReq(in))
	require.EqualError(t, err, `endpoint name "orders" is taken by another endpoint handler; choose another`)
}

func TestMayWrite_RefusesANonAdministratorsWrite(t *testing.T) {
	ctx := context.Background()
	h := handler(newStore())
	b := &epbinding.Binding{ID: "ep_1", ScriptID: "s1", Name: "orders"}
	_, err := h.Set(ctx, Request{Script: lookup})
	require.ErrorIs(t, err, epbinding.ErrNotAllowed, "set")
	_, err = h.Enable(ctx, Request{Script: lookup}, b, true)
	require.ErrorIs(t, err, epbinding.ErrNotAllowed, "enable")
	_, err = h.Enable(ctx, Request{Script: lookup}, b, false)
	require.ErrorIs(t, err, epbinding.ErrNotAllowed, "disable")
	require.ErrorIs(t, h.Delete(ctx, Request{Script: lookup}, b), epbinding.ErrNotAllowed, "delete")

	require.NoError(t, MayWrite(Request{Admin: true}))
	require.ErrorIs(t, MayWrite(Request{}), epbinding.ErrNotAllowed, "a request naming no script")
}

func TestSet_Refusals(t *testing.T) {
	ctx := context.Background()
	in := Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated}

	_, err := handler(newStore()).Set(ctx, Request{
		Input: in, Admin: true,
		Script: &script.Script{ID: "s2", Name: "daily", Source: "def main():\n    pass\n"},
	})
	require.ErrorContains(t, err, "defines no handle(req)")

	s := newStore()
	s.byScriptErr = errors.New("boom")
	_, err = handler(s).Set(ctx, setReq(in))
	require.ErrorContains(t, err, "failed to read the script's endpoint handlers")
	require.ErrorIs(t, err, ErrStore)

	_, err = handler(newStore()).Set(ctx, setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthBotCheck, BotCheck: "gone"}))
	require.ErrorContains(t, err, "no bot check is named gone")

	_, err = handler(newStore()).Set(ctx, setReq(Input{Endpoint: "orders", AuthMode: "signed_in"}))
	require.ErrorContains(t, err, `auth mode "signed_in" is not one of`)

	s = newStore()
	s.setErr = fmt.Errorf("bot check %q: %w", "default", epbinding.ErrNoSuchCheck)
	_, err = handler(s).Set(ctx, setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthBotCheck, BotCheck: "default"}))
	require.ErrorIs(t, err, epbinding.ErrNoSuchCheck)
	require.NotErrorIs(t, err, ErrStore, "the caller named a check that does not exist; the store did not fail")

	_, err = handler(newStore()).Set(ctx, setReq(Input{Endpoint: "Bad Name", AuthMode: epbinding.AuthAuthenticated}))
	require.ErrorContains(t, err, "lowercase")

	s = newStore()
	s.setErr = epbinding.ErrNotFound
	_, err = handler(s).Set(ctx, setReq(in))
	require.ErrorIs(t, err, epbinding.ErrNotFound, "a binding gone between the read and the write is named as gone")
	require.NotErrorIs(t, err, ErrStore)

	s.setErr = errors.New("boom")
	_, err = handler(s).Set(ctx, setReq(in))
	require.ErrorContains(t, err, "failed to set the endpoint handler")
	require.ErrorIs(t, err, ErrStore)
}

func TestEnableAndDisable(t *testing.T) {
	s := newStore()
	h := handler(s)
	ctx := context.Background()
	admin := Request{Script: lookup, Admin: true}
	_, err := h.Enable(ctx, admin, &epbinding.Binding{ID: "ep_gone"}, false)
	require.EqualError(t, err, "script lookup has no endpoint handler ep_gone")

	_, err = h.Set(ctx, setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated}))
	require.NoError(t, err)
	_, err = h.Set(ctx, setReq(Input{Endpoint: "archive", AuthMode: epbinding.AuthAuthenticated}))
	require.NoError(t, err)
	orders, err := s.GetByName(ctx, "orders")
	require.NoError(t, err)

	out, err := h.Enable(ctx, admin, orders, false)
	require.NoError(t, err)
	assert.Equal(t, false, out["enabled"])
	assert.Equal(t, orders.ID, out["handler"])
	assert.False(t, s.byID[orders.ID].Enabled)
	archive, err := s.GetByName(ctx, "archive")
	require.NoError(t, err)
	assert.True(t, archive.Enabled, "turning one handler off leaves the script's others on")

	out, err = h.Enable(ctx, admin, orders, true)
	require.NoError(t, err)
	assert.Equal(t, true, out["enabled"])

	s.enableErr = "boom"
	_, err = h.Enable(ctx, admin, orders, true)
	require.ErrorContains(t, err, "failed to change the endpoint handler")
	require.ErrorIs(t, err, ErrStore)
}

// TestDelete removes one endpoint handler, leaving the script's others; a
// handler already gone is not an error, and a store fault is the platform's.
func TestDelete(t *testing.T) {
	s := newStore()
	h := handler(s)
	ctx := context.Background()
	admin := Request{Script: lookup, Admin: true}
	for _, name := range []string{"orders", "archive"} {
		_, err := h.Set(ctx, setReq(Input{Endpoint: name, AuthMode: epbinding.AuthAuthenticated}))
		require.NoError(t, err)
	}
	orders, err := s.GetByName(ctx, "orders")
	require.NoError(t, err)

	require.NoError(t, h.Delete(ctx, admin, orders))
	assert.Equal(t, "archive", s.only(t, "s1").Name)
	require.NoError(t, h.Delete(ctx, admin, orders), "deleting a handler already gone is done")

	s.deleteErr = errors.New("boom")
	err = h.Delete(ctx, admin, orders)
	require.ErrorContains(t, err, "failed to delete the endpoint handler")
	require.ErrorIs(t, err, ErrStore)
}

func TestDescribeAndList(t *testing.T) {
	s := newStore()
	h := handler(s)
	ctx := context.Background()

	_, err := h.Set(ctx, setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated}))
	require.NoError(t, err)
	b := s.only(t, "s1")
	out, err := h.Describe(ctx, lookup, &b)
	require.NoError(t, err)
	assert.Equal(t, b.ID, out["handler"])
	assert.Equal(t, "orders", out["endpoint"])
	assert.Len(t, out["failures"], 1)
	assert.Equal(t, "This deployment has no pages domain.", out["page_problem"])

	out, err = h.List(ctx, Request{Visible: map[string]*script.Script{"s1": lookup}})
	require.NoError(t, err)
	assert.Equal(t, 1, out["count"])
	items, _ := out["endpoints"].([]map[string]any)
	assert.Equal(t, "lookup", items[0]["name"])

	out, err = h.List(ctx, Request{Visible: map[string]*script.Script{}})
	require.NoError(t, err)
	assert.Equal(t, 0, out["count"], "a caller who sees no scripts sees no endpoints")

	h.ScriptName = func(context.Context, string) string { return "by-id" }
	out, err = h.List(ctx, Request{Admin: true})
	require.NoError(t, err)
	items, _ = out["endpoints"].([]map[string]any)
	assert.Equal(t, "by-id", items[0]["name"], "an administrator's listing names a script it was not handed")
	assert.Equal(t, 1, out["total"])
	assert.Nil(t, out["note"], "a listing that holds every endpoint says nothing more")

	// A listing cut at its limit says how many exist (#2251).
	s.byID["e2"] = epbinding.Binding{ID: "e2", ScriptID: "other", Name: "zz-other"}
	out, err = h.List(ctx, Request{Admin: true, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, out["count"])
	assert.Equal(t, 2, out["total"])
	assert.Contains(t, out["note"], "1 of 2 endpoints listed; Admin > Automations > Handlers pages through every one")
	delete(s.byID, "e2")

	s.failuresErr = errors.New("boom")
	_, err = h.Describe(ctx, lookup, &b)
	require.ErrorContains(t, err, "failed requests")
	require.ErrorIs(t, err, ErrStore)
	s.listErr = errors.New("boom")
	_, err = h.List(ctx, Request{Admin: true})
	require.EqualError(t, err, "failed to list endpoints: boom")
	require.ErrorIs(t, err, ErrStore, "a store's failure is the platform's")
	require.ErrorIs(t, err, s.listErr)
}

// TestChoices is what adding an endpoint handler offers: the script's name
// and where its page would answer; a script that is not a form says nothing
// of forms.
func TestChoices(t *testing.T) {
	h := handler(newStore())
	h.PageAllowed = func(context.Context, string) error { return errors.New("this script is a part of no application") }
	h.PageAddress = func(context.Context, string) string { return "https://shop.pages.example.net" }
	out, err := h.Choices(context.Background(), lookup)
	require.NoError(t, err)
	assert.Equal(t, "lookup", out["name"])
	assert.Equal(t, "https://shop.pages.example.net", out["page_address"])
	assert.Equal(t, "this script is a part of no application", out["page_problem"])
	assert.NotContains(t, out, "is_form")
}

func TestPropertiesCoverTheInput(t *testing.T) {
	props := Properties()
	for _, name := range []string{
		"endpoint", "methods", "path", "auth_mode", "callers", "bot_check", "checked_methods", "max_memory_bytes",
		"max_body_bytes", "allowed_origins", "rate_limit", "caller_rate_limit", "connection", "decrypt_personas",
		"preview_version", "response_mode", "page_cdns",
		"page_frame_origins", "page_connect_origins", "page_form_origins", "ip_rate_limit",
	} {
		assert.Contains(t, props, name)
	}
	assert.Len(t, props, 21)
}
