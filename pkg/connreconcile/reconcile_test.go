package connreconcile

import (
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// recordingToolkit is a registry.Toolkit that also implements
// toolkit.ConnectionManager, recording the Add/Remove call order so the
// reconciler's sequencing can be asserted.
type recordingToolkit struct {
	kind      string
	name      string
	has       bool
	addErr    error
	removeErr error

	events     []string
	addConfigs []map[string]any
}

func (t *recordingToolkit) Kind() string                          { return t.kind }
func (t *recordingToolkit) Name() string                          { return t.name }
func (*recordingToolkit) Connection() string                      { return "" }
func (*recordingToolkit) Tools() []string                         { return nil }
func (*recordingToolkit) RegisterTools(_ *mcp.Server)             {}
func (*recordingToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*recordingToolkit) SetQueryProvider(_ query.Provider)       {}
func (*recordingToolkit) Close() error                            { return nil }

func (t *recordingToolkit) HasConnection(string) bool { return t.has }
func (t *recordingToolkit) AddConnection(name string, config map[string]any) error {
	t.events = append(t.events, "add:"+name)
	t.addConfigs = append(t.addConfigs, config)
	return t.addErr
}

func (t *recordingToolkit) RemoveConnection(name string) error {
	t.events = append(t.events, "remove:"+name)
	return t.removeErr
}

// plainToolkit implements registry.Toolkit but NOT ConnectionManager, so the
// reconciler must skip it even when its kind matches.
type plainToolkit struct{ kind, name string }

func (t *plainToolkit) Kind() string                          { return t.kind }
func (t *plainToolkit) Name() string                          { return t.name }
func (*plainToolkit) Connection() string                      { return "" }
func (*plainToolkit) Tools() []string                         { return nil }
func (*plainToolkit) RegisterTools(_ *mcp.Server)             {}
func (*plainToolkit) SetSemanticProvider(_ semantic.Provider) {}
func (*plainToolkit) SetQueryProvider(_ query.Provider)       {}
func (*plainToolkit) Close() error                            { return nil }

func mustRegister(t *testing.T, tks ...registry.Toolkit) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	for _, tk := range tks {
		if err := reg.Register(tk); err != nil {
			t.Fatalf("register toolkit: %v", err)
		}
	}
	return reg
}

func TestReconciler_Remove(t *testing.T) {
	const kind, name = "api", "c1"

	t.Run("removes only from matching toolkits that hold the connection", func(t *testing.T) {
		match := &recordingToolkit{kind: kind, name: "match", has: true}
		absent := &recordingToolkit{kind: kind, name: "absent", has: false}
		otherKind := &recordingToolkit{kind: "s3", name: "other", has: true}
		reg := mustRegister(t, match, absent, otherKind)

		failures := New(reg).Remove(kind, name)

		if len(failures) != 0 {
			t.Fatalf("expected no failures, got %v", failures)
		}
		if got := match.events; len(got) != 1 || got[0] != "remove:c1" {
			t.Errorf("matching toolkit events = %v, want [remove:c1]", got)
		}
		if len(absent.events) != 0 {
			t.Errorf("absent toolkit must not be touched, got %v", absent.events)
		}
		if len(otherKind.events) != 0 {
			t.Errorf("other-kind toolkit must not be touched, got %v", otherKind.events)
		}
	})

	t.Run("reports a remove failure as PhaseRemove", func(t *testing.T) {
		tk := &recordingToolkit{kind: kind, has: true, removeErr: errors.New("boom")}
		failures := New(mustRegister(t, tk)).Remove(kind, name)
		if len(failures) != 1 {
			t.Fatalf("expected one failure, got %d", len(failures))
		}
		if failures[0].Phase != PhaseRemove {
			t.Errorf("phase = %v, want PhaseRemove", failures[0].Phase)
		}
	})
}

func TestReconciler_Upsert(t *testing.T) {
	const kind, name = "api", "c1"
	cfg := map[string]any{"base_url": "https://new"}

	t.Run("adds without a prior remove when the connection is absent", func(t *testing.T) {
		tk := &recordingToolkit{kind: kind, has: false}
		failures := New(mustRegister(t, tk)).Upsert(kind, name, cfg)
		if len(failures) != 0 {
			t.Fatalf("expected no failures, got %v", failures)
		}
		if got := tk.events; len(got) != 1 || got[0] != "add:c1" {
			t.Errorf("events = %v, want [add:c1]", got)
		}
		if len(tk.addConfigs) != 1 || tk.addConfigs[0]["base_url"] != "https://new" {
			t.Errorf("add config = %v, want base_url=https://new", tk.addConfigs)
		}
	})

	t.Run("removes then adds when the connection already exists", func(t *testing.T) {
		tk := &recordingToolkit{kind: kind, has: true}
		New(mustRegister(t, tk)).Upsert(kind, name, cfg)
		want := []string{"remove:c1", "add:c1"}
		if len(tk.events) != 2 || tk.events[0] != want[0] || tk.events[1] != want[1] {
			t.Errorf("events = %v, want %v", tk.events, want)
		}
	})

	t.Run("a failed remove aborts the add for that toolkit", func(t *testing.T) {
		tk := &recordingToolkit{kind: kind, has: true, removeErr: errors.New("stuck")}
		failures := New(mustRegister(t, tk)).Upsert(kind, name, cfg)
		if got := tk.events; len(got) != 1 || got[0] != "remove:c1" {
			t.Errorf("events = %v, want [remove:c1] (add must be skipped)", got)
		}
		if len(failures) != 1 || failures[0].Phase != PhaseRemove {
			t.Errorf("failures = %v, want one PhaseRemove", failures)
		}
	})

	t.Run("a failed add is reported and does not stop other toolkits", func(t *testing.T) {
		failing := &recordingToolkit{kind: kind, name: "failing", has: false, addErr: errors.New("dial")}
		healthy := &recordingToolkit{kind: kind, name: "healthy", has: false}
		failures := New(mustRegister(t, failing, healthy)).Upsert(kind, name, cfg)

		if len(failures) != 1 || failures[0].Phase != PhaseAdd {
			t.Errorf("failures = %v, want one PhaseAdd", failures)
		}
		if got := healthy.events; len(got) != 1 || got[0] != "add:c1" {
			t.Errorf("healthy toolkit did not receive its add despite the other failing: %v", got)
		}
	})
}

func TestReconciler_SkipsNonManagerAndNilSource(t *testing.T) {
	const kind, name = "api", "c1"

	t.Run("skips a matching toolkit that is not a ConnectionManager", func(t *testing.T) {
		plain := &plainToolkit{kind: kind, name: "plain"}
		manager := &recordingToolkit{kind: kind, name: "manager", has: false}
		// Registering a same-kind non-manager alongside a real manager proves
		// the reconciler routes only to the manager, never to the plain toolkit.
		if failures := New(mustRegister(t, plain, manager)).Upsert(kind, name, nil); failures != nil {
			t.Errorf("expected no failures, got %v", failures)
		}
		if got := manager.events; len(got) != 1 || got[0] != "add:c1" {
			t.Errorf("manager events = %v, want [add:c1]", got)
		}
	})

	t.Run("nil source is a no-op", func(t *testing.T) {
		if f := New(nil).Remove(kind, name); f != nil {
			t.Errorf("Remove on nil source = %v, want nil", f)
		}
		if f := New(nil).Upsert(kind, name, nil); f != nil {
			t.Errorf("Upsert on nil source = %v, want nil", f)
		}
	})
}

func TestPhaseString(t *testing.T) {
	if PhaseRemove.String() != "remove" {
		t.Errorf("PhaseRemove.String() = %q, want remove", PhaseRemove.String())
	}
	if PhaseAdd.String() != "add" {
		t.Errorf("PhaseAdd.String() = %q, want add", PhaseAdd.String())
	}
}
