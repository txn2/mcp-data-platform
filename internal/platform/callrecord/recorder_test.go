package callrecord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// fakeStore records what the recorder asked of the catalog.
type fakeStore struct {
	Store
	inserted  []Record
	credited  []Record
	insertErr error
	creditErr error
}

func (f *fakeStore) Insert(_ context.Context, r Record) error {
	f.inserted = append(f.inserted, r)
	return f.insertErr
}

func (f *fakeStore) CreditReuse(_ context.Context, r Record) (int, error) {
	f.credited = append(f.credited, r)
	return 0, f.creditErr
}

// fakeAudit stands in for the audit store the recorder decorates.
type fakeAudit struct {
	logged []audit.Event
	err    error
	closed bool
}

func (f *fakeAudit) Log(_ context.Context, ev audit.Event) error {
	f.logged = append(f.logged, ev)
	return f.err
}

func (*fakeAudit) Query(context.Context, audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}

func (f *fakeAudit) Close() error {
	f.closed = true
	return nil
}

// testURN is the URN builder the recorder is given: a stand-in for the
// platform's connection-aware one.
// testURN stands in for the platform's builder. It puts the connection KIND in
// the platform segment, which is what the real one resolves it from, so a test
// asserting on a target is asserting the kind reached the builder (#1384).
func testURN(connectionKind, _, catalog, schema, table string) string {
	return "urn:li:dataset:(urn:li:dataPlatform:" + connectionKind + "," + catalog + "." + schema + "." + table + ",PROD)"
}

func TestRecorderCatalogsAQuery(t *testing.T) {
	t.Parallel()

	inner, store := &fakeAudit{}, &fakeStore{}
	rec := NewRecorder(inner, store, testURN)

	err := rec.Log(context.Background(), audit.Event{
		ID:         "evt-1",
		Timestamp:  time.Now(),
		ToolName:   "trino_query",
		Connection: "acme",
		Purpose:    "Sizing Q3 revenue by region.",
		UserID:     "u1",
		SessionID:  "dps_abc",
		Success:    true,
		DurationMS: 143,
		Parameters: map[string]any{
			"sql": "SELECT region FROM iceberg.sales.orders JOIN iceberg.sales.regions ON true",
		},
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	if len(inner.logged) != 1 {
		t.Fatalf("the audit row must still be written, got %d", len(inner.logged))
	}
	if len(store.inserted) != 1 {
		t.Fatalf("expected one record, got %d", len(store.inserted))
	}
	got := store.inserted[0]
	if got.Kind != KindSQL || got.EventID != "evt-1" || got.Purpose == "" {
		t.Errorf("record not built from the event: %+v", got)
	}
	// Both tables a join reads are targets: this is what supersession and the
	// per-table enrichment both key on.
	if len(got.Targets) != 2 {
		t.Errorf("targets = %v, want both joined tables", got.Targets)
	}
	if len(store.credited) != 1 {
		t.Error("a recorded call must be offered for reuse crediting")
	}
}

func TestRecorderCatalogsAnAPICall(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	rec := NewRecorder(&fakeAudit{}, store, testURN)

	if err := rec.Log(context.Background(), audit.Event{
		ID:         "evt-2",
		ToolName:   "api_invoke_endpoint",
		Connection: "acme-crm",
		Success:    true,
		Parameters: map[string]any{"method": "get", "path": "/v1/orders", "operation_id": "listOrders"},
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	got := store.inserted[0]
	if got.Kind != KindAPI || got.Method != "GET" || got.Path != "/v1/orders" {
		t.Errorf("api record not built from the event: %+v", got)
	}
	if len(got.Targets) != 1 || got.Targets[0] != "api:acme-crm:listOrders" {
		t.Errorf("targets = %v, want the endpoint identity", got.Targets)
	}
}

// TestRecorderResolvesPathParamsIntoTheTarget proves the arguments an audit row
// kept reach the target, so two calls that addressed different resources
// through one endpoint are two targets (#1352). Both argument shapes are
// exercised: the typed map a toolkit declares and the map a JSON round trip
// leaves behind.
func TestRecorderResolvesPathParamsIntoTheTarget(t *testing.T) {
	t.Parallel()

	const operation = "POST /admin/scripts/{id}/versions/{version}/approve"
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name: "typed map",
			params: map[string]any{
				"operation_id": operation, "method": "post",
				"path_params": map[string]string{"id": "script-a", "version": "3"},
			},
			want: "api:platform-admin:POST /admin/scripts/script-a/versions/3/approve",
		},
		{
			name: "json round trip",
			params: map[string]any{
				"operation_id": operation, "method": "post",
				"path_params": map[string]any{"id": "script-b", "version": "3"},
			},
			want: "api:platform-admin:POST /admin/scripts/script-b/versions/3/approve",
		},
		{
			name:   "no path params recorded",
			params: map[string]any{"operation_id": operation, "method": "post"},
			want:   "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeStore{}
			rec := NewRecorder(&fakeAudit{}, store, testURN)
			if err := rec.Log(context.Background(), audit.Event{
				ID: "evt-" + c.name, ToolName: "api_invoke_endpoint",
				Connection: "platform-admin", Success: true, Parameters: c.params,
			}); err != nil {
				t.Fatalf("Log: %v", err)
			}
			got := store.inserted[0].Targets
			if c.want == "" {
				if len(got) != 0 {
					t.Errorf("targets = %v, want none: an unresolved template names no resource", got)
				}
				return
			}
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("targets = %v, want [%s]", got, c.want)
			}
		})
	}
}

// TestRecorderTargetsCarryTheConnectionKind proves a recorded call's target
// names the platform the statement actually ran against (#1384). A Trino export
// over a connection whose name is also carried by an s3 connection recorded an
// s3 dataset URN, because the builder resolved the name alone; the kind is on
// the audit event this recorder reads, so it is passed rather than guessed.
func TestRecorderTargetsCarryTheConnectionKind(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	rec := NewRecorder(&fakeAudit{}, store, testURN)

	if err := rec.Log(context.Background(), audit.Event{
		ID: "evt-kind", ToolName: "trino_export", ToolkitKind: "trino",
		Connection: "acme", Success: true,
		Parameters: map[string]any{"sql": "SELECT * FROM warehouse.public.regions"},
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	want := "urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.regions,PROD)"
	got := store.inserted[0].Targets
	if len(got) != 1 || got[0] != want {
		t.Errorf("targets = %v, want [%s]", got, want)
	}
}

func TestRecorderIgnoresEverythingElse(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	rec := NewRecorder(&fakeAudit{}, store, testURN)

	// Discovery, bookkeeping, and a call the platform minted no id for are all
	// outside the catalog: a record of them would be a record of nothing worth
	// running again, or one nothing could ever cite.
	for _, ev := range []audit.Event{
		{ID: "evt-3", ToolName: "trino_describe_table", Success: true},
		{ID: "evt-4", ToolName: "save_asset", Success: true},
		{ID: "", ToolName: "trino_query", Success: true},
	} {
		if err := rec.Log(context.Background(), ev); err != nil {
			t.Fatalf("Log: %v", err)
		}
	}
	if len(store.inserted) != 0 {
		t.Errorf("expected no records, got %+v", store.inserted)
	}
}

func TestRecorderKeepsAFailedCall(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	rec := NewRecorder(&fakeAudit{}, store, testURN)

	if err := rec.Log(context.Background(), audit.Event{
		ID: "evt-5", ToolName: "trino_query", Success: false,
		ErrorMessage: "Table not found", Parameters: map[string]any{"sql": "SELECT 1 FROM nope"},
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	// A failed query is part of how an answer was reached, and the catalog is
	// where an agent sees that a correction followed it.
	if len(store.inserted) != 1 || store.inserted[0].Success {
		t.Fatalf("a failed call must still be cataloged, got %+v", store.inserted)
	}
	if len(store.credited) != 0 {
		t.Error("a failed call re-ran nothing and must not credit reuse")
	}
}

func TestRecorderNeverFailsTheAuditWrite(t *testing.T) {
	t.Parallel()

	// The catalog is derived from the audit log. Losing an entry costs a query
	// its place in the catalog; it must never cost the audit row.
	inner := &fakeAudit{err: errors.New("store down")}
	store := &fakeStore{insertErr: errors.New("catalog down")}
	rec := NewRecorder(inner, store, testURN)

	err := rec.Log(context.Background(), audit.Event{
		ID: "evt-6", ToolName: "trino_query", Success: true,
		Parameters: map[string]any{"sql": "SELECT 1"},
	})
	if err == nil || !strings.Contains(err.Error(), "store down") {
		t.Errorf("the audit error must be returned unchanged, got %v", err)
	}
	if len(store.credited) != 0 {
		t.Error("a record that was not stored must not be credited")
	}
}

func TestRecorderWithoutACatalogIsTheAuditStore(t *testing.T) {
	t.Parallel()

	inner := &fakeAudit{}
	// A deployment with no database gets the audit store back unchanged rather
	// than a decorator that swallows every write into nothing.
	if got := NewRecorder(inner, nil, testURN); got != audit.Logger(inner) {
		t.Errorf("NewRecorder with no catalog = %T, want the audit store itself", got)
	}
}

func TestRecorderPassesThroughQueryAndClose(t *testing.T) {
	t.Parallel()

	inner := &fakeAudit{}
	rec := NewRecorder(inner, &fakeStore{}, testURN)

	if _, err := rec.Query(context.Background(), audit.QueryFilter{}); err != nil {
		t.Errorf("Query: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if !inner.closed {
		t.Error("Close must reach the audit store, which owns the connection")
	}
}

func TestRecorderWithoutAURNBuilderStillRecords(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	rec := NewRecorder(&fakeAudit{}, store, nil)

	if err := rec.Log(context.Background(), audit.Event{
		ID: "evt-7", ToolName: "trino_query", Success: true,
		Purpose: "Counting orders.", Parameters: map[string]any{"sql": "SELECT 1 FROM iceberg.sales.orders"},
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	// Without a URN builder a record carries no targets, which costs it
	// supersession and the per-table enrichment. It is still a record.
	got := store.inserted[0]
	if len(got.Targets) != 0 || got.Statement == "" {
		t.Errorf("record = %+v, want a statement and no targets", got)
	}
}

func TestKindForTool(t *testing.T) {
	t.Parallel()

	if KindForTool("trino_export") != KindSQL || KindForTool("api_export") != KindAPI {
		t.Error("the export tools produce records of their own kind")
	}
	if KindForTool("search") != "" {
		t.Error("discovery is not data access")
	}
}
