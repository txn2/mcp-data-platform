package indexqueue

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

type fakeDepth struct {
	mu    sync.Mutex
	rows  []indexjobs.QueueDepth
	err   error
	reads int
}

func (f *fakeDepth) QueueDepth(context.Context) ([]indexjobs.QueueDepth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.rows, f.err
}

type fakeCoverage struct {
	kinds []string
	cov   map[string]*indexjobs.Coverage
	errs  map[string]error
}

func (f *fakeCoverage) Kinds() []string { return f.kinds }

func (f *fakeCoverage) Coverage(_ context.Context, kind string) (*indexjobs.Coverage, error) {
	return f.cov[kind], f.errs[kind]
}

// TestQueueSampler_OneSamplePerKind: every registered kind is sampled, the one
// with no open work as zeros, the one whose coverage read failed without
// coverage, and the one whose Sink reports none without it either.
func TestQueueSampler_OneSamplePerKind(t *testing.T) {
	t.Parallel()
	depth := &fakeDepth{rows: []indexjobs.QueueDepth{{
		SourceKind: "calls", Pending: 2558, Running: 2, Retrying: 7, FailedUnits: 1,
		OldestRunnableWait: time.Minute,
	}}}
	cov := &fakeCoverage{
		kinds: []string{"calls", "resources", "tools", "memory"},
		cov: map[string]*indexjobs.Coverage{
			"calls": {Indexed: 833090, Expected: 835775, ExpectedKnown: true},
			"tools": {Indexed: 140},
		},
		errs: map[string]error{"resources": errors.New("vector table gone")},
	}

	got, err := newQueueSampler(depth, cov).Sample(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []observability.IndexQueueSample{
		{
			Kind: "calls", Pending: 2558, Running: 2, Retrying: 7, FailedUnits: 1,
			OldestRunnableWait: time.Minute,
			CoverageKnown:      true, Indexed: 833090, Expected: 835775, ExpectedKnown: true,
		},
		{Kind: "resources"},
		{Kind: "tools", CoverageKnown: true, Indexed: 140},
		{Kind: "memory"},
	}, got)
}

// TestQueueSampler_CachesForTheTTL: scrapes inside the TTL are answered from
// one read; the first scrape after it reads again; a failed read is an error
// and the next scrape tries again.
func TestQueueSampler_CachesForTheTTL(t *testing.T) {
	t.Parallel()
	depth := &fakeDepth{}
	s := newQueueSampler(depth, &fakeCoverage{kinds: []string{"calls"}})
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	_, err := s.Sample(ctx)
	require.NoError(t, err)
	_, err = s.Sample(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, depth.reads, "a scrape inside the TTL reads nothing")

	now = now.Add(sampleTTL)
	depth.err = errors.New("db down")
	_, err = s.Sample(ctx)
	require.Error(t, err)
	assert.Equal(t, 2, depth.reads)

	depth.err = nil
	_, err = s.Sample(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, depth.reads, "a failed read is retried on the next scrape")
}

// TestNew_WiresTheQueueIntoMetrics assembles the queue as the platform does,
// with metrics on, and reads a real scrape: an Enqueue through the assembled
// store reaches the enqueue counter, and the scrape's database gauges come
// from the store's QueueDepth read through the registered sampler (#1837).
func TestNew_WiresTheQueueIntoMetrics(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.MatchExpectationsInOrder(false)

	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	defer m.Shutdown(context.Background()) //nolint:errcheck // test cleanup

	h := New(Config{DB: db, Embedder: testEmbedder(), ModelName: "m", Metrics: m})
	require.NotNil(t, h)

	mock.ExpectQuery("INSERT INTO index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))
	_, err = h.store.Enqueue(context.Background(),
		indexjobs.Key{SourceKind: toolsindex.SourceKind, SourceID: toolsindex.SourceID}, indexjobs.TriggerWrite)
	require.NoError(t, err)

	mock.ExpectQuery("GROUP BY source_kind").WillReturnRows(sqlmock.NewRows(
		[]string{"source_kind", "pending", "running", "retrying", "failed_units", "wait"}).
		AddRow(toolsindex.SourceKind, 1, 0, 0, 0, 0.0))

	body := scrape(t, m)
	for _, want := range []string{
		`indexjob_enqueued_total{kind="tools",result="created",trigger="write"} 1`,
		`indexjob_queue_jobs{kind="tools",state="pending"} 1`,
	} {
		assert.Contains(t, body, want)
	}
}

func scrape(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}
