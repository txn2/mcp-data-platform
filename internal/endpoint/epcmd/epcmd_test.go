package epcmd

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// store is an epbinding.Store over one map keyed by script, with an error a
// test sets per method.
type store struct {
	byScript             map[string]epbinding.Binding
	getErr, setErr       error
	listErr, failuresErr error
	enableErr            string
}

func newStore() *store { return &store{byScript: map[string]epbinding.Binding{}} }

func (s *store) Set(_ context.Context, b *epbinding.Binding) error {
	if s.setErr != nil {
		return s.setErr
	}
	b.ID = "ep_" + b.ScriptID
	s.byScript[b.ScriptID] = *b
	return nil
}

func (s *store) Get(_ context.Context, id string) (*epbinding.Binding, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	b, ok := s.byScript[id]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &b, nil
}

func (*store) GetByName(context.Context, string) (*epbinding.Binding, error) {
	return nil, epbinding.ErrNotFound
}

func (s *store) List(_ context.Context, f epbinding.Filter) ([]epbinding.Binding, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := []epbinding.Binding{}
	for id, b := range s.byScript {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, id) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *store) SetEnabled(_ context.Context, id string, enabled bool, _ string) error {
	if s.enableErr != "" {
		return errors.New(s.enableErr)
	}
	b, ok := s.byScript[id]
	if !ok {
		return epbinding.ErrNotFound
	}
	b.Enabled = enabled
	s.byScript[id] = b
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

var lookup = &script.Script{ID: "s1", Name: "lookup", Source: "def handle(req):\n    return {}\n"}

func handler(s *store) *Handler {
	return &Handler{
		Store: s, BaseURL: "https://data.example.com",
		PublicAllowed: func(context.Context) (bool, error) { return true, nil },
	}
}

func setReq(in Input) Request {
	return Request{Command: CmdSet, Input: in, Script: lookup, Admin: true, Actor: "admin@example.com"}
}

func TestRun_SetStoresTheBindingAndReportsItsAddress(t *testing.T) {
	s := newStore()
	out, err := handler(s).Run(context.Background(), setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthSignedIn, RateLimit: 50, CallerRateLimit: 5}))
	require.NoError(t, err)
	assert.Equal(t, "https://data.example.com/endpoints/orders", out["url"])
	assert.Equal(t, "lookup now answers GET at https://data.example.com/endpoints/orders.", out["message"])
	assert.Equal(t, 50, s.byScript["s1"].RateLimit)
	assert.Equal(t, "orders", handler(s).Bound(context.Background(), "s1"))
	assert.Empty(t, handler(s).Bound(context.Background(), "s2"))
}

func TestRun_RefusesANonAdministratorsWrite(t *testing.T) {
	for _, cmd := range []string{CmdSet, CmdEnable, CmdDisable} {
		_, err := handler(newStore()).Run(context.Background(), Request{Command: cmd, Script: lookup})
		require.ErrorIs(t, err, epbinding.ErrNotAllowed, cmd)
	}
	assert.True(t, Writes(CmdSet))
	assert.False(t, Writes(CmdList))
}

func TestRun_SetRefusals(t *testing.T) {
	ctx := context.Background()
	in := Input{Endpoint: "orders", AuthMode: epbinding.AuthSignedIn}

	_, err := handler(newStore()).Run(ctx, Request{
		Command: CmdSet, Input: in, Admin: true,
		Script: &script.Script{ID: "s2", Name: "daily", Source: "def main():\n    pass\n"},
	})
	require.ErrorContains(t, err, "defines no handle(req)")

	s := newStore()
	s.getErr = errors.New("boom")
	_, err = handler(s).Run(ctx, setReq(in))
	require.ErrorContains(t, err, "failed to read the current binding")

	h := handler(newStore())
	h.PublicAllowed = func(context.Context) (bool, error) { return false, errors.New("boom") }
	_, err = h.Run(ctx, setReq(in))
	require.ErrorContains(t, err, "bot check settings")

	_, err = handler(newStore()).Run(ctx, setReq(Input{Endpoint: "Bad Name", AuthMode: epbinding.AuthSignedIn}))
	require.ErrorContains(t, err, "lowercase")

	s = newStore()
	s.setErr = epbinding.ErrNameTaken
	_, err = handler(s).Run(ctx, setReq(in))
	require.ErrorContains(t, err, `"orders" is taken`)
	s.setErr = errors.New("boom")
	_, err = handler(s).Run(ctx, setReq(in))
	require.ErrorContains(t, err, "failed to set the binding")
}

func TestRun_EnableAndDisable(t *testing.T) {
	s := newStore()
	h := handler(s)
	ctx := context.Background()
	_, err := h.Run(ctx, Request{Command: CmdDisable, Script: lookup, Admin: true})
	require.ErrorContains(t, err, "not bound to an endpoint")

	_, err = h.Run(ctx, setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthSignedIn}))
	require.NoError(t, err)
	out, err := h.Run(ctx, Request{Command: CmdDisable, Script: lookup, Admin: true})
	require.NoError(t, err)
	assert.Equal(t, false, out["enabled"])
	assert.False(t, s.byScript["s1"].Enabled)

	s.enableErr = "boom"
	_, err = h.Run(ctx, Request{Command: CmdEnable, Script: lookup, Admin: true})
	require.ErrorContains(t, err, "failed to change the binding")
}

func TestRun_List(t *testing.T) {
	s := newStore()
	h := handler(s)
	ctx := context.Background()
	out, err := h.Run(ctx, Request{Command: CmdList, Script: lookup})
	require.NoError(t, err)
	assert.Equal(t, false, out["bound"])

	_, err = h.Run(ctx, setReq(Input{Endpoint: "orders", AuthMode: epbinding.AuthSignedIn}))
	require.NoError(t, err)
	out, err = h.Run(ctx, Request{Command: CmdList, Script: lookup})
	require.NoError(t, err)
	assert.Equal(t, true, out["bound"])
	assert.Len(t, out["failures"], 1)

	out, err = h.Run(ctx, Request{Command: CmdList, Visible: map[string]*script.Script{"s1": lookup}})
	require.NoError(t, err)
	assert.Equal(t, 1, out["count"])
	items, _ := out["endpoints"].([]map[string]any)
	assert.Equal(t, "lookup", items[0]["name"])

	out, err = h.Run(ctx, Request{Command: CmdList, Visible: map[string]*script.Script{}})
	require.NoError(t, err)
	assert.Equal(t, 0, out["count"], "a caller who sees no scripts sees no endpoints")

	h.ScriptName = func(context.Context, string) string { return "by-id" }
	out, err = h.Run(ctx, Request{Command: CmdList, Admin: true})
	require.NoError(t, err)
	items, _ = out["endpoints"].([]map[string]any)
	assert.Equal(t, "by-id", items[0]["name"], "an administrator's listing names a script it was not handed")

	s.failuresErr = errors.New("boom")
	_, err = h.Run(ctx, Request{Command: CmdList, Script: lookup})
	require.ErrorContains(t, err, "failed requests")
	s.getErr = errors.New("boom")
	_, err = h.Run(ctx, Request{Command: CmdList, Script: lookup})
	require.ErrorContains(t, err, "failed to read the binding")
	s.listErr = errors.New("boom")
	_, err = h.Run(ctx, Request{Command: CmdList, Admin: true})
	require.EqualError(t, err, "failed to list endpoints: boom")
	require.ErrorIs(t, err, ErrStore, "a store's failure is the platform's")
	require.ErrorIs(t, err, s.listErr)

	_, err = h.Run(ctx, Request{Command: "endpoint_nope"})
	require.ErrorContains(t, err, "unknown endpoint command")
}

func TestPropertiesCoverTheInput(t *testing.T) {
	props := Properties()
	for _, name := range []string{
		"endpoint", "methods", "path", "auth_mode", "open_reads", "max_memory_bytes",
		"max_body_bytes", "allowed_origins", "rate_limit", "caller_rate_limit",
	} {
		assert.Contains(t, props, name)
	}
	assert.Len(t, props, 10)
}
