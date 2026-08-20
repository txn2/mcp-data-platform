package reflexivecapture

import (
	"context"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

// recordingStore embeds a noop store and records inserts.
type recordingStore struct {
	memory.Store
	mu       sync.Mutex
	inserted []memory.Record
}

func newRecordingStore() *recordingStore {
	return &recordingStore{Store: memory.NewNoopStore()}
}

func (s *recordingStore) Insert(_ context.Context, r memory.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inserted = append(s.inserted, r)
	return nil
}

func TestConfig_IsEnabled(t *testing.T) {
	if !(Config{}).IsEnabled() {
		t.Error("reflexive capture should default to enabled")
	}
	if (Config{Enabled: new(false)}).IsEnabled() {
		t.Error("explicit false should disable")
	}
	if !(Config{Enabled: new(true)}).IsEnabled() {
		t.Error("explicit true should enable")
	}
}

func TestCaptor_CaptureCorrection(t *testing.T) {
	store := newRecordingStore()
	tk, err := memorykit.New("test", store, nil)
	if err != nil {
		t.Fatalf("memorykit.New: %v", err)
	}
	c := &captor{toolkit: tk}

	err = c.CaptureCorrection(context.Background(), middleware.CorrectionCapture{
		SinkClass: memory.SinkSchemaEntity,
		Category:  memory.CategoryCorrection,
		Content:   "A query error was corrected on sales.orders in this session.",
		CreatedBy: "analyst@example.com",
		SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("CaptureCorrection: %v", err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("expected 1 insert, got %d", len(store.inserted))
	}
	if store.inserted[0].Source != memory.SourceAutomation {
		t.Errorf("Source = %q, want automation", store.inserted[0].Source)
	}
}

func TestWire_Gating(t *testing.T) {
	tk, _ := memorykit.New("test", newRecordingStore(), nil)
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)

	// Disabled -> nil tracker.
	if tr := Wire(Deps{Enabled: false, Server: server, Toolkit: tk}); tr != nil {
		t.Error("disabled Wire should return nil")
	}
	// No toolkit -> nil tracker.
	if tr := Wire(Deps{Enabled: true, Server: server, Toolkit: nil}); tr != nil {
		t.Error("Wire without a toolkit should return nil")
	}
	// Enabled with toolkit -> tracker wired.
	tr := Wire(Deps{
		Enabled:           true,
		Server:            server,
		Toolkit:           tk,
		BuildURN:          func(_, _, _, _, _ string) string { return "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)" },
		PersonaAllowsTool: func(context.Context, []string, string) bool { return true },
	})
	if tr == nil {
		t.Fatal("enabled Wire should return a tracker")
	}
	tr.Stop()
}
