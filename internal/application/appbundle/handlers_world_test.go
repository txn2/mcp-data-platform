package appbundle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epsource"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The handler half of the world: each kind's table in memory, the
// script_handlers view derived from them, and the handler service over them,
// which an import sets handlers through as handler_set does.

var errDown = errors.New("db down")

// handlerFaults fail one handler store method each.
type handlerFaults struct {
	byScript, listSchedules, handlers, addHandler, enable, view error
}

func (w *world) nextID(prefix string) string {
	w.seq++
	return fmt.Sprintf("%s_%d", prefix, w.seq)
}

// handlerService is the service over the world's handler tables.
func (w *world) handlerService() *scripthandler.Service {
	h := &epcmd.Handler{
		Store: hbindings{w}, BaseURL: "https://data.example.com",
		// As the real binding refuses (#2554): a page belongs to an
		// application, so its script must be a part before it is bound.
		PageAllowed: func(_ context.Context, scriptID string) error {
			if !w.isPart(scriptID) {
				return errors.New("a page belongs to an application, and this script is a part of none")
			}
			return nil
		},
	}
	h.SetForms(formSources{w})
	return &scripthandler.Service{
		View: hview{w}, Scripts: scripts{w}, Endpoints: h, Schedules: hschedules{w}, Webhooks: hwebhooks{w},
		Sources: adminRule{}, Now: time.Now,
	}
}

// formSources records the form sources an endpoint on a form asks for.
type formSources struct{ w *world }

func (f formSources) Ensure(_ context.Context, req epsource.SourceRequest) (epsource.SourceResult, error) {
	f.w.formSources = append(f.w.formSources, req)
	return epsource.SourceResult{Source: "form-" + req.Endpoint, Connection: req.Connection}, nil
}

func (formSources) Find(context.Context, string) (epsource.SourceResult, bool, error) {
	return epsource.SourceResult{}, false, nil
}

func (formSources) ConnectionChoices() []string { return []string{"warehouse"} }

// adminRule lets an administrator name any source, as sourcerule does.
type adminRule struct{}

func (adminRule) Check(_ context.Context, source, _ string, admin bool) error {
	if !admin || source == "" {
		return errors.New("refused")
	}
	return nil
}

// hview is script_handlers over the world.
type hview struct{ w *world }

func (v hview) rows() []scripthandler.Handler {
	var out []scripthandler.Handler
	add := func(id, kind, typ, scriptID, target string, enabled bool) {
		sc := v.w.scripts[scriptID]
		if sc == nil {
			return
		}
		out = append(out, scripthandler.Handler{
			ID: id, Kind: kind, Type: typ, ScriptID: scriptID, ScriptName: sc.Name, OwnerEmail: sc.OwnerEmail,
			Target: target, Enabled: enabled,
		})
	}
	for _, b := range v.w.bindings {
		add(b.ID, scripthandler.KindEndpoint, scripthandler.KindEndpoint, b.ScriptID, b.Name, b.Enabled)
	}
	for _, s := range v.w.schedules {
		add(s.ID, scripthandler.KindSchedule, scripthandler.KindSchedule, s.ScriptID, s.CronSpec+" "+s.Timezone, s.Enabled)
	}
	for _, h := range v.w.webhooks {
		add(h.ID, scripthandler.KindWebhook, scripthandler.KindWebhook, h.ScriptID, h.Source, h.Enabled)
	}
	slices.SortFunc(out, func(a, b scripthandler.Handler) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (v hview) List(_ context.Context, f scripthandler.Filter) ([]scripthandler.Handler, int, error) {
	if v.w.faults.view != nil {
		return nil, 0, v.w.faults.view
	}
	out := []scripthandler.Handler{}
	for _, h := range v.rows() {
		if (f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, h.ScriptID)) && (len(f.Types) == 0 || slices.Contains(f.Types, h.Type)) {
			out = append(out, h)
		}
	}
	return out, len(out), nil
}

func (v hview) Get(_ context.Context, id string) (scripthandler.Handler, error) {
	for _, h := range v.rows() {
		if h.ID == id {
			return h, nil
		}
	}
	return scripthandler.Handler{}, scripthandler.ErrNotFound
}

func (hview) TypesOf(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// held is the handlers of the one script named name, by kind.
func (w *world) held(name string) (endpoints []epbinding.Binding, schedules []script.Schedule, webhooks []scriptevents.Handler) {
	var id string
	for k, sc := range w.scripts {
		if sc.Name == name {
			id = k
		}
	}
	for _, b := range w.bindings {
		if b.ScriptID == id {
			endpoints = append(endpoints, *b)
		}
	}
	for _, s := range w.schedules {
		if s.ScriptID == id {
			schedules = append(schedules, *s)
		}
	}
	for _, h := range w.webhooks {
		if h.ScriptID == id {
			webhooks = append(webhooks, *h)
		}
	}
	slices.SortFunc(endpoints, func(a, b epbinding.Binding) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(schedules, func(a, b script.Schedule) int { return strings.Compare(a.CronSpec, b.CronSpec) })
	slices.SortFunc(webhooks, func(a, b scriptevents.Handler) int { return strings.Compare(a.Source, b.Source) })
	return endpoints, schedules, webhooks
}

// hbindings is script_endpoints.
type hbindings struct{ w *world }

func (b hbindings) Set(_ context.Context, in *epbinding.Binding) error {
	for id, other := range b.w.bindings {
		if other.Name == in.Name && id != in.ID {
			return epbinding.ErrNameTaken
		}
	}
	if in.ID == "" {
		in.ID = b.w.nextID("ep")
	} else if prev, ok := b.w.bindings[in.ID]; !ok || prev.ScriptID != in.ScriptID {
		return epbinding.ErrNotFound
	}
	cp := *in
	b.w.bindings[in.ID] = &cp
	return nil
}

func (b hbindings) GetByID(_ context.Context, id string) (*epbinding.Binding, error) {
	if x, ok := b.w.bindings[id]; ok {
		cp := *x
		return &cp, nil
	}
	return nil, epbinding.ErrNotFound
}

func (b hbindings) ByScript(_ context.Context, scriptID string) ([]epbinding.Binding, error) {
	if b.w.faults.byScript != nil {
		return nil, b.w.faults.byScript
	}
	out := []epbinding.Binding{}
	for _, x := range b.w.bindings {
		if x.ScriptID == scriptID {
			out = append(out, *x)
		}
	}
	slices.SortFunc(out, func(a, c epbinding.Binding) int { return strings.Compare(a.Name, c.Name) })
	return out, nil
}

func (b hbindings) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	for _, x := range b.w.bindings {
		if x.Name == name {
			return x, nil
		}
	}
	return nil, epbinding.ErrNotFound
}

func (hbindings) List(context.Context, epbinding.Filter) ([]epbinding.Binding, int, error) {
	return nil, 0, nil
}

func (b hbindings) Enable(_ context.Context, id string, enabled bool, actor string) error {
	if b.w.faults.enable != nil {
		return b.w.faults.enable
	}
	x, ok := b.w.bindings[id]
	if !ok {
		return epbinding.ErrNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	return nil
}

func (b hbindings) Delete(_ context.Context, id string) error {
	delete(b.w.bindings, id)
	return nil
}

func (hbindings) RecordFailure(context.Context, epbinding.Failure) error { return nil }
func (hbindings) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	return []epbinding.Failure{}, nil
}
func (hbindings) SweepFailures(context.Context, time.Time) (int64, error) { return 0, nil }
func (hbindings) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{}, nil
}

// hschedules is script_schedules.
type hschedules struct{ w *world }

func (s hschedules) SetSchedule(_ context.Context, in *script.Schedule) error {
	if in.ID == "" {
		in.ID = s.w.nextID("sched")
	} else if prev, ok := s.w.schedules[in.ID]; !ok || prev.ScriptID != in.ScriptID {
		return script.ErrScheduleNotFound
	}
	cp := *in
	s.w.schedules[in.ID] = &cp
	return nil
}

func (s hschedules) ScheduleByID(_ context.Context, id string) (*script.Schedule, error) {
	if x, ok := s.w.schedules[id]; ok {
		cp := *x
		return &cp, nil
	}
	return nil, script.ErrScheduleNotFound
}

func (s hschedules) ListSchedules(_ context.Context, f script.ScheduleFilter) ([]script.Schedule, error) {
	if s.w.faults.listSchedules != nil {
		return nil, s.w.faults.listSchedules
	}
	out := []script.Schedule{}
	for _, x := range s.w.schedules {
		if f.ScriptID == "" || x.ScriptID == f.ScriptID {
			out = append(out, *x)
		}
	}
	slices.SortFunc(out, func(a, b script.Schedule) int { return strings.Compare(a.CronSpec, b.CronSpec) })
	return out, nil
}

func (s hschedules) EnableSchedule(_ context.Context, id string, enabled bool, actor string) error {
	if s.w.faults.enable != nil {
		return s.w.faults.enable
	}
	x, ok := s.w.schedules[id]
	if !ok {
		return script.ErrScheduleNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	return nil
}

func (s hschedules) DeleteSchedule(_ context.Context, id string) error {
	delete(s.w.schedules, id)
	return nil
}

func (hschedules) DueSchedules(context.Context, time.Time, int) ([]script.Schedule, error) {
	return nil, nil
}

func (hschedules) MaterializeRun(context.Context, *script.Run) (script.Materialization, error) {
	return script.MaterializedRun, nil
}

func (hschedules) AdvanceSchedule(context.Context, script.ScheduleAdvance) (bool, error) {
	return true, nil
}

// hwebhooks is script_webhook_handlers.
type hwebhooks struct{ w *world }

func (h hwebhooks) AddHandler(_ context.Context, scriptID, source, actor string) (scriptevents.Handler, bool, error) {
	if h.w.faults.addHandler != nil {
		return scriptevents.Handler{}, false, h.w.faults.addHandler
	}
	for _, x := range h.w.webhooks {
		if x.ScriptID == scriptID && x.Source == source {
			x.Enabled = true
			return *x, false, nil
		}
	}
	x := &scriptevents.Handler{ID: h.w.nextID("wh"), ScriptID: scriptID, Source: source, Enabled: true, CreatedBy: actor}
	h.w.webhooks[x.ID] = x
	return *x, true, nil
}

func (h hwebhooks) HandlerByID(_ context.Context, id string) (scriptevents.Handler, error) {
	if x, ok := h.w.webhooks[id]; ok {
		return *x, nil
	}
	return scriptevents.Handler{}, scriptevents.ErrHandlerNotFound
}

func (h hwebhooks) Handlers(_ context.Context, f scriptevents.HandlerFilter) ([]scriptevents.Handler, error) {
	if h.w.faults.handlers != nil {
		return nil, h.w.faults.handlers
	}
	out := []scriptevents.Handler{}
	for _, x := range h.w.webhooks {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, x.ScriptID) {
			out = append(out, *x)
		}
	}
	slices.SortFunc(out, func(a, b scriptevents.Handler) int { return strings.Compare(a.Source, b.Source) })
	return out, nil
}

func (h hwebhooks) EnableHandler(_ context.Context, id string, enabled bool, _ string) error {
	if h.w.faults.enable != nil {
		return h.w.faults.enable
	}
	x, ok := h.w.webhooks[id]
	if !ok {
		return scriptevents.ErrHandlerNotFound
	}
	x.Enabled = enabled
	return nil
}

func (h hwebhooks) DeleteHandler(_ context.Context, id string) error {
	delete(h.w.webhooks, id)
	return nil
}

// scriptOther is a script of another application holding an endpoint name.
var scriptOther = script.Script{ID: "other", Name: "other", OwnerEmail: "bob@example.com"}

func bindingOf(id, scriptID, name string) *epbinding.Binding {
	return &epbinding.Binding{ID: id, ScriptID: scriptID, Name: name, Methods: []string{"GET"}, AuthMode: epbinding.AuthAuthenticated}
}
