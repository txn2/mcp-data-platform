package webhookwire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestRegisterCompactionGauge_ReportsTheBacklog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery(regexp.QuoteMeta("FROM webhook_windows")).
		WillReturnRows(sqlmock.NewRows([]string{"p", "r", "w", "o"}).AddRow(6, 0, 2, 30.0))
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()
	registerCompactionGauge(m, whstore.New(db))
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body := rec.Body.String()
	for _, want := range []string{
		`background_queue_items{loop="webhook_compactor",state="pending"} 6`,
		`background_queue_items{loop="webhook_compactor",state="waiting"} 2`,
		`background_queue_oldest_age_seconds{loop="webhook_compactor"} 30`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}

	failing, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = failing.Shutdown(context.Background()) }()
	mock.ExpectQuery(regexp.QuoteMeta("FROM webhook_windows")).WillReturnError(context.DeadlineExceeded)
	registerCompactionGauge(failing, whstore.New(db))
	rec = httptest.NewRecorder()
	failing.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	if strings.Contains(rec.Body.String(), "background_queue_items") {
		t.Error("a failed read reports no backlog")
	}
}

// TestStorageGaugeIsReadOffTheScrape: a scrape answers from the last read of
// each source's storage (#2150); a read older than storageEvery starts a new
// one in the background, never more than one at a time, and a failed read
// keeps the last.
func TestStorageGaugeIsReadOffTheScrape(t *testing.T) {
	reads := 0
	var fail error
	clock := time.Unix(1000, 0)
	c := &storageCache{
		read: func(context.Context, string) ([]whstore.SourceStorage, error) {
			reads++
			return []whstore.SourceStorage{{Source: "esp", RawBytes: 900, CompactedBytes: 500}}, fail
		},
		now: func() time.Time { return clock },
	}
	ctx := context.Background()
	c.refresh(ctx)
	got, err := c.sample(ctx)
	if err != nil || len(got) != 1 || got[0].RawBytes != 900 {
		t.Fatalf("after a read the scrape answers from it: %v %v", got, err)
	}
	if c.reading {
		t.Fatal("a fresh read is not read again")
	}

	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()
	m.RegisterWebhookStorage(c.sample)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/metrics", http.NoBody))
	for _, want := range []string{`webhook_source_bytes{kind="raw",source="esp"} 900`, `webhook_source_bytes{kind="compacted",source="esp"} 500`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %s", want)
		}
	}

	c.mu.Lock()
	c.reading = true // a read already running
	c.mu.Unlock()
	clock = clock.Add(storageEvery)
	_, _ = c.sample(ctx)
	fail = errors.New("db down")
	c.refresh(ctx)
	c.mu.Lock()
	kept, reading := c.samples, c.reading
	c.mu.Unlock()
	if len(kept) != 1 || reads != 2 || reading {
		t.Fatalf("a failed read keeps the last one: %v after %d reads (reading %v)", kept, reads, reading)
	}
}

func TestRegisterStorageGaugeInstallsTheCache(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()                           //nolint:errcheck // test cleanup
	registerStorageGauge(nil, whstore.New(db)) // nil metrics: nothing to install on
}
