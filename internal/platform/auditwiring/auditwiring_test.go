package auditwiring

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// nilStore is an audit store that records nothing; the composition, not the
// storage, is what this package owns.
type nilStore struct{}

func (nilStore) Log(context.Context, audit.Event) error { return nil }

func (nilStore) Query(context.Context, audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}

func (nilStore) Close() error { return nil }

func TestAnUnassembledLayerAnswersNothing(t *testing.T) {
	t.Parallel()

	// Every write path reads through these accessors, including on a platform
	// built for a test that never assembled a layer. They must answer rather
	// than panic.
	var layer *Layer
	if layer.Logger() != nil || layer.Store() != nil || layer.Calls() != nil || layer.Capturer() != nil {
		t.Error("a nil layer must answer nothing rather than something")
	}
	// Capture is nil-safe on the capturer itself, so a write path may call it
	// unconditionally.
	if got := layer.Capturer().Capture(context.Background(), portal.ProvenanceRequest{Tool: "save_asset"}); got.Tool != "save_asset" {
		t.Errorf("capture = %+v, want the request's own tool", got)
	}
}

func TestInjectedLayerCarriesOnlyTheLogger(t *testing.T) {
	t.Parallel()

	logger := middleware.NoopAuditLogger{}
	layer := Injected(&logger)

	if layer.Logger() == nil {
		t.Error("an injected logger must be reachable")
	}
	// A deployment that supplied its own logger owns the storage, so nothing
	// derived from the audit log is available.
	if layer.Store() != nil || layer.Calls() != nil || layer.Capturer() != nil {
		t.Error("an injected logger carries no store, catalog, or capturer")
	}
}

func TestNewLoggerSelectsTheDeliveryMode(t *testing.T) {
	t.Parallel()

	async := NewLogger(nilStore{}, false, nil)
	if AsFlusher(async) == nil {
		t.Error("the async writer buffers, so it must be offered as a flush barrier")
	}
	// The sync adapter exposes the same method, and its flush is a no-op:
	// what it would wait for is already stored.
	if f := AsFlusher(NewLogger(nilStore{}, true, nil)); f != nil {
		if err := f.Flush(context.Background()); err != nil {
			t.Errorf("a sync writer's flush must be a no-op, got %v", err)
		}
	}
	if AsFlusher(&middleware.NoopAuditLogger{}) != nil {
		t.Error("a logger that cannot flush must not be offered as one")
	}
}

func TestProvenQueriesWithoutACatalogIsNotOffered(t *testing.T) {
	t.Parallel()

	// A nil lister leaves a describe carrying only what the catalog knows,
	// rather than one that answers nothing on every call.
	if ProvenQueries(nil) != nil {
		t.Error("no catalog means no lister")
	}
}

func TestAssembleComposesTheWholeLayer(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	layer := Assemble(Config{DB: db, RetentionDays: 30})

	// The composition order is the point: the catalog decorator sits inside
	// the delivery writer, and the capturer waits on that writer.
	if layer.Store() == nil || layer.Calls() == nil || layer.Logger() == nil || layer.Capturer() == nil {
		t.Fatalf("layer = %+v, want every member assembled", layer)
	}
	if AsFlusher(layer.Logger()) == nil {
		t.Error("the capturer's flush barrier is the assembled logger")
	}
}

func TestProvenQueriesReportsNothingOnAnUnreadableCatalog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("FROM call_records").WillReturnError(errors.New("catalog down"))

	lister := ProvenQueries(callrecord.NewPostgresStore(db, callrecord.Config{}))
	// Best-effort: an unreadable catalog costs the reader the proven queries,
	// never the schema they asked for.
	if got := lister(context.Background(), "urn:li:dataset:(x,y,PROD)", "u1", 3); got != nil {
		t.Errorf("proven queries = %+v, want none", got)
	}
}

func TestProvenQueriesRendersWhatARecordSays(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cols := []string{
		"id", "event_id", "kind", "tool_name", "connection",
		"statement", "method", "path", "operation_id", "targets",
		"purpose", "user_id", "user_email", "session_id", "persona",
		"success", "error_message", "duration_ms", "response_chars",
		"promoted_urn", "promoted_at", "promoted_by",
		"rejected_at", "rejected_by", "rejection_note", "created_at",
		"satisfied_by", "superseded", "reuse_count", "outcome",
	}
	mock.ExpectQuery("FROM call_records").WillReturnRows(sqlmock.NewRows(cols).AddRow(
		"call-1", "evt-1", "sql", "trino_query", "acme",
		"SELECT 1", "", "", "", []byte("[]"),
		"Sizing revenue.", "u1", "u1@example.com", "dps_abc", "analyst",
		true, "", int64(12), 100,
		"urn:li:query:x", nil, "reviewer",
		nil, "", "", time.Now(),
		"capture", false, 4, "satisfied",
	))

	got := ProvenQueries(callrecord.NewPostgresStore(db, callrecord.Config{}))(context.Background(), "urn:li:dataset:(x,y,PROD)", "u1", 3)
	if len(got) != 1 {
		t.Fatalf("proven queries = %+v, want one", got)
	}
	// The standing is what makes a prior query worth preferring: how it was
	// used, how often it has been re-run, and what it became.
	if got[0].Reference != "mcp:call:evt-1" || got[0].SatisfiedBy != "capture" ||
		got[0].ReuseCount != 4 || got[0].PromotedURN != "urn:li:query:x" {
		t.Errorf("proven query = %+v", got[0])
	}
}

func TestLayerAnswersWhetherItIsRecording(t *testing.T) {
	t.Parallel()

	// Callers ask the layer the question they have, rather than reasoning
	// about which of its members being nil implies the answer.
	var unassembled *Layer
	if unassembled.Recording() || Injected(&middleware.NoopAuditLogger{}).Recording() {
		t.Error("a layer with no store records nothing")
	}

	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if !Assemble(Config{DB: db}).Recording() {
		t.Error("an assembled layer records")
	}
}

func TestLayerCloseDrainsBeforeItClosesTheStore(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	layer := Assemble(Config{DB: db})
	// Close is the shutdown order the assembly requires, owned by the thing
	// that did the assembling: the writer drains THROUGH the store, so the
	// store cannot be closed first.
	if err := layer.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// A layer that was never assembled, and one that only wraps an injected
	// logger, both close without complaint: every shutdown path runs this.
	var unassembled *Layer
	if err := unassembled.Close(); err != nil {
		t.Errorf("Close on a nil layer: %v", err)
	}
	if err := Injected(&middleware.NoopAuditLogger{}).Close(); err != nil {
		t.Errorf("Close on an injected logger: %v", err)
	}
}

func TestAssembleStartsTheCatalogSweeper(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The layer owns the sweeper's lifecycle, so a deployment does not have to
	// remember to start it and shutdown does not have to remember to stop it.
	layer := Assemble(Config{DB: db, CallRetentionDays: 30})
	if layer.Calls() == nil {
		t.Fatal("the catalog must be assembled")
	}
	if err := layer.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
