package integration

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memStore is Bindings over maps: what a pass and the routes read and write.
type memStore struct {
	bindings map[string]*Binding
	items    map[string]map[string]StoredItem
	runs     map[string]RunOutcome
	started  []NewRun
	refusals []string
	locked   bool
	err      error
}

func newMem() *memStore {
	return &memStore{bindings: map[string]*Binding{}, items: map[string]map[string]StoredItem{}, runs: map[string]RunOutcome{}}
}

func (m *memStore) Binding(_ context.Context, op string) (*Binding, error) {
	if m.err != nil {
		return nil, m.err
	}
	b := m.bindings[op]
	if b == nil {
		return nil, nil //nolint:nilnil // no binding, as the store answers
	}
	c := *b
	return &c, nil
}

func (m *memStore) Bind(_ context.Context, op, scriptID string, interval int, by string) (*Binding, error) {
	b := m.bindings[op]
	if b == nil {
		b = &Binding{ID: "b-" + op, Operation: op, CreatedBy: by}
		m.bindings[op] = b
	}
	b.ScriptID, b.IntervalSeconds, b.RetryRequested, b.UpdatedBy = scriptID, interval, true, by
	c := *b
	return &c, nil
}

func (m *memStore) Clear(_ context.Context, op string) (bool, error) {
	_, had := m.bindings[op]
	delete(m.bindings, op)
	delete(m.items, op)
	return had, nil
}

func (m *memStore) RequestRetry(_ context.Context, op string) error {
	b := m.bindings[op]
	if b == nil {
		return ErrNotBound
	}
	b.RetryRequested = true
	return nil
}

func (m *memStore) Items(_ context.Context, op string) ([]StoredItem, error) {
	out := make([]StoredItem, 0, len(m.items[op]))
	for _, it := range m.items[op] {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *memStore) ItemsByKey(_ context.Context, op string, keys []string) (map[string]StoredItem, error) {
	out := map[string]StoredItem{}
	for _, k := range keys {
		if it, ok := m.items[op][k]; ok && !it.Removing {
			out[k] = it
		}
	}
	return out, nil
}

func (m *memStore) StartRun(_ context.Context, op string, r NewRun) error {
	if m.items[op] == nil {
		m.items[op] = map[string]StoredItem{}
	}
	for _, it := range r.Added {
		m.items[op][it.Key] = StoredItem{Key: it.Key, Entry: it.Entry, Status: StatusPending}
	}
	for _, k := range r.Removed {
		it := m.items[op][k]
		it.Removing = true
		m.items[op][k] = it
	}
	m.started = append(m.started, r)
	m.runs[r.ID] = RunOutcome{Call: r.Call}
	b := m.bindings[op]
	now := time.Now()
	b.CurrentRunID, b.RetryRequested, b.LastStartedAt, b.LastReason = r.ID, false, &now, r.Reason
	return nil
}

func (m *memStore) RecordRefusal(_ context.Context, op, reason string) error {
	m.refusals = append(m.refusals, reason)
	b := m.bindings[op]
	now := time.Now()
	b.RetryRequested, b.LastStartedAt, b.LastStatus, b.LastError = false, &now, "failed", reason
	return nil
}

func (m *memStore) RunOutcome(_ context.Context, id string) (RunOutcome, error) {
	return m.runs[id], nil
}

func (m *memStore) Apply(_ context.Context, op string, a Applied) error {
	for k, r := range a.Results {
		it := m.items[op][k]
		if it.Removing {
			continue
		}
		it.Status, it.Message, it.FailedRun = r.Status, r.Message, ""
		m.items[op][k] = it
	}
	for _, k := range a.Failed {
		it, ok := m.items[op][k]
		if !ok || it.Removing {
			continue
		}
		it.Status, it.Message, it.FailedRun = StatusFailed, a.FailWith, a.RunID
		m.items[op][k] = it
	}
	for _, k := range a.Removed {
		if m.items[op][k].Removing {
			delete(m.items[op], k)
		}
	}
	b := m.bindings[op]
	b.CurrentRunID, b.LastRunID, b.LastStatus, b.LastError = "", a.RunID, a.Status, a.Error
	return nil
}

func (m *memStore) Lock(context.Context) (release func(), ok bool, err error) {
	if m.locked {
		return nil, false, nil
	}
	return func() {}, true, nil
}

// finishRun ends the store's run in flight as a worker would.
func (m *memStore) finishRun(t *testing.T, status string, result any, runErr string) {
	t.Helper()
	op := OpSiteRouting
	id := m.bindings[op].CurrentRunID
	require.NotEmpty(t, id, "a run is in flight")
	out := m.runs[id]
	out.Finished, out.Status, out.Error = true, status, runErr
	if result != nil {
		out.Result, _ = json.Marshal(result)
	}
	m.runs[id] = out
}

type fakeScripts struct{ scripts map[string]*script.Script }

func (f fakeScripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	return f.scripts[id], nil
}

func (f fakeScripts) GetVersion(_ context.Context, id string, version int) (*script.Version, error) {
	sc := f.scripts[id]
	if sc == nil {
		return nil, nil //nolint:nilnil // no script, as the store answers
	}
	return &script.Version{ID: "v-" + id, Version: version, Source: sc.Source}, nil
}

func (f fakeScripts) List(_ context.Context, _ script.ListFilter) ([]script.Script, error) {
	out := make([]script.Script, 0, len(f.scripts))
	for _, sc := range f.scripts {
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type fakeApprovals struct {
	runAs    map[string]approval.RunAs
	approved []string
}

func (f *fakeApprovals) HandlerRunAs(_ context.Context, id string) (approval.RunAs, bool) {
	r, ok := f.runAs[id]
	return r, ok
}

func (f *fakeApprovals) ApproveForHandlers(_ context.Context, a approval.Actor, sc *script.Script, _ int) error {
	if !a.Admin {
		return errors.New("not admin")
	}
	f.approved = append(f.approved, sc.ID)
	f.runAs[sc.ID] = approval.RunAs{Version: sc.Version, Approver: a.Email, Roles: a.Roles}
	return nil
}

type fakeSource struct {
	items []Item
	err   error
}

func (f *fakeSource) Items(context.Context) ([]Item, error) { return f.items, f.err }

func host(h string) Item { return Item{Key: h, Entry: map[string]any{"hostname": h, "kind": "domain"}} }

const routeSource = "INTEGRATION = \"site_routing\"\n\ndef route(req):\n    return {}\n"

// world is the service under test and the fakes it reads and writes.
type world struct {
	svc *Service
	mem *memStore
	src *fakeSource
	apr *fakeApprovals
}

func fixture() world {
	mem := newMem()
	src := &fakeSource{items: []Item{host("a.example.com"), host("b.example.com")}}
	apr := &fakeApprovals{runAs: map[string]approval.RunAs{}}
	svc := &Service{
		Store: mem, Approvals: apr, Sources: map[string]Source{OpSiteRouting: src},
		Scripts: fakeScripts{scripts: map[string]*script.Script{
			"s1":  {ID: "s1", Name: "ingress-routes", Version: 7, Source: routeSource, OwnerEmail: "ops@example.com"},
			"lib": {ID: "lib", Name: "helpers", Version: 1, Source: routeSource, Library: true},
			"bad": {ID: "bad", Name: "report", Version: 2, Source: "def main():\n    pass\n"},
		}},
	}
	return world{svc: svc, mem: mem, src: src, apr: apr}
}

var admin = Actor{Email: "ops@example.com", Roles: []string{"admin"}, Admin: true}

// TestBind pins #2659's criterion 2 on the service: only an administrator
// binds; a script that does not define route(req) is refused naming the
// contract; a library and an unapproved version are refused; approve
// approves the latest version as the administrator.
func TestBind(t *testing.T) {
	w := fixture()
	svc, mem, _, apr := w.svc, w.mem, w.src, w.apr
	ctx := context.Background()
	_, err := svc.Bind(ctx, Actor{Email: "x@example.com"}, BindRequest{Operation: OpSiteRouting, ScriptID: "s1"})
	require.ErrorIs(t, err, ErrNotAllowed)
	_, err = svc.Bind(ctx, admin, BindRequest{Operation: "nope", ScriptID: "s1"})
	require.ErrorIs(t, err, ErrUnknown)
	_, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "missing"})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "lib", Approve: true})
	require.ErrorContains(t, err, "library")
	_, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1"})
	require.ErrorContains(t, err, "approved for handlers")
	_, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "bad", Approve: true})
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorContains(t, err, "route(req)")
	_, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1", Approve: true, IntervalSeconds: 5})
	require.ErrorContains(t, err, "interval_seconds")

	b, err := svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1", Approve: true})
	require.NoError(t, err)
	assert.Equal(t, DefaultInterval, b.IntervalSeconds)
	assert.True(t, b.RetryRequested)
	assert.Equal(t, []string{"s1"}, apr.approved)

	b, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1", IntervalSeconds: 600})
	require.NoError(t, err, "an approved version is run without approving again")
	assert.Equal(t, 600, b.IntervalSeconds)
	b, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1"})
	require.NoError(t, err)
	assert.Equal(t, 600, b.IntervalSeconds, "a bind naming no interval keeps the one set")

	apr.runAs["s1"] = approval.RunAs{Version: 6, Approver: "ops@example.com"}
	_, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1"})
	require.NoError(t, err, "the approved older version is read and checked")

	require.ErrorIs(t, svc.Clear(ctx, Actor{}, OpSiteRouting), ErrNotAllowed)
	require.NoError(t, svc.Clear(ctx, admin, OpSiteRouting))
	require.ErrorIs(t, svc.Clear(ctx, admin, OpSiteRouting), ErrNotBound)
	assert.Empty(t, mem.bindings)
	require.ErrorIs(t, svc.Retry(ctx, "nope"), ErrUnknown)
	require.ErrorIs(t, svc.Retry(ctx, OpSiteRouting), ErrNotBound)
}

// TestPass pins #2659's criteria 3 to 9 on the service: a bind runs the
// script with every host; a change runs it with the difference; a change
// while a run is in flight folds into the next run; a failure marks every
// host failed with the generic sentence and keeps the error for the
// administrator; a timer pass with nothing changed has empty lists; a
// removal's status is gone once its run ends.
func TestPass(t *testing.T) {
	w := fixture()
	svc, mem, src, _ := w.svc, w.mem, w.src, w.apr
	ctx := context.Background()
	clock := time.Now()
	svc.now = func() time.Time { return clock }

	require.NoError(t, svc.Pass(ctx), "nothing bound, nothing runs")
	assert.Empty(t, mem.started)

	_, err := svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1", Approve: true})
	require.NoError(t, err)
	require.NoError(t, svc.Pass(ctx))
	require.Len(t, mem.started, 1)
	first := mem.started[0]
	_, req, err := FromRunCall(first.Call)
	require.NoError(t, err)
	assert.Equal(t, ReasonChange, req["reason"])
	assert.Len(t, req["added"], 2)
	assert.Len(t, req["hosts"], 2)
	assert.Equal(t, "ops@example.com", first.Approver)

	// Two hosts added while the run is in flight: no second run.
	src.items = append(src.items, host("c.example.com"), host("d.example.com"))
	require.NoError(t, svc.Pass(ctx))
	assert.Len(t, mem.started, 1, "no two runs overlap")

	mem.finishRun(t, script.RunStatusSucceeded, map[string]any{
		"a.example.com": map[string]any{"status": "ready", "message": "routed"},
		"b.example.com": map[string]any{"status": "failed", "message": "no DNS"},
	}, "")
	require.NoError(t, svc.Pass(ctx))
	require.Len(t, mem.started, 2, "the folded change is the next run")
	_, req, _ = FromRunCall(mem.started[1].Call)
	added, _ := RequestKeys(siteSpec(), map[string]any{"hosts": req["added"]})
	assert.Equal(t, []string{"c.example.com", "d.example.com"}, added, "one run, both hosts")
	assert.Equal(t, StatusReady, mem.items[OpSiteRouting]["a.example.com"].Status)
	assert.Equal(t, "no DNS", mem.items[OpSiteRouting]["b.example.com"].Message)

	// The script raises: every host in that call is failed, generic for owners.
	mem.finishRun(t, script.RunStatusFailed, nil, "ingress API answered 503 SECRET-MARKER")
	src.items = src.items[1:] // a.example.com removed
	require.NoError(t, svc.Pass(ctx))
	for _, k := range []string{"b.example.com", "c.example.com", "d.example.com"} {
		it := mem.items[OpSiteRouting][k]
		assert.Equal(t, StatusFailed, it.Status, k)
		assert.Equal(t, "The site routing script failed", it.Message, k)
		assert.NotContains(t, it.Message, "SECRET-MARKER")
	}
	assert.Equal(t, "ingress API answered 503 SECRET-MARKER", mem.bindings[OpSiteRouting].LastError)
	require.Len(t, mem.started, 3)
	_, req, _ = FromRunCall(mem.started[2].Call)
	assert.Len(t, req["removed"], 1, "the removal reaches the script")

	owner, err := svc.Statuses(ctx, OpSiteRouting, []string{"c.example.com", "z.example.com"}, false)
	require.NoError(t, err)
	assert.True(t, owner["c.example.com"].ScriptFailed)
	assert.Empty(t, owner["c.example.com"].RunError, "an owner never reads the run error")
	assert.Equal(t, StatusPending, owner["z.example.com"].Status, "a host the script has not answered for is pending")
	// The admin's run error is the last run's when that run is the one that failed the item.
	mem.finishRun(t, script.RunStatusSucceeded, map[string]any{}, "")
	require.NoError(t, svc.Pass(ctx))
	_, gone := mem.items[OpSiteRouting]["a.example.com"]
	assert.False(t, gone, "a removed host's status is gone once its run ends")

	// A timer pass with nothing changed.
	clock = clock.Add(time.Duration(DefaultInterval+1) * time.Second)
	require.NoError(t, svc.Pass(ctx))
	require.Len(t, mem.started, 4)
	_, req, _ = FromRunCall(mem.started[3].Call)
	raw, _ := json.Marshal(req)
	assert.JSONEq(t, `{"reason":"timer","added":[],"removed":[],"hosts":[`+
		`{"hostname":"b.example.com","kind":"domain"},{"hostname":"c.example.com","kind":"domain"},{"hostname":"d.example.com","kind":"domain"}]}`, string(raw))

	mem.finishRun(t, script.RunStatusFailed, nil, "boom")
	require.NoError(t, svc.Pass(ctx))
	adminView, err := svc.Statuses(ctx, OpSiteRouting, []string{"b.example.com"}, true)
	require.NoError(t, err)
	assert.Equal(t, "boom", adminView["b.example.com"].RunError, "an administrator reads the run's error")
	assert.NotEmpty(t, adminView["b.example.com"].RunID)

	// Retry runs at once with every host.
	require.NoError(t, svc.Retry(ctx, OpSiteRouting))
	require.NoError(t, svc.Pass(ctx))
	assert.Len(t, mem.started, 5, "Retry runs the script at once")

	// Clearing stops the calls and hides the statuses.
	mem.finishRun(t, script.RunStatusSucceeded, map[string]any{}, "")
	require.NoError(t, svc.Clear(ctx, admin, OpSiteRouting))
	none, err := svc.Statuses(ctx, OpSiteRouting, []string{"b.example.com"}, true)
	require.NoError(t, err)
	assert.Nil(t, none)
	require.NoError(t, svc.Pass(ctx))
	assert.Len(t, mem.started, 5)
}

func siteSpec() Spec { sp, _ := SpecOf(OpSiteRouting); return sp }

// TestPass_Refusals: no approved version, an unreadable version and one that
// stopped defining route(req) are recorded, not run; a malformed stored
// result fails every host; another replica's lock skips the pass.
func TestPass_Refusals(t *testing.T) {
	w := fixture()
	svc, mem, src, apr := w.svc, w.mem, w.src, w.apr
	ctx := context.Background()
	mem.bindings[OpSiteRouting] = &Binding{Operation: OpSiteRouting, ScriptID: "s1", IntervalSeconds: DefaultInterval, RetryRequested: true}
	require.NoError(t, svc.Pass(ctx))
	assert.Contains(t, mem.refusals[0], "approved for handlers")

	apr.runAs["s1"] = approval.RunAs{Version: 7}
	svc.Scripts = fakeScripts{scripts: map[string]*script.Script{"s1": {ID: "s1", Version: 7, Source: "def main():\n    pass\n"}}}
	mem.bindings[OpSiteRouting].RetryRequested = true
	require.NoError(t, svc.Pass(ctx))
	assert.Contains(t, mem.refusals[1], "route(req)")

	svc.Scripts = fakeScripts{scripts: map[string]*script.Script{}}
	mem.bindings[OpSiteRouting].RetryRequested = true
	require.NoError(t, svc.Pass(ctx))
	assert.Contains(t, mem.refusals[2], "could not be read")

	svc.Scripts = fakeScripts{scripts: map[string]*script.Script{"s1": {ID: "s1", Version: 7, Source: routeSource}}}
	mem.bindings[OpSiteRouting].RetryRequested = true
	require.NoError(t, svc.Pass(ctx))
	id := mem.bindings[OpSiteRouting].CurrentRunID
	out := mem.runs[id]
	out.Finished, out.Status, out.Result = true, script.RunStatusSucceeded, []byte(`"not a dict"`)
	mem.runs[id] = out
	require.NoError(t, svc.Pass(ctx))
	assert.Equal(t, StatusFailed, mem.items[OpSiteRouting]["a.example.com"].Status)
	assert.Contains(t, mem.bindings[OpSiteRouting].LastError, "could not be read")

	src.err = errors.New("db down")
	mem.bindings[OpSiteRouting].CurrentRunID = ""
	require.ErrorContains(t, svc.Pass(ctx), "db down")

	mem.locked = true
	require.NoError(t, svc.Pass(ctx), "another replica holds the pass")
	mem.locked = false
	mem.err = errors.New("store down")
	require.Error(t, svc.Pass(ctx))
}

// TestOverviewAndCandidates: the settings page's read, with pending counted
// for hosts the script has not been handed, and the picker's scripts.
func TestOverviewAndCandidates(t *testing.T) {
	w := fixture()
	svc, mem, _, apr := w.svc, w.mem, w.src, w.apr
	ctx := context.Background()
	out, err := svc.Overview(ctx)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Nil(t, out[0].Binding)
	assert.Equal(t, 2, out[0].Total)

	_, err = svc.Bind(ctx, admin, BindRequest{Operation: OpSiteRouting, ScriptID: "s1", Approve: true})
	require.NoError(t, err)
	mem.items[OpSiteRouting] = map[string]StoredItem{
		"a.example.com": {Key: "a.example.com", Status: StatusReady},
		"x.example.com": {Key: "x.example.com", Status: StatusFailed, Removing: true},
	}
	out, err = svc.Overview(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ingress-routes", out[0].ScriptName)
	assert.Equal(t, 7, out[0].Running)
	assert.Equal(t, map[string]int{StatusReady: 1, StatusPending: 1}, out[0].Counts)
	assert.Len(t, out[0].Items, 1)

	cands, err := svc.Candidates(ctx, admin, OpSiteRouting, "")
	require.NoError(t, err)
	names := make([]string, 0, len(cands))
	for _, c := range cands {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "ingress-routes")
	assert.NotContains(t, names, "report", "a script without route(req) is not offered")
	assert.True(t, cands[0].RunningServes || cands[len(cands)-1].RunningServes)
	_, err = svc.Candidates(ctx, Actor{}, OpSiteRouting, "")
	require.ErrorIs(t, err, ErrNotAllowed)
	delete(apr.runAs, "s1")

	svc.Scripts = struct{ Scripts }{fakeScripts{}}
	cands, err = svc.Candidates(ctx, admin, OpSiteRouting, "")
	require.NoError(t, err)
	assert.Empty(t, cands, "a store that cannot list offers none")

	var nilSvc *Service
	st, err := nilSvc.Statuses(ctx, OpSiteRouting, nil, false)
	require.NoError(t, err)
	assert.Nil(t, st)
}

// TestRetryFailed: a reader's retry is honored only when a hostname they see
// failed, and not within RetryInterval of the last run's start, so a reader
// can ask again for what failed but cannot keep the script running (#2659).
func TestRetryFailed(t *testing.T) {
	w := fixture()
	svc, mem := w.svc, w.mem
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	require.ErrorIs(t, svc.RetryFailed(ctx, "nope", nil), ErrUnknown)
	require.ErrorIs(t, svc.RetryFailed(ctx, OpSiteRouting, []string{"a.example.com"}), ErrNotBound)

	started := now.Add(-5 * time.Second)
	mem.bindings[OpSiteRouting] = &Binding{Operation: OpSiteRouting, ScriptID: "s1", IntervalSeconds: DefaultInterval, LastStartedAt: &started}
	mem.items[OpSiteRouting] = map[string]StoredItem{
		"a.example.com": {Key: "a.example.com", Status: StatusFailed},
		"b.example.com": {Key: "b.example.com", Status: StatusReady},
	}
	require.ErrorIs(t, svc.RetryFailed(ctx, OpSiteRouting, []string{"b.example.com"}), ErrNothingFailed)
	err := svc.RetryFailed(ctx, OpSiteRouting, []string{"a.example.com", "b.example.com"})
	require.ErrorIs(t, err, ErrTooSoon)
	require.ErrorContains(t, err, "ask again in 10s")
	assert.False(t, mem.bindings[OpSiteRouting].RetryRequested)

	now = now.Add(RetryInterval)
	require.NoError(t, svc.RetryFailed(ctx, OpSiteRouting, []string{"a.example.com", "b.example.com"}))
	assert.True(t, mem.bindings[OpSiteRouting].RetryRequested)
}
