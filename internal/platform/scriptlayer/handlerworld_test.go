package scriptlayer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
)

// handlerWorld is a deployment's handler tables in memory: the script store's
// schedules, the endpoint bindings, the webhook handlers, and the
// script_handlers view derived from them as the migration derives it.
type handlerWorld struct {
	store    *memStore
	bindings *memBindings
	webhooks *memWebhooks
	rule     *sourceRule
	// viewErr fails every read of the view.
	viewErr error
}

// handlerHandle is a script layer whose deployment keeps handlers of every
// kind, with the author's endpoint script "lookup" saved; checks names the
// bot checks that exist.
func handlerHandle(t *testing.T, checks ...string) (*Handle, *handlerWorld) {
	t.Helper()
	store := newMemStore()
	bindings := &memBindings{byID: map[string]epbinding.Binding{}}
	commands := &epcmd.Handler{
		Store: bindings, BaseURL: "https://data.example.com",
		CheckUsable: func(_ context.Context, name string) error {
			if !slices.Contains(checks, name) {
				return fmt.Errorf("no bot check is named %q", name)
			}
			return nil
		},
	}
	h := New(Config{Store: store, Runs: newStubRuns(), Endpoints: &epwire.Endpoints{Commands: commands}})
	w := withHandlers(h, store, bindings, commands)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "lookup", Source: handlerSource})
	require.False(t, res.IsError, resultText(res))
	return h, w
}

// withHandlers gives h the handler service a deployment with a database has,
// over store, bindings and commands (nil for no endpoint kind), with its save
// check.
func withHandlers(h *Handle, store *memStore, bindings *memBindings, commands *epcmd.Handler) *handlerWorld {
	w := &handlerWorld{
		store: store, bindings: bindings, webhooks: &memWebhooks{byID: map[string]scriptevents.Handler{}}, rule: &sourceRule{},
	}
	if w.bindings == nil {
		w.bindings = &memBindings{byID: map[string]epbinding.Binding{}}
	}
	h.handlers = &scripthandler.Service{
		View: handlerView{w}, Scripts: store, Endpoints: commands, Schedules: store,
		Webhooks: w.webhooks, Sources: w.rule, Now: time.Now,
	}
	h.gate.Handlers = h.handlers.Check
	return w
}

// handlerView is script_handlers over the world.
type handlerView struct{ w *handlerWorld }

func (v handlerView) rows() []scripthandler.Handler {
	var out []scripthandler.Handler
	add := func(id, kind, typ, scriptID, target string, enabled bool, at time.Time) {
		sc := v.w.store.scripts[scriptID]
		if sc == nil {
			return
		}
		if kind == scripthandler.KindEndpoint && epform.DefinesSource(sc.Source) {
			typ = scripthandler.TypeForm
		}
		out = append(out, scripthandler.Handler{
			ID: id, Kind: kind, Type: typ, ScriptID: scriptID, ScriptName: sc.Name, OwnerEmail: sc.OwnerEmail,
			Target: target, Enabled: enabled, CreatedAt: at,
		})
	}
	for _, b := range v.w.bindings.byID {
		add(b.ID, scripthandler.KindEndpoint, scripthandler.KindEndpoint, b.ScriptID, b.Name, b.Enabled, b.CreatedAt)
	}
	for _, s := range v.w.store.schedules {
		add(s.ID, scripthandler.KindSchedule, scripthandler.KindSchedule, s.ScriptID, s.CronSpec+" "+s.Timezone, s.Enabled, s.CreatedAt)
	}
	for _, h := range v.w.webhooks.byID {
		add(h.ID, scripthandler.KindWebhook, scripthandler.KindWebhook, h.ScriptID, h.Source, h.Enabled, h.CreatedAt)
	}
	slices.SortFunc(out, func(a, b scripthandler.Handler) int {
		return cmp.Or(strings.Compare(a.ScriptName, b.ScriptName), strings.Compare(a.Kind, b.Kind), strings.Compare(a.ID, b.ID))
	})
	return out
}

func (v handlerView) List(_ context.Context, f scripthandler.Filter) ([]scripthandler.Handler, int, error) {
	if v.w.viewErr != nil {
		return nil, 0, v.w.viewErr
	}
	out := []scripthandler.Handler{}
	for _, h := range v.rows() {
		switch {
		case len(f.Types) > 0 && !slices.Contains(f.Types, h.Type):
		case f.ScriptIDs != nil && !slices.Contains(f.ScriptIDs, h.ScriptID):
		case f.OwnerEmail != "" && !strings.EqualFold(f.OwnerEmail, h.OwnerEmail):
		default:
			out = append(out, h)
		}
	}
	return out, len(out), nil
}

func (v handlerView) Get(_ context.Context, id string) (scripthandler.Handler, error) {
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

func (handlerView) TypesOf(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// memWebhooks is script_webhook_handlers: one handler per (script, source).
type memWebhooks struct {
	byID map[string]scriptevents.Handler
	seq  int
}

func (m *memWebhooks) AddHandler(_ context.Context, scriptID, source, actor string) (scriptevents.Handler, bool, error) {
	for id, x := range m.byID {
		if x.ScriptID == scriptID && x.Source == source {
			x.Enabled = true
			m.byID[id] = x
			return x, false, nil
		}
	}
	m.seq++
	x := scriptevents.Handler{
		ID: fmt.Sprintf("wh_%d", m.seq), ScriptID: scriptID, Source: source, Enabled: true, CreatedBy: actor, UpdatedBy: actor,
		CreatedAt: time.Unix(int64(m.seq), 0),
	}
	m.byID[x.ID] = x
	return x, true, nil
}

func (m *memWebhooks) HandlerByID(_ context.Context, id string) (scriptevents.Handler, error) {
	if x, ok := m.byID[id]; ok {
		return x, nil
	}
	return scriptevents.Handler{}, scriptevents.ErrHandlerNotFound
}

func (m *memWebhooks) Handlers(_ context.Context, f scriptevents.HandlerFilter) ([]scriptevents.Handler, error) {
	out := []scriptevents.Handler{}
	for _, x := range m.byID {
		if f.ScriptIDs == nil || slices.Contains(f.ScriptIDs, x.ScriptID) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (m *memWebhooks) EnableHandler(_ context.Context, id string, enabled bool, actor string) error {
	x, ok := m.byID[id]
	if !ok {
		return scriptevents.ErrHandlerNotFound
	}
	x.Enabled, x.UpdatedBy = enabled, actor
	m.byID[id] = x
	return nil
}

func (m *memWebhooks) DeleteHandler(_ context.Context, id string) error {
	if _, ok := m.byID[id]; !ok {
		return scriptevents.ErrHandlerNotFound
	}
	delete(m.byID, id)
	return nil
}

// sourceRule is the source rule over two sources: shop, any administrator's
// to name, and form-daily, the form source of a script jane owns.
type sourceRule struct{ broken bool }

func (r *sourceRule) Check(_ context.Context, source, email string, admin bool) error {
	switch {
	case r.broken:
		return errors.New("reading the form source's script: db down")
	case source == "":
		return fmt.Errorf("event_source is required: %w", sourcerule.ErrSourceRefused)
	case source != "shop" && source != "form-daily":
		return fmt.Errorf("no webhook source is named %q", source)
	case admin, source == "form-daily" && email == "jane@example.com":
		return nil
	}
	return fmt.Errorf("only an administrator runs a script for a source that is not the source of a form they own: %w",
		sourcerule.ErrSourceRefused)
}
