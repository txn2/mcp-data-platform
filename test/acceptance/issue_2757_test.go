//go:build integration

package acceptance

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #2757: the collection item sweep (#2752) runs in a process's first
// retention pass. The HTTP layer adds it after the platform has started the
// retention loop, and an added sweep used to wait a whole interval for the
// next pass, so a replica restarted more often than retention.every never ran
// it.
//
// Wire forms: none. The criterion calls no tool and sends no body: it
// restarts both replicas and reads each one's GET /metrics.

const (
	// loop2757 is the collection item sweep's series name.
	loop2757 = "retention_collection_items"
	// built2757 is a sweep the loop is built with, which runs once in every
	// pass: its count is the number of passes the process has run.
	built2757 = "retention_graphql_superseded_embeddings"
)

// TestIssue2757_TheCollectionSweepRunsInTheFirstPassAfterARestart: after both
// replicas restart, each one's first background_loop_iterations_total sample
// for the collection item sweep (run, or skipped under the other replica's
// lock) appears while the sweep the loop was built with has run once, that is,
// within the first pass. Before the fix it first appeared in the second pass,
// a retention.every after start.
func TestIssue2757_TheCollectionSweepRunsInTheFirstPassAfterARestart(t *testing.T) {
	targets := metricsURLs()
	before := map[string]int{}
	for _, target := range targets {
		pid, ok := pid2757(t, target)
		if !ok {
			t.Fatalf("nothing listens at %s; `make dev` starts one metrics endpoint per replica", target)
		}
		before[target] = pid
	}

	restart2757(t)

	for _, target := range targets {
		awaitNewProcess2757(t, target, before[target])
		sweeps, passes := firstSweep2757(t, target)
		if passes > 1 {
			t.Errorf("%s first ran the collection item sweep after %v passes of the built sweeps; want it in the first", target, passes)
		}
		if sweeps < 1 {
			t.Errorf("%s never reported the collection item sweep", target)
		}
	}
}

// restart2757 restarts both replicas: their air processes rebuild and restart
// on a write to a Go source file, written with the bytes it already holds.
func restart2757(t *testing.T) {
	t.Helper()
	src := filepath.Join("..", "..", "cmd", "mcp-data-platform", "main.go")
	body, err := os.ReadFile(src) // #nosec G304 -- a fixed path in this repository
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	if err := os.WriteFile(src, body, 0o644); err != nil { // #nosec G306 -- the file's own mode in the checkout
		t.Fatalf("rewriting %s to restart the replicas: %v", src, err)
	}
}

// awaitNewProcess2757 waits, to a bound, until a process other than old
// listens at target and answers a scrape.
func awaitNewProcess2757(t *testing.T, target string, old int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for {
		if pid, ok := pid2757(t, target); ok && pid != old && scrapes2757(t, target) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no new process answered at %s within 4 minutes of the restart", target)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// firstSweep2757 polls target until the collection item sweep's iterations
// appear, and returns them with the built sweep's iterations from the same
// scrape. It gives up, returning what it read, once the built sweep has run
// twice without the collection sweep.
func firstSweep2757(t *testing.T, target string) (sweeps, passes float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		body := scrapeOne(t, target)
		sweeps = metricSeries(body, "background_loop_iterations_total", map[string]string{"loop": loop2757})
		passes = metricSeries(body, "background_loop_iterations_total", map[string]string{"loop": built2757})
		if sweeps > 0 || passes > 1 || time.Now().After(deadline) {
			return sweeps, passes
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// scrapes2757 reports whether target answers a scrape now.
func scrapes2757(t *testing.T, target string) bool {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// pid2757 names the process listening on target's port, if one is.
func pid2757(t *testing.T, target string) (int, bool) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parsing %s: %v", target, err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("%s names no port: %v", target, err)
	}
	out, err := exec.CommandContext(t.Context(), "lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t").Output() // #nosec G204 -- a port the suite names
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0]))
	return pid, err == nil
}
