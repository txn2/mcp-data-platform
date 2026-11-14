package scripthandler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

var (
	errDown   = errors.New("db down")
	fakeEpoch = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
)

const (
	endpointSource = "def handle(req):\n    return {\"json\": req}\n"
	mainSource     = "def main():\n    print(1)\n"
	formSource     = `def form_spec(req):
    return {"fields": [{"name": "email", "type": "email"}]}

def form_submit(req, values):
    return None
`
)

// world is every store a Service reads, in memory, with the script_handlers
// view derived from the three kind tables the way the migration's view
// derives it.
type world struct {
	scripts   map[string]*script.Script
	bindings  *bindings
	schedules *schedules
	webhooks  *webhooks
	rule      *rule
	sources   *sourceList
	calls     *calls
	versions  *versions
	seq       int
	// viewErr fails every read of the view; scriptErr every script read.
	viewErr, scriptErr error
}

func newWorld() *world {
	w := &world{scripts: map[string]*script.Script{}}
	w.bindings = &bindings{w: w, byID: map[string]epbinding.Binding{}}
	w.schedules = &schedules{w: w, byID: map[string]script.Schedule{}}
	w.webhooks = &webhooks{w: w, byID: map[string]scriptevents.Handler{}}
	w.rule = &rule{}
	w.sources = &sourceList{}
	w.calls = &calls{w: w, byID: map[string]callhandler.Handler{}}
	w.versions = &versions{}
	return w
}

func (w *world) next(prefix string) string {
	w.seq++
	return fmt.Sprintf("%s_%d", prefix, w.seq)
}

// stamp is a creation time later than every one before it.
func (w *world) stamp() time.Time { return fakeEpoch.Add(time.Duration(w.seq) * time.Minute) }

// add saves a script owned by owner.
func (w *world) add(id, name, owner, source string) *script.Script {
	sc := &script.Script{
		ID: id, Name: name, OwnerEmail: owner, Source: source, Enabled: true, Status: script.StatusActive,
		IsForm: source == formSource, Version: 1,
	}
	w.scripts[id] = sc
	return sc
}

// service is the Service over the world, its endpoint kind a real
// epcmd.Handler over the world's bindings.
func (w *world) service() *Service {
	return &Service{
		View: view{w}, Scripts: scripts{w}, Endpoints: &epcmd.Handler{Store: w.bindings, BaseURL: "https://data.example.com"},
		Schedules: w.schedules, Webhooks: w.webhooks, Sources: w.rule, SourceList: w.sources,
		Now: func() time.Time { return fakeEpoch }, Calls: w.calls, Versions: w.versions,
	}
}

type scripts struct{ w *world }

func (s scripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	if s.w.scriptErr != nil {
		return nil, s.w.scriptErr
	}
	return s.w.scripts[id], nil
}

// view is the script_handlers view over the world.
type view struct{ w *world }

func (v view) rows() []Handler {
	var out []Handler
	row := func(id, kind, typ, scriptID, target string, enabled bool, by string, at time.Time) {
		sc := v.w.scripts[scriptID]
		if sc == nil {
			return
		}
		out = append(out, Handler{
			ID: id, Kind: kind, Type: typ, ScriptID: scriptID, ScriptName: sc.Name, OwnerEmail: sc.OwnerEmail,
			Target: target, Enabled: enabled, CreatedBy: by, CreatedAt: at,
		})
	}
	for _, b := range v.w.bindings.byID {
		typ := KindEndpoint
		if sc := v.w.scripts[b.ScriptID]; sc != nil && sc.IsForm {
			typ = TypeForm
		}
		row(b.ID, KindEndpoint, typ, b.ScriptID, b.Name, b.Enabled, b.CreatedBy, b.CreatedAt)
	}
	for _, s := range v.w.schedules.byID {
		row(s.ID, KindSchedule, KindSchedule, s.ScriptID, s.CronSpec+" "+s.Timezone, s.Enabled, s.CreatedBy, s.CreatedAt)
	}
	for _, h := range v.w.webhooks.byID {
		row(h.ID, KindWebhook, KindWebhook, h.ScriptID, h.Source, h.Enabled, h.CreatedBy, h.CreatedAt)
	}
	for _, h := range v.w.calls.byID {
		row(h.ID, h.Kind, h.Kind, h.ScriptID, h.Connection, h.Enabled, h.CreatedBy, h.CreatedAt)
	}
	slices.SortFunc(out, func(a, b Handler) int {
		return cmp.Or(strings.Compare(a.ScriptName, b.ScriptName), strings.Compare(a.Kind, b.Kind),
			a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return out
}

func (view) matches(f Filter, h Handler) bool {
	switch {
	case len(f.Types) > 0 && !slices.Contains(f.Types, h.Type):
		return false
	case f.ScriptIDs != nil && !slices.Contains(f.ScriptIDs, h.ScriptID):
		return false
	case f.OwnerEmail != "" && !strings.EqualFold(f.OwnerEmail, h.OwnerEmail):
		return false
	case f.Enabled != nil && *f.Enabled != h.Enabled:
		return false
	}
	return f.Search == "" || strings.Contains(h.ScriptName, f.Search) || strings.Contains(h.Target, f.Search)
}

func (v view) List(_ context.Context, f Filter) ([]Handler, int, error) {
	if v.w.viewErr != nil {
		return nil, 0, v.w.viewErr
	}
	all := []Handler{}
	for _, h := range v.rows() {
		if v.matches(f, h) {
			all = append(all, h)
		}
	}
	return all, len(all), nil
}

func (v view) Get(_ context.Context, id string) (Handler, error) {
	if v.w.viewErr != nil {
		return Handler{}, v.w.viewErr
	}
	for _, h := range v.rows() {
		if h.ID == id {
			return h, nil
		}
	}
	return Handler{}, fmt.Errorf("no handler %s: %w", id, ErrNotFound)
}

func (v view) TypesOf(_ context.Context, ids []string) (map[string][]string, error) {
	if v.w.viewErr != nil {
		return nil, v.w.viewErr
	}
	out := map[string][]string{}
	for _, h := range v.rows() {
		if slices.Contains(ids, h.ScriptID) && !slices.Contains(out[h.ScriptID], h.Type) {
			out[h.ScriptID] = append(out[h.ScriptID], h.Type)
		}
	}
	for id := range out {
		slices.Sort(out[id])
	}
	return out, nil
}

// bindings is script_endpoints: one name per binding, a replace held to its
// own script.
type bindings struct {
	w    *world
	byID map[string]epbinding.Binding
	// getErr fails GetByID; err every other method.
	getErr, err error
}

func (b *bindings) Set(_ context.Context, in *epbinding.Binding) error {
	if b.err != nil {
		return b.err
	}
	if in.ID != "" {
		if prev, ok := b.byID[in.ID]; !ok || prev.ScriptID != in.ScriptID {
			return epbinding.ErrNotFound
		}
	}
	for id, other := range b.byID {
		if other.Name == in.Name && id != in.ID {
			return epbinding.ErrNameTaken
		}
	}
	if in.ID == "" {
		in.ID, in.CreatedBy, in.CreatedAt = b.w.next("ep"), in.UpdatedBy, b.w.stamp()
	}
	b.byID[in.ID] = *in
	return nil
}

func (b *bindings) GetByID(_ context.Context, id string) (*epbinding.Binding, error) {
	if b.getErr != nil {
		return nil, b.getErr
	}
	out, ok := b.byID[id]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &out, nil
}

func (b *bindings) ByScript(_ context.Context, scriptID string) ([]epbinding.Binding, error) {
	if b.err != nil {
		return nil, b.err
	}
	out := []epbinding.Binding{}
	for _, x := range b.byID {
		if x.ScriptID == scriptID {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, c epbinding.Binding) int { return strings.Compare(a.Name, c.Name) })
	return out, nil
}

func (b *bindings) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	for _, x := range b.byID {
		if x.Name == name {
			return &x, nil
		}
	}
	return nil, epbinding.ErrNotFound
}

func (*bindings) List(context.Context, epbinding.Filter) ([]epbinding.Binding, int, error) {
	return nil, 0, nil
}

func (b *bindings) Enable(_ context.Context, id string, enabled bool, actor string) error {
	if b.err != nil {
		return b.err
	}
	x, ok := b.byID[id]
	if !ok {
		return epbinding.ErrNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	b.byID[id] = x
	return nil
}

func (b *bindings) Delete(_ context.Context, id string) error {
	if b.err != nil {
		return b.err
	}
	if _, ok := b.byID[id]; !ok {
		return epbinding.ErrNotFound
	}
	delete(b.byID, id)
	return nil
}

func (*bindings) RecordFailure(context.Context, epbinding.Failure) error { return nil }
func (*bindings) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	return []epbinding.Failure{}, nil
}
func (*bindings) SweepFailures(context.Context, time.Time) (int64, error) { return 0, nil }
func (*bindings) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{}, nil
}

// schedules is script_schedules: a replace held to its own script, and a
// schedule its runs name refused on delete.
type schedules struct {
	w    *world
	byID map[string]script.Schedule
	// hasRuns marks the schedules their runs name.
	hasRuns map[string]bool
	// listErr fails ListSchedules, readErr ScheduleByID, err every write.
	listErr, readErr, err error
}

func (s *schedules) SetSchedule(_ context.Context, in *script.Schedule) error {
	if s.err != nil {
		return s.err
	}
	if in.ID == "" {
		in.ID, in.CreatedAt = s.w.next("sched"), s.w.stamp()
	} else if prev, ok := s.byID[in.ID]; !ok || prev.ScriptID != in.ScriptID {
		return script.ErrScheduleNotFound
	}
	s.byID[in.ID] = *in
	return nil
}

func (s *schedules) ScheduleByID(_ context.Context, id string) (*script.Schedule, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	out, ok := s.byID[id]
	if !ok {
		return nil, script.ErrScheduleNotFound
	}
	return &out, nil
}

func (s *schedules) ListSchedules(_ context.Context, f script.ScheduleFilter) ([]script.Schedule, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := []script.Schedule{}
	for _, x := range s.byID {
		if f.ScriptID == "" || x.ScriptID == f.ScriptID {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b script.Schedule) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (s *schedules) EnableSchedule(_ context.Context, id string, enabled bool, actor string) error {
	if s.err != nil {
		return s.err
	}
	x, ok := s.byID[id]
	if !ok {
		return script.ErrScheduleNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	s.byID[id] = x
	return nil
}

func (s *schedules) DeleteSchedule(_ context.Context, id string) error {
	if s.err != nil {
		return s.err
	}
	if s.hasRuns[id] {
		return scriptstore.ErrScheduleHasRuns
	}
	if _, ok := s.byID[id]; !ok {
		return script.ErrScheduleNotFound
	}
	delete(s.byID, id)
	return nil
}

func (*schedules) DueSchedules(context.Context, time.Time, int) ([]script.Schedule, error) {
	return nil, nil
}

func (*schedules) MaterializeRun(context.Context, *script.Run) (script.Materialization, error) {
	return script.MaterializedRun, nil
}

func (*schedules) AdvanceSchedule(context.Context, script.ScheduleAdvance) (bool, error) {
	return true, nil
}

// webhooks is script_webhook_handlers: one handler per (script, source).
type webhooks struct {
	w    *world
	byID map[string]scriptevents.Handler
	// listErr fails Handlers, readErr HandlerByID, err every write.
	listErr, readErr, err error
}

func (h *webhooks) AddHandler(_ context.Context, scriptID, source, actor string) (scriptevents.Handler, bool, error) {
	if h.err != nil {
		return scriptevents.Handler{}, false, h.err
	}
	for id, x := range h.byID {
		if x.ScriptID == scriptID && x.Source == source {
			x.Enabled, x.UpdatedBy = true, actor
			h.byID[id] = x
			return x, false, nil
		}
	}
	x := scriptevents.Handler{
		ID: h.w.next("wh"), ScriptID: scriptID, Source: source, Enabled: true,
		CreatedBy: actor, UpdatedBy: actor, CreatedAt: h.w.stamp(), UpdatedAt: h.w.stamp(),
	}
	h.byID[x.ID] = x
	return x, true, nil
}

func (h *webhooks) HandlerByID(_ context.Context, id string) (scriptevents.Handler, error) {
	if h.readErr != nil {
		return scriptevents.Handler{}, h.readErr
	}
	x, ok := h.byID[id]
	if !ok {
		return scriptevents.Handler{}, scriptevents.ErrHandlerNotFound
	}
	return x, nil
}

func (h *webhooks) Handlers(_ context.Context, f scriptevents.HandlerFilter) ([]scriptevents.Handler, error) {
	if h.listErr != nil {
		return nil, h.listErr
	}
	out := []scriptevents.Handler{}
	for _, x := range h.byID {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, x.ScriptID) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (h *webhooks) EnableHandler(_ context.Context, id string, enabled bool, actor string) error {
	if h.err != nil {
		return h.err
	}
	x, ok := h.byID[id]
	if !ok {
		return scriptevents.ErrHandlerNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	h.byID[id] = x
	return nil
}

func (h *webhooks) DeleteHandler(_ context.Context, id string) error {
	if h.err != nil {
		return h.err
	}
	if _, ok := h.byID[id]; !ok {
		return scriptevents.ErrHandlerNotFound
	}
	delete(h.byID, id)
	return nil
}

// rule is sourcerule's decision: an administrator any source, anyone else
// only form-<name>, and broken a read that fails.
type rule struct{ asked []string }

func (r *rule) Check(_ context.Context, source, email string, admin bool) error {
	r.asked = append(r.asked, fmt.Sprintf("%s %s %v", source, email, admin))
	switch {
	case source == "":
		return fmt.Errorf("event_source is required: %w", sourcerule.ErrSourceRefused)
	case source == "broken":
		return errDown
	case admin || strings.HasPrefix(source, whsource.FormPrefix):
		return nil
	}
	return fmt.Errorf("only an administrator runs a script for source %s: %w", source, sourcerule.ErrSourceRefused)
}

// sourceList is the deployment's webhook sources.
type sourceList struct {
	all []whsource.Source
	err error
}

func (s *sourceList) List(context.Context) ([]whsource.Source, error) { return s.all, s.err }

// calls keeps tool-call handlers in memory.
type calls struct {
	w    *world
	byID map[string]callhandler.Handler
	// failures are every handler's failures; err fails every write.
	failures map[string][]callhandler.Failure
	stats    map[string]callhandler.Stat
	err      error
}

func (c *calls) Insert(_ context.Context, h *callhandler.Handler) error {
	if c.err != nil {
		return c.err
	}
	h.ID, h.CreatedAt = c.w.next("ch"), c.w.stamp()
	c.byID[h.ID] = *h
	return nil
}

func (c *calls) Update(_ context.Context, h *callhandler.Handler) error {
	if c.err != nil {
		return c.err
	}
	if _, ok := c.byID[h.ID]; !ok {
		return callhandler.ErrNotFound
	}
	c.byID[h.ID] = *h
	return nil
}

func (c *calls) Get(_ context.Context, id string) (*callhandler.Handler, error) {
	h, ok := c.byID[id]
	if !ok {
		return nil, callhandler.ErrNotFound
	}
	return &h, nil
}

func (c *calls) ByScript(_ context.Context, scriptID string) ([]callhandler.Handler, error) {
	var out []callhandler.Handler
	for _, h := range c.byID {
		if h.ScriptID == scriptID {
			out = append(out, h)
		}
	}
	return out, c.err
}

func (c *calls) SetEnabled(_ context.Context, id string, enabled bool, by string) error {
	h, ok := c.byID[id]
	if !ok {
		return callhandler.ErrNotFound
	}
	h.Enabled, h.UpdatedBy = enabled, by
	c.byID[id] = h
	return nil
}

func (c *calls) Delete(_ context.Context, id string) error {
	if _, ok := c.byID[id]; !ok {
		return callhandler.ErrNotFound
	}
	delete(c.byID, id)
	return nil
}

func (c *calls) Failures(_ context.Context, id string, _ int) ([]callhandler.Failure, error) {
	return append([]callhandler.Failure{}, c.failures[id]...), nil
}

// versions records what an administrator approved for handlers.
type versions struct {
	approved map[string]int
	by       string
	waiting  int
	err      error
}

func (v *versions) ApproveFor(_ context.Context, c Caller, sc *script.Script, version int) error {
	if v.err != nil {
		return v.err
	}
	if v.approved == nil {
		v.approved = map[string]int{}
	}
	if version == 0 {
		version = sc.Version
	}
	v.approved[sc.ID], v.by = version, c.Email
	return nil
}

func (v *versions) State(_ context.Context, sc *script.Script) (running int, approver string, waiting int) {
	return v.approved[sc.ID], v.by, v.waiting
}

func (c *calls) All(context.Context) ([]callhandler.Handler, error) {
	if c.err != nil {
		return nil, c.err
	}
	out := make([]callhandler.Handler, 0, len(c.byID))
	for _, h := range c.byID {
		out = append(out, h)
	}
	return out, nil
}

func (c *calls) Stats(_ context.Context, _ string) (map[string]callhandler.Stat, error) {
	if c.stats == nil {
		return nil, errors.New("no stats")
	}
	return c.stats, nil
}

func (c *calls) SetOrder(_ context.Context, ids []string, by string) error {
	if c.err != nil {
		return c.err
	}
	for i, id := range ids {
		h, ok := c.byID[id]
		if !ok {
			return callhandler.ErrNotFound
		}
		h.Order, h.UpdatedBy = i, by
		c.byID[id] = h
	}
	return nil
}
