//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2664: a site release whose page does not finish drawing
// before the render deadline is recorded as the release's failure on its
// first attempt, not retried as unfinished; the Sites list carries the
// recorded reason on the environment (tile_failure); and the failure is
// counted under thumbnail_render_failures_total{kind="site",reason="document"},
// not reason="storage".
//
// Wire forms: manage_site takes command, site and note as strings and files
// as a JSON array of objects of strings; the portal list takes q as a query
// parameter.

// stuckPage2664 never reports itself drawn: the image in the frame decodes
// with a promise that never settles.
const stuckPage2664 = `<!doctype html><html><head><script>
HTMLImageElement.prototype.decode = function () { return new Promise(function () {}); };
</script></head><body><h1>stuck</h1>
<img width="200" height="100" alt="pixel" src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7">
</body></html>`

// renderFailures2664 sums thumbnail_render_failures_total for kind site and
// reason over every replica.
func renderFailures2664(t *testing.T, reason string) float64 {
	t.Helper()
	var sum float64
	for _, target := range metricsURLs() {
		sum += metricSeries(scrapeOne(t, target), "thumbnail_render_failures_total", map[string]string{"kind": "site", "reason": reason})
	}
	return sum
}

// envOf2664 is an environment as the Sites list shows it.
func envOf2664(t *testing.T, s *site2604, env string) map[string]any {
	t.Helper()
	list := listed2658(t, s.key, "q="+url.QueryEscape(s.name))
	sites, _ := list["sites"].([]any)
	for _, raw := range sites {
		st, _ := raw.(map[string]any)
		if st["name"] != s.name {
			continue
		}
		envs, _ := st["environments"].([]any)
		for _, e := range envs {
			m, _ := e.(map[string]any)
			if m["name"] == env {
				return m
			}
		}
	}
	t.Fatalf("the Sites list has no %s on %s: %v", env, s.name, list["_raw"])
	return nil
}

// TestIssue2664_APageThatMissesTheDeadlineIsTheReleasesFailure: the stuck page
// is recorded on its first attempt with a reason naming the deadline, the
// Sites list carries the reason, and it is counted as the document's.
func TestIssue2664_APageThatMissesTheDeadlineIsTheReleasesFailure(t *testing.T) {
	document, storage := renderFailures2664(t, "document"), renderFailures2664(t, "storage")
	s := newSite2604(t, keyOwner2604, "acc2664-")
	connectAs(t, s.key).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "stuck",
		"files": []map[string]any{{"path": "index.html", "content": stuckPage2664}},
	})

	var failure string
	deadline := time.Now().Add(3 * time.Minute)
	for failure == "" {
		failure, _ = envOf2664(t, s, "dev")["tile_failure"].(string)
		if failure == "" && time.Now().After(deadline) {
			t.Fatal("the Sites list carries no tile failure for dev within three minutes")
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(failure, "deadline") {
		t.Errorf("the recorded failure does not name the deadline: %q", failure)
	}
	db := issue1904DB(t)
	var attempts int
	if err := db.QueryRow(`SELECT t.attempts FROM site_tiles t JOIN sites s ON s.id = t.site_id WHERE s.name = $1 AND t.environment = 'dev'`, s.name).Scan(&attempts); err != nil {
		t.Fatalf("reading the tile's attempts: %v", err)
	}
	if attempts != 1 {
		t.Errorf("the failure was recorded after %d attempts, not on the first", attempts)
	}
	if status, _ := s.rest(t, s.key, http.MethodGet, "/environments/dev/tile", nil); status != http.StatusNotFound {
		t.Errorf("the tile route answers %d for a release that could not be drawn", status)
	}
	if got := renderFailures2664(t, "document"); got < document+1 {
		t.Errorf("thumbnail_render_failures_total{kind=\"site\",reason=\"document\"} = %v, want at least %v", got, document+1)
	}
	if got := renderFailures2664(t, "storage"); got != storage {
		t.Errorf("thumbnail_render_failures_total{kind=\"site\",reason=\"storage\"} moved from %v to %v", storage, got)
	}
}
