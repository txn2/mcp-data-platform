package handlerhttp

import (
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
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// world is a deployment's scripts and handler tables in memory, with the
// script_handlers view derived from them.
type world struct {
	scripts   map[string]*script.Script
	bindings  map[string]epbinding.Binding
	schedules map[string]script.Schedule
	webhooks  map[string]scriptevents.Handler
	hasRuns   map[string]bool
	sources   []whsource.Source
	// calls are the tool-call handlers (#2436).
	calls map[string]callhandler.Handler
	seq   int
	// viewErr fails the view, scheduleErr the schedule table, sourcesErr the
	// source listing.
	viewErr, scheduleErr, sourcesErr error
	asked                            scripthandler.Filter
}

func newWorld() *world {
	return &world{
		scripts: map[string]*script.Script{}, bindings: map[string]epbinding.Binding{},
		schedules: map[string]script.Schedule{}, webhooks: map[string]scriptevents.Handler{}, hasRuns: map[string]bool{},
	}
}

func (w *world) id(prefix string) string {
	w.seq++
	return fmt.Sprintf("%s_%d", prefix, w.seq)
}

func (w *world) service() *scripthandler.Service {
	return &scripthandler.Service{
		View: view{w}, Scripts: scripts{w}, Endpoints: &epcmd.Handler{Store: bindings{w}, BaseURL: "https://data.example.com"},
		Schedules: schedules{w}, Webhooks: webhooks{w}, Sources: rule{}, SourceList: sourceList{w}, Now: time.Now,
	}
}

type scripts struct{ w *world }

func (s scripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	if id == "broken" {
		return nil, errors.New("db down")
	}
	return s.w.scripts[id], nil
}

type view struct{ w *world }

func (v view) rows() []scripthandler.Handler {
	var out []scripthandler.Handler
	add := func(id, kind, scriptID, target string, enabled bool) {
		sc := v.w.scripts[scriptID]
		if sc == nil {
			return
		}
		typ := kind
		if kind == scripthandler.KindEndpoint && sc.IsForm {
			typ = scripthandler.TypeForm
		}
		out = append(out, scripthandler.Handler{
			ID: id, Kind: kind, Type: typ, ScriptID: scriptID, ScriptName: sc.Name, OwnerEmail: sc.OwnerEmail,
			Target: target, Enabled: enabled,
		})
	}
	for _, b := range v.w.bindings {
		add(b.ID, scripthandler.KindEndpoint, b.ScriptID, b.Name, b.Enabled)
	}
	for _, s := range v.w.schedules {
		add(s.ID, scripthandler.KindSchedule, s.ScriptID, s.CronSpec+" "+s.Timezone, s.Enabled)
	}
	for _, h := range v.w.webhooks {
		add(h.ID, scripthandler.KindWebhook, h.ScriptID, h.Source, h.Enabled)
	}
	for _, h := range v.w.calls {
		add(h.ID, h.Kind, h.ScriptID, h.Connection, h.Enabled)
	}
	slices.SortFunc(out, func(a, b scripthandler.Handler) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (v view) List(_ context.Context, f scripthandler.Filter) ([]scripthandler.Handler, int, error) {
	v.w.asked = f
	if v.w.viewErr != nil {
		return nil, 0, v.w.viewErr
	}
	out := []scripthandler.Handler{}
	for _, h := range v.rows() {
		switch {
		case len(f.Types) > 0 && !slices.Contains(f.Types, h.Type):
		case f.ScriptIDs != nil && !slices.Contains(f.ScriptIDs, h.ScriptID):
		case f.OwnerEmail != "" && !strings.EqualFold(f.OwnerEmail, h.OwnerEmail):
		case f.Enabled != nil && *f.Enabled != h.Enabled:
		case f.Search != "" && !strings.Contains(h.ScriptName+" "+h.Target, f.Search):
		default:
			out = append(out, h)
		}
	}
	return out, len(out), nil
}

func (v view) Get(_ context.Context, id string) (scripthandler.Handler, error) {
	if v.w.viewErr != nil {
		return scripthandler.Handler{}, v.w.viewErr
	}
	for _, h := range v.rows() {
		if h.ID == id {
			return h, nil
		}
	}
	return scripthandler.Handler{}, fmt.Errorf("no handler %s: %w", id, scripthandler.ErrNotFound)
}

func (view) TypesOf(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

type bindings struct{ w *world }

func (b bindings) Set(_ context.Context, in *epbinding.Binding) error {
	for id, x := range b.w.bindings {
		if x.Name == in.Name && id != in.ID {
			return epbinding.ErrNameTaken
		}
	}
	if in.ID == "" {
		in.ID = b.w.id("ep")
	}
	b.w.bindings[in.ID] = *in
	return nil
}

func (b bindings) GetByID(_ context.Context, id string) (*epbinding.Binding, error) {
	x, ok := b.w.bindings[id]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &x, nil
}

func (b bindings) ByScript(_ context.Context, scriptID string) ([]epbinding.Binding, error) {
	out := []epbinding.Binding{}
	for _, x := range b.w.bindings {
		if x.ScriptID == scriptID {
			out = append(out, x)
		}
	}
	return out, nil
}

func (b bindings) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	for _, x := range b.w.bindings {
		if x.Name == name {
			return &x, nil
		}
	}
	return nil, epbinding.ErrNotFound
}

func (bindings) List(context.Context, epbinding.Filter) ([]epbinding.Binding, int, error) {
	return nil, 0, nil
}

func (b bindings) Enable(_ context.Context, id string, enabled bool, actor string) error {
	x, ok := b.w.bindings[id]
	if !ok {
		return epbinding.ErrNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	b.w.bindings[id] = x
	return nil
}

func (b bindings) Delete(_ context.Context, id string) error {
	delete(b.w.bindings, id)
	return nil
}

func (bindings) RecordFailure(context.Context, epbinding.Failure) error { return nil }
func (bindings) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	return []epbinding.Failure{}, nil
}
func (bindings) SweepFailures(context.Context, time.Time) (int64, error) { return 0, nil }
func (bindings) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{}, nil
}

type schedules struct{ w *world }

func (s schedules) SetSchedule(_ context.Context, in *script.Schedule) error {
	if s.w.scheduleErr != nil {
		return s.w.scheduleErr
	}
	if in.ID == "" {
		in.ID = s.w.id("sched")
	}
	s.w.schedules[in.ID] = *in
	return nil
}

func (s schedules) ScheduleByID(_ context.Context, id string) (*script.Schedule, error) {
	x, ok := s.w.schedules[id]
	if !ok {
		return nil, script.ErrScheduleNotFound
	}
	return &x, nil
}

func (s schedules) ListSchedules(_ context.Context, f script.ScheduleFilter) ([]script.Schedule, error) {
	if s.w.scheduleErr != nil {
		return nil, s.w.scheduleErr
	}
	out := []script.Schedule{}
	for _, x := range s.w.schedules {
		if x.ScriptID == f.ScriptID {
			out = append(out, x)
		}
	}
	return out, nil
}

func (s schedules) EnableSchedule(_ context.Context, id string, enabled bool, actor string) error {
	x, ok := s.w.schedules[id]
	if !ok {
		return script.ErrScheduleNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	s.w.schedules[id] = x
	return nil
}

func (s schedules) DeleteSchedule(_ context.Context, id string) error {
	if s.w.hasRuns[id] {
		return scriptstore.ErrScheduleHasRuns
	}
	delete(s.w.schedules, id)
	return nil
}

func (schedules) DueSchedules(context.Context, time.Time, int) ([]script.Schedule, error) {
	return nil, nil
}

func (schedules) MaterializeRun(context.Context, *script.Run) (script.Materialization, error) {
	return script.MaterializedRun, nil
}

func (schedules) AdvanceSchedule(context.Context, script.ScheduleAdvance) (bool, error) {
	return true, nil
}

type webhooks struct{ w *world }

func (h webhooks) AddHandler(_ context.Context, scriptID, source, actor string) (scriptevents.Handler, bool, error) {
	for id, x := range h.w.webhooks {
		if x.ScriptID == scriptID && x.Source == source {
			x.Enabled = true
			h.w.webhooks[id] = x
			return x, false, nil
		}
	}
	x := scriptevents.Handler{ID: h.w.id("wh"), ScriptID: scriptID, Source: source, Enabled: true, CreatedBy: actor}
	h.w.webhooks[x.ID] = x
	return x, true, nil
}

func (h webhooks) HandlerByID(_ context.Context, id string) (scriptevents.Handler, error) {
	x, ok := h.w.webhooks[id]
	if !ok {
		return scriptevents.Handler{}, scriptevents.ErrHandlerNotFound
	}
	return x, nil
}

func (h webhooks) Handlers(_ context.Context, f scriptevents.HandlerFilter) ([]scriptevents.Handler, error) {
	out := []scriptevents.Handler{}
	for _, x := range h.w.webhooks {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, x.ScriptID) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (h webhooks) EnableHandler(_ context.Context, id string, enabled bool, _ string) error {
	x, ok := h.w.webhooks[id]
	if !ok {
		return scriptevents.ErrHandlerNotFound
	}
	x.Enabled = enabled
	h.w.webhooks[id] = x
	return nil
}

func (h webhooks) DeleteHandler(_ context.Context, id string) error {
	delete(h.w.webhooks, id)
	return nil
}

// rule lets an administrator name any source and anyone else form-*.
type rule struct{}

func (rule) Check(_ context.Context, source, _ string, admin bool) error {
	if admin || strings.HasPrefix(source, whsource.FormPrefix) {
		return nil
	}
	return fmt.Errorf("only an administrator names source %s: %w", source, sourcerule.ErrSourceRefused)
}

type sourceList struct{ w *world }

func (s sourceList) List(context.Context) ([]whsource.Source, error) {
	return s.w.sources, s.w.sourcesErr
}
