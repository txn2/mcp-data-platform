package platform

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// recordingConnMgr is a registry.Toolkit that also implements
// toolkit.ConnectionManager, recording the exact Add/Remove call order so the
// reloader's decision table can be asserted end to end.
type recordingConnMgr struct {
	mockToolkit
	has       bool  // value returned by HasConnection
	addErr    error // error returned by AddConnection
	removeErr error // error returned by RemoveConnection

	events     []string         // "remove:<name>" / "add:<name>" in call order
	addConfigs []map[string]any // config passed to each AddConnection
}

func (m *recordingConnMgr) HasConnection(string) bool { return m.has }

func (m *recordingConnMgr) AddConnection(name string, config map[string]any) error {
	m.events = append(m.events, "add:"+name)
	m.addConfigs = append(m.addConfigs, config)
	return m.addErr
}

func (m *recordingConnMgr) RemoveConnection(name string) error {
	m.events = append(m.events, "remove:"+name)
	return m.removeErr
}

// configurableConnStore returns a fixed (instance, error) pair from Get so the
// reloader's read-outcome branches can be driven directly.
type configurableConnStore struct {
	inst *ConnectionInstance
	err  error
}

func (configurableConnStore) List(context.Context) ([]ConnectionInstance, error) { return nil, nil }
func (s configurableConnStore) Get(context.Context, string, string) (*ConnectionInstance, error) {
	return s.inst, s.err
}
func (configurableConnStore) Set(context.Context, ConnectionInstance) error { return nil }
func (configurableConnStore) Delete(context.Context, string, string) error  { return nil }
func (configurableConnStore) Persistent() bool                              { return false }

func TestReloadConnectionLocal(t *testing.T) {
	const kind, name = "api", "c1"
	newCfg := map[string]any{"base_url": "https://new"}

	tests := []struct {
		name       string
		inst       *ConnectionInstance
		getErr     error
		has        bool // HasConnection reported by the toolkit
		removeErr  error
		addErr     error
		wantEvents []string
		wantAddCfg map[string]any // asserted when a single Add is expected
		wantLevel  string         // "", "level=WARN", or "level=ERROR"
	}{
		{
			name:       "store read error keeps live config untouched",
			getErr:     errors.New("db unavailable"),
			has:        true, // present, yet must not be touched
			wantEvents: nil,
			wantLevel:  "level=ERROR",
		},
		{
			name:       "deleted connection is removed from the toolkit",
			getErr:     ErrConnectionNotFound,
			has:        true,
			wantEvents: []string{"remove:c1"},
		},
		{
			name:       "deleted connection absent from toolkit is a no-op",
			getErr:     ErrConnectionNotFound,
			has:        false,
			wantEvents: nil,
		},
		{
			name:       "deleted connection remove error is logged at WARN",
			getErr:     ErrConnectionNotFound,
			has:        true,
			removeErr:  errors.New("boom"),
			wantEvents: []string{"remove:c1"},
			wantLevel:  "level=WARN",
		},
		{
			name:       "created connection is added without a prior remove",
			inst:       &ConnectionInstance{Kind: kind, Name: name, Config: newCfg},
			has:        false,
			wantEvents: []string{"add:c1"},
			wantAddCfg: newCfg,
		},
		{
			name:       "updated connection is removed then re-added with the new config",
			inst:       &ConnectionInstance{Kind: kind, Name: name, Config: newCfg},
			has:        true,
			wantEvents: []string{"remove:c1", "add:c1"},
			wantAddCfg: newCfg,
		},
		{
			name:       "defensive nil instance with no error is treated as deletion",
			inst:       nil,
			getErr:     nil,
			has:        true,
			wantEvents: []string{"remove:c1"},
		},
		{
			name:       "add error is logged at ERROR",
			inst:       &ConnectionInstance{Kind: kind, Name: name, Config: newCfg},
			has:        false,
			addErr:     errors.New("dial failed"),
			wantEvents: []string{"add:c1"},
			wantLevel:  "level=ERROR",
		},
		{
			name:       "remove error during update aborts the add for that toolkit",
			inst:       &ConnectionInstance{Kind: kind, Name: name, Config: newCfg},
			has:        true,
			removeErr:  errors.New("stuck"),
			wantEvents: []string{"remove:c1"},
			wantLevel:  "level=ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tk := &recordingConnMgr{
				mockToolkit: mockToolkit{kind: kind},
				has:         tt.has,
				removeErr:   tt.removeErr,
				addErr:      tt.addErr,
			}
			reg := registry.NewRegistry()
			if err := reg.Register(tk); err != nil {
				t.Fatalf("register toolkit: %v", err)
			}

			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
			defer slog.SetDefault(prev)

			p := &Platform{
				toolkitRegistry: reg,
				connectionStore: configurableConnStore{inst: tt.inst, err: tt.getErr},
			}
			// The table drives the ReloadUpsert (read-and-decide) path; the
			// ReloadDelete short-circuit is covered separately below.
			p.reloadConnectionLocal(kind, name, ReloadUpsert.String())

			if !slices.Equal(tk.events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", tk.events, tt.wantEvents)
			}
			if tt.wantAddCfg != nil {
				if len(tk.addConfigs) != 1 {
					t.Fatalf("expected exactly one AddConnection, got %d", len(tk.addConfigs))
				}
				if got := tk.addConfigs[0]["base_url"]; got != tt.wantAddCfg["base_url"] {
					t.Errorf("add config base_url = %v, want %v", got, tt.wantAddCfg["base_url"])
				}
			}

			logs := buf.String()
			switch tt.wantLevel {
			case "":
				if bytes.Contains(buf.Bytes(), []byte("level=WARN")) ||
					bytes.Contains(buf.Bytes(), []byte("level=ERROR")) {
					t.Errorf("expected no WARN/ERROR logs, got: %q", logs)
				}
			default:
				if !bytes.Contains(buf.Bytes(), []byte(tt.wantLevel)) {
					t.Errorf("expected a %s log line, got: %q", tt.wantLevel, logs)
				}
			}
		})
	}
}

// TestReloadConnectionLocal_MultipleToolkitsSameKind pins the acceptance
// criterion that a failure on one toolkit does not stop other toolkits of the
// same kind from receiving their update.
func TestReloadConnectionLocal_MultipleToolkitsSameKind(t *testing.T) {
	const kind, name = "api", "c1"
	cfg := map[string]any{"base_url": "https://new"}

	failing := &recordingConnMgr{
		mockToolkit: mockToolkit{kind: kind, name: "failing"},
		addErr:      errors.New("boom"),
	}
	healthy := &recordingConnMgr{
		mockToolkit: mockToolkit{kind: kind, name: "healthy"},
	}
	reg := registry.NewRegistry()
	if err := reg.Register(failing); err != nil {
		t.Fatalf("register failing toolkit: %v", err)
	}
	if err := reg.Register(healthy); err != nil {
		t.Fatalf("register healthy toolkit: %v", err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	p := &Platform{
		toolkitRegistry: reg,
		connectionStore: configurableConnStore{
			inst: &ConnectionInstance{Kind: kind, Name: name, Config: cfg},
		},
	}
	p.reloadConnectionLocal(kind, name, ReloadUpsert.String())

	if !slices.Equal(failing.events, []string{"add:c1"}) {
		t.Errorf("failing toolkit events = %v, want [add:c1]", failing.events)
	}
	if !slices.Equal(healthy.events, []string{"add:c1"}) {
		t.Errorf("healthy toolkit did not receive its add despite the other toolkit failing: %v", healthy.events)
	}
	if !bytes.Contains(buf.Bytes(), []byte("level=ERROR")) {
		t.Errorf("expected the failing add to be logged at ERROR, got: %q", buf.String())
	}
}

// TestReloadConnectionLocal_NotFoundSemantics pins the real store's not-found
// convention (a sentinel error, not (nil, nil)) and confirms an upsert reload
// whose store read races a concurrent delete routes to removal rather than the
// keep-live error branch.
func TestReloadConnectionLocal_NotFoundSemantics(t *testing.T) {
	const kind, name = "api", "c1"

	// The real (Noop) store signals not-found with the ErrConnectionNotFound
	// sentinel and a nil instance.
	_, err := (&NoopConnectionStore{}).Get(context.Background(), kind, name)
	if !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("store not-found convention changed: got %v, want ErrConnectionNotFound", err)
	}

	tk := &recordingConnMgr{mockToolkit: mockToolkit{kind: kind}, has: true}
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	p := &Platform{
		toolkitRegistry: reg,
		connectionStore: &NoopConnectionStore{},
	}
	p.reloadConnectionLocal(kind, name, ReloadUpsert.String())

	if !slices.Equal(tk.events, []string{"remove:c1"}) {
		t.Errorf("not-found sentinel was not routed to the deletion branch: events = %v", tk.events)
	}
}

// storeReadForbidden is a ConnectionStore whose Get fails the test if called.
// It proves the ReloadDelete path removes a connection without any store read,
// which is what closes the #885 gap where a transient read failure on a
// one-shot delete event could leave a deleted connection live.
type storeReadForbidden struct{ t *testing.T }

func (storeReadForbidden) List(context.Context) ([]ConnectionInstance, error) {
	return nil, nil
}

func (s storeReadForbidden) Get(context.Context, string, string) (*ConnectionInstance, error) {
	s.t.Helper()
	s.t.Fatal("ReloadDelete must not read the connection store")
	return nil, ErrConnectionNotFound // unreachable; satisfies the signature
}
func (storeReadForbidden) Set(context.Context, ConnectionInstance) error { return nil }
func (storeReadForbidden) Delete(context.Context, string, string) error  { return nil }
func (storeReadForbidden) Persistent() bool                              { return false }

// TestReloadConnectionLocal_DeleteOpSkipsStoreRead proves a ReloadDelete event
// removes the connection from live toolkits without touching the store, so a
// store outage on the peer cannot leave a deleted connection callable (#885).
func TestReloadConnectionLocal_DeleteOpSkipsStoreRead(t *testing.T) {
	const kind, name = "api", "c1"

	tk := &recordingConnMgr{mockToolkit: mockToolkit{kind: kind}, has: true}
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	p := &Platform{
		toolkitRegistry: reg,
		connectionStore: storeReadForbidden{t: t}, // Get would fail the test
	}
	p.reloadConnectionLocal(kind, name, ReloadDelete.String())

	if !slices.Equal(tk.events, []string{"remove:c1"}) {
		t.Errorf("ReloadDelete did not remove without a store read: events = %v", tk.events)
	}
}

// TestReloadConnectionLocal_DeleteOpRemoveFailureWarns proves a failed removal
// on the delete path is logged at WARN (not ERROR): the connection is gone from
// the store, and a toolkit that could not drop it is a degraded-but-benign
// state, not a corruption.
func TestReloadConnectionLocal_DeleteOpRemoveFailureWarns(t *testing.T) {
	const kind, name = "api", "c1"

	tk := &recordingConnMgr{
		mockToolkit: mockToolkit{kind: kind},
		has:         true,
		removeErr:   errors.New("boom"),
	}
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	p := &Platform{
		toolkitRegistry: reg,
		connectionStore: storeReadForbidden{t: t},
	}
	p.reloadConnectionLocal(kind, name, ReloadDelete.String())

	if !slices.Equal(tk.events, []string{"remove:c1"}) {
		t.Errorf("events = %v, want [remove:c1]", tk.events)
	}
	if !bytes.Contains(buf.Bytes(), []byte("level=WARN")) {
		t.Errorf("expected a WARN log for the failed delete-path removal, got: %q", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("level=ERROR")) {
		t.Errorf("delete-path removal failure must not log at ERROR, got: %q", buf.String())
	}
}
