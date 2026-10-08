package thumbworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func installMetrics(t *testing.T) (m *observability.Metrics, scrape func() string) {
	t.Helper()
	var err error
	m, err = observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	bgloop.SetDefaultMetrics(m)
	t.Cleanup(func() {
		bgloop.SetDefaultMetrics(nil)
		_ = m.Shutdown(context.Background())
	})
	return m, func() string {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
}

func TestUnfinishedReasonAndKind(t *testing.T) {
	if got := unfinishedReason(fmt.Errorf("draw: %w", headless.ErrUnavailable)); got != failRenderer {
		t.Errorf("renderer unavailable read as %q", got)
	}
	if got := unfinishedReason(errors.New("storing the tile: denied")); got != failStorage {
		t.Errorf("storage failure read as %q", got)
	}
	if got := (job{name: "collection abc"}).kind(); got != kindCollection {
		t.Errorf("kind = %q", got)
	}
}

func TestRecordDocumentFailure_CountsUnderTheDrawnFamily(t *testing.T) {
	_, scrape := installMetrics(t)
	recordDocumentFailure(withKind(context.Background(), kindAsset))
	if body := scrape(); !strings.Contains(body, `thumbnail_render_failures_total{kind="asset",reason="document"} 1`) {
		t.Errorf("document failure not counted:\n%s", body)
	}
}

type backlogAssets struct{ AssetWork }

func (backlogAssets) ThumbnailBacklog(context.Context, int) (pending, waiting int64, err error) {
	return 4, 1, nil
}

type backlogCollections struct{ CollectionWork }

func (backlogCollections) CollectionThumbnailBacklog(context.Context) (pending, waiting int64, err error) {
	return 2, 0, nil
}

type backlogScripts struct{ ScriptWork }

func (backlogScripts) Backlog(context.Context, int) (pending, waiting int64, err error) {
	return 0, 0, errors.New("down")
}

func TestRegisterBacklog_ReportsEachFamilyItsStoreCounts(t *testing.T) {
	m, scrape := installMetrics(t)
	w := New(Tuning{}, Deps{Assets: backlogAssets{}, Collections: backlogCollections{}})
	w.registerBacklog(m)
	body := scrape()
	for _, want := range []string{
		`background_queue_items{kind="asset",loop="thumbnail_worker",state="pending"} 4`,
		`background_queue_items{kind="asset",loop="thumbnail_worker",state="waiting"} 1`,
		`background_queue_items{kind="collection",loop="thumbnail_worker",state="pending"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}

	failing, scrapeFailing := installMetrics(t)
	New(Tuning{}, Deps{Scripts: backlogScripts{}}).registerBacklog(failing)
	if strings.Contains(scrapeFailing(), "background_queue_items") {
		t.Error("a failing count reports nothing")
	}
	New(Tuning{}, Deps{}).registerBacklog(failing)
}

// TestCachedBacklog_CountsOnceAMinute: the counts scan whole tables, so a
// reading is kept for backlogMaxAge and every family is counted again only
// after it.
func TestCachedBacklog_CountsOnceAMinute(t *testing.T) {
	var (
		calls atomic.Int32
		down  error
	)
	count := func(context.Context) (int64, int64, error) {
		calls.Add(1)
		return 1, 0, down
	}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sample := cachedBacklog(map[string]func(context.Context) (int64, int64, error){
		kindAsset: count, kindResource: count,
	}, backlogMaxAge, func() time.Time { return clock })

	first, err := sample(context.Background())
	if err != nil || len(first) != 2 || first[0].Kind != kindAsset || first[1].Kind != kindResource {
		t.Fatalf("first reading = %+v, %v", first, err)
	}
	clock = clock.Add(backlogMaxAge - time.Second)
	if _, err := sample(context.Background()); err != nil || calls.Load() != 2 {
		t.Fatalf("a reading inside the minute counted again: %d counts, %v", calls.Load(), err)
	}
	clock = clock.Add(2 * time.Second)
	if _, err := sample(context.Background()); err != nil || calls.Load() != 4 {
		t.Errorf("a reading past the minute did not count again: %d counts, %v", calls.Load(), err)
	}

	// A failed count is returned, and not kept: the next scrape counts again.
	down = errors.New("database unavailable")
	clock = clock.Add(backlogMaxAge)
	if got, err := sample(context.Background()); err == nil || got != nil {
		t.Errorf("a failed count reported %+v, %v", got, err)
	}
	down = nil
	if _, err := sample(context.Background()); err != nil {
		t.Errorf("the reading after a failure was not counted again: %v", err)
	}
}
