package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// TestIndexJobsSummary_RealAssembly wires the real index-jobs stack end
// to end — a real indexjobs.Reporter over a real Registry, a real
// catalogindex.Sink (backed by a catalog.MemoryStore for coverage), and
// a sqlmock-backed indexjobs.PostgresStore for the job-state counts —
// behind the real admin.Handler, then drives the HTTP endpoint and
// asserts the assembled JSON. A unit test that hand-builds the response
// would not prove that Registry.Kinds, Store.Counts (real SQL), and
// Sink.Coverage (real store) actually reach the handler through the
// Reporter; this does.
func TestIndexJobsSummary_RealAssembly(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Real catalog store seeded so the catalog Sink's coverage query
	// returns a real indexed/expected ratio.
	ctx := context.Background()
	cat := catalog.NewMemoryStore()
	if err := cat.CreateCatalog(ctx, catalog.Catalog{ID: "c", Name: "c", DisplayName: "c"}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := cat.UpsertSpec(ctx, "c", catalog.SpecEntry{
		SpecName: "v1", Content: "x", SourceKind: catalog.SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	if err := cat.SetOperationCount(ctx, "c", "v1", 2); err != nil {
		t.Fatalf("SetOperationCount: %v", err)
	}
	if err := cat.UpsertOperationEmbeddings(ctx, "c", "v1", []catalog.OperationEmbedding{
		{OperationID: "op1", TextHash: []byte("h"), Embedding: []float32{1}, Dim: 1},
	}); err != nil {
		t.Fatalf("UpsertOperationEmbeddings: %v", err)
	}

	store := indexjobs.NewPostgresStore(db)
	reg := indexjobs.NewRegistry()
	if err := reg.Register(stubCatalogSource{}, catalogindex.NewSink(cat)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reporter := indexjobs.NewReporter(store, reg)

	// Counts query for the single registered kind (4 state counts, the
	// MAX last-activity aggregate, and the unresolved-failure count).
	activity := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("WITH last AS").
		WithArgs(catalogindex.SourceKind).
		WillReturnRows(sqlmock.NewRows([]string{"pending", "running", "succeeded", "failed", "last_activity", "unresolved_failures"}).
			AddRow(1, 0, 3, 2, activity, 2))

	h := NewHandler(Deps{
		IndexJobs: reporter,
		Embedder:  &stubProvider{kind: "ollama", model: "nomic-embed-text", dim: 768},
	}, nil)

	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body %s", res.Code, res.Body.String())
	}
	var got indexJobsSummaryResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Provider.Status != embeddingStatusOK {
		t.Errorf("provider status = %q; want ok", got.Provider.Status)
	}
	if len(got.Kinds) != 1 {
		t.Fatalf("kinds = %d; want 1", len(got.Kinds))
	}
	k := got.Kinds[0]
	if k.Kind != catalogindex.SourceKind {
		t.Errorf("kind = %q; want %q", k.Kind, catalogindex.SourceKind)
	}
	if k.Pending != 1 || k.Succeeded != 3 || k.Failed != 2 {
		t.Errorf("counts = %+v; want pending 1 succeeded 3 failed 2", k)
	}
	if k.UnresolvedFailures != 2 {
		t.Errorf("unresolved_failures = %d; want 2", k.UnresolvedFailures)
	}
	// pending > 0 means work is in flight, which takes priority over the
	// open failures: the verdict the real DeriveVerdict produces is
	// "indexing", computed from the same counts the response carries.
	if k.Verdict != string(indexjobs.VerdictIndexing) {
		t.Errorf("verdict = %q; want %q", k.Verdict, indexjobs.VerdictIndexing)
	}
	if k.Coverage == nil || k.Coverage.Indexed != 1 || k.Coverage.Expected != 2 || !k.Coverage.ExpectedKnown {
		t.Errorf("coverage = %+v; want indexed 1 expected 2 known true", k.Coverage)
	}
	if k.LastActivity == nil || *k.LastActivity != activity.Format(time.RFC3339) {
		t.Errorf("last_activity = %v; want %s", k.LastActivity, activity.Format(time.RFC3339))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// stubCatalogSource is a minimal indexjobs.Source for the api_catalog
// kind: the Registry needs a Source paired with the real Sink, but this
// test exercises only the read path, so LoadItems is never called.
type stubCatalogSource struct{}

func (stubCatalogSource) Kind() string { return catalogindex.SourceKind }
func (stubCatalogSource) LoadItems(context.Context, string) ([]indexjobs.Item, error) {
	return nil, nil
}
func (stubCatalogSource) OnSucceeded(string) {}
