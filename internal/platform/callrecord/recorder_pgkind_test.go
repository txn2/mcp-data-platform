package callrecord

import (
	"context"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// A postgres call is a SQL record (#2400): its statement is the sql it ran,
// or a statements list in order, and never a bound value, which the audit row
// the recorder reads no longer holds.
func TestRecorderCatalogsAPostgresStatementsList(t *testing.T) {
	t.Parallel()
	inner, store := &fakeAudit{}, &fakeStore{}
	rec := NewRecorder(inner, store, testURN, Exclusion{})
	for _, ev := range []audit.Event{
		{ID: "pg-1", ToolName: "postgres_query", Parameters: map[string]any{"sql": "SELECT 1", "params": map[string]any{"$1": "string"}}},
		{ID: "pg-2", ToolName: "postgres_execute", Parameters: map[string]any{"statements": []any{
			map[string]any{"sql": " INSERT INTO a VALUES ($1); ", "params": map[string]any{"$1": "string"}},
			map[string]any{"sql": "DELETE FROM b"},
			map[string]any{"params": map[string]any{}},
		}}},
	} {
		ev.Timestamp, ev.Connection, ev.ToolkitKind, ev.Success = time.Now(), "orders", "postgres", true
		if err := rec.Log(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.inserted) != 2 {
		t.Fatalf("records = %d, want 2", len(store.inserted))
	}
	if got := store.inserted[0]; got.Kind != KindSQL || got.Statement != "SELECT 1" {
		t.Errorf("postgres_query record = %+v", got)
	}
	if got := store.inserted[1].Statement; got != "INSERT INTO a VALUES ($1);\nDELETE FROM b;" {
		t.Errorf("postgres_execute statement = %q", got)
	}
}
