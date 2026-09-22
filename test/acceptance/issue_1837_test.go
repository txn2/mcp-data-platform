//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// Issue #1837: three portal figures described something other than what they
// were labelled as.
//
//   - A Resources folder view counted, and paged through, every file beneath
//     the folder while showing only the ones directly in it.
//   - The Indexing dashboard's Throughput, Embed latency, In flight and Retry
//     backoff panels were computed from the newest 500 job rows, which during
//     a backlog are all pending, so all four went blank.
//   - A kind's coverage rounded 99.68% up to a green "100%".
//
// The first two are criteria about what the portal's routes answer, and are
// checked here against the running stack. Throughput and Embed latency are read
// from Prometheus through the platform's PromQL proxy, with the queries the
// dashboard sends (ui/src/pages/indexing/metrics.ts). The coverage figure is a
// rendering rule over counts the summary already reported exactly, and is
// checked on the portal page (build/1837/acceptance.md).

// unique1837 names one run of this file so a re-run does not collide with what
// the last one left behind.
func unique1837() string {
	return strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 10)
}

// createAt1837 files a markdown resource at path in the caller's own library
// and returns its id, deleting it when the test ends.
func createAt1837(t *testing.T, c *client, path, name string) string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     name + ".md",
		"display_name": name,
		"path":         path,
		"description":  "Acceptance #1837: a file filed at one folder level.",
		"content":      "# " + name + "\n",
		"content_type": "text/markdown",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id
}

// listing1837 returns the ids and the total a resource listing answered with.
func listing1837(t *testing.T, c *client, query string) (ids []string, total int, raw any) {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources?"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/resources?%s: status %d: %v", query, status, body)
	}
	list, _ := body["resources"].([]any)
	for _, item := range list {
		if r, ok := item.(map[string]any); ok {
			if id, _ := r["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	n, _ := body["total"].(float64)
	return ids, int(n), body["resources"]
}

// TestIssue1837_AFolderListsItsOwnFiles is criterion 1: a folder whose own
// files fit one page returns all of them, and the total it reports is their
// number, not the subtree's. That total is what the footer prints and what
// decides whether Load more is offered.
func TestIssue1837_AFolderListsItsOwnFiles(t *testing.T) {
	c := connect(t)
	folder := "acc-1837-" + unique1837()
	own := map[string]bool{
		createAt1837(t, c, folder, "level-a"): true,
		createAt1837(t, c, folder, "level-b"): true,
	}
	for i := range 3 {
		createAt1837(t, c, folder+"/posters", fmt.Sprintf("poster-%d", i))
	}

	ids, total, _ := listing1837(t, c, "path="+url.QueryEscape(folder)+"&direct=true")
	if total != 2 || len(ids) != 2 {
		t.Fatalf("the folder's own level answered %d rows, total %d; want its 2 own files", len(ids), total)
	}
	for _, id := range ids {
		if !own[id] {
			t.Errorf("the folder's own level carried %s, a file filed beneath it", id)
		}
	}

	// Without direct the route still answers the subtree, which is what a
	// folder move plans over.
	_, subtree, _ := listing1837(t, c, "path="+url.QueryEscape(folder))
	if subtree != 5 {
		t.Errorf("the subtree listing's total is %d; want 5", subtree)
	}
}

// TestIssue1837_AFolderWithNoFilesOfItsOwnHasNothingToPage is criterion 2: a
// folder whose files all sit in its subfolders reports a total of zero and an
// empty list, so the page shows no Load more and no "Showing" line however
// many files the subfolders hold.
func TestIssue1837_AFolderWithNoFilesOfItsOwnHasNothingToPage(t *testing.T) {
	c := connect(t)
	folder := "acc-1837-" + unique1837()
	for i := range 4 {
		createAt1837(t, c, folder+"/posters", fmt.Sprintf("poster-%d", i))
	}
	createAt1837(t, c, folder+"/logos", "logo")

	ids, total, raw := listing1837(t, c, "path="+url.QueryEscape(folder)+"&direct=true")
	if total != 0 || len(ids) != 0 {
		t.Fatalf("a level with no files of its own answered %d rows, total %d; want none", len(ids), total)
	}
	if raw == nil {
		t.Error(`an empty level answered "resources": null; the portal reads []`)
	}
}

// Index-job fixtures. They are rows in the platform's own queue, written where
// a worker cannot take them: the running row holds a lease an hour out, and
// every pending row is scheduled an hour out, so the platform's own workers
// leave them alone while the dashboard reads them. The kind is tools, which
// every deployment with a queue registers.
const (
	kind1837    = "tools"
	backlog1837 = 600
)

type fixtures1837 struct {
	running, retrying, succeeded int64
}

// seedBacklog1837 writes one succeeded, one running and one retried job, then
// a backlog of pending jobs after them larger than the dashboard's old page.
func seedBacklog1837(t *testing.T, prefix string) fixtures1837 {
	t.Helper()
	dsn := issue1694DevDSN()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening the dev database at %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("the dev database does not answer at %s (%v). The suite needs the local stack: `make dev`", dsn, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = db.ExecContext(ctx, `DELETE FROM index_jobs WHERE source_kind = $1 AND source_id LIKE $2`,
			kind1837, prefix+"%")
	})

	insert := func(sourceID, status string, attempts int, extra string) int64 {
		t.Helper()
		var id int64
		q := `INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status, attempts, next_run_at` + extra
		if err := db.QueryRowContext(ctx, q, kind1837, prefix+sourceID, status, attempts).Scan(&id); err != nil {
			t.Fatalf("seeding %s job %s: %v", status, sourceID, err)
		}
		return id
	}
	var f fixtures1837
	f.succeeded = insert("done", "succeeded", 1,
		`, started_at, completed_at) VALUES ($1, $2, 'write', $3, $4, NOW(), NOW() - interval '2 seconds', NOW()) RETURNING id`)
	f.running = insert("running", "running", 1,
		`, worker_id, started_at, lease_expires_at) VALUES ($1, $2, 'write', $3, $4, NOW(), 'acceptance-1837', NOW(), NOW() + interval '1 hour') RETURNING id`)
	f.retrying = insert("backing-off", "pending", 2,
		`, last_error) VALUES ($1, $2, 'write', $3, $4, NOW() + interval '1 hour', 'acceptance #1837: a retryable failure') RETURNING id`)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status, next_run_at)
		SELECT $1, $2 || 'backlog-' || g, 'reconciler', 'pending', NOW() + interval '1 hour'
		  FROM generate_series(1, $3) AS g`, kind1837, prefix, backlog1837); err != nil {
		t.Fatalf("seeding the backlog: %v", err)
	}
	return f
}

// jobIDs1837 returns the ids one job-list request answered with.
func jobIDs1837(t *testing.T, c *client, query string) map[int64]bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/index-jobs/jobs?"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/index-jobs/jobs?%s: status %d: %v", query, status, body)
	}
	ids := map[int64]bool{}
	list, _ := body["jobs"].([]any)
	for _, item := range list {
		if j, ok := item.(map[string]any); ok {
			if id, ok := j["id"].(float64); ok {
				ids[int64(id)] = true
			}
		}
	}
	return ids
}

// summaryKinds1837 returns the summary's per-kind rows keyed by kind.
func summaryKinds1837(t *testing.T, c *client) map[string]map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/index-jobs", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/index-jobs: status %d: %v", status, body)
	}
	out := map[string]map[string]any{}
	list, _ := body["kinds"].([]any)
	for _, item := range list {
		if k, ok := item.(map[string]any); ok {
			if name, _ := k["kind"].(string); name != "" {
				out[name] = k
			}
		}
	}
	return out
}

// TestIssue1837_InFlightAndRetryBackoffSeeBehindTheBacklog is criterion 3's
// table-backed half: with more pending jobs enqueued after the running and
// retried ones than the drill-down's page holds, the page the dashboard used to
// derive every panel from holds none of them, and the questions In flight and
// Retry backoff now ask each find theirs. In flight's count equals the kind
// cards' running.
func TestIssue1837_InFlightAndRetryBackoffSeeBehindTheBacklog(t *testing.T) {
	c := connect(t)
	f := seedBacklog1837(t, "acceptance-1837-"+unique1837()+"-")

	page := jobIDs1837(t, c, "limit=500")
	for name, id := range map[string]int64{"running": f.running, "retrying": f.retrying, "succeeded": f.succeeded} {
		if page[id] {
			t.Fatalf("the newest 500 rows carry the %s fixture; the backlog did not bury it, so this run proves nothing", name)
		}
	}

	if !jobIDs1837(t, c, "retrying=true&limit=50")[f.retrying] {
		t.Error("retrying=true did not list the job waiting out its backoff")
	}
	if kinds := summaryKinds1837(t, c); number(t, kinds[kind1837], "retrying") < 1 {
		t.Errorf("the summary's %s retrying count is %v; want at least the seeded one", kind1837, kinds[kind1837]["retrying"])
	}

	// The platform's own workers run beside this test, so the two reads are
	// compared until they are taken between the same two job transitions.
	var running map[int64]bool
	var cards float64
	for range 10 {
		running = jobIDs1837(t, c, "status=running&limit=500")
		cards = 0
		for _, k := range summaryKinds1837(t, c) {
			cards += number(t, k, "running")
		}
		if float64(len(running)) == cards {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !running[f.running] {
		t.Error("status=running did not list the running job")
	}
	if float64(len(running)) != cards {
		t.Errorf("In flight lists %d running jobs; the kind cards count %v", len(running), cards)
	}
}

// promQuery1837 runs one instant query through the platform's PromQL proxy,
// the route the dashboard reads, and returns each series' value keyed by its
// kind label ("" for an unlabelled series).
func promQuery1837(t *testing.T, c *client, query string) map[string]float64 {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/observability/query?query="+url.QueryEscape(query), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the PromQL proxy answered %d for %s: %v. `make dev` runs Prometheus beside the platform", status, query, body)
	}
	data, _ := body["data"].(map[string]any)
	result, _ := data["result"].([]any)
	out := map[string]float64{}
	for _, item := range result {
		series, _ := item.(map[string]any)
		metric, _ := series["metric"].(map[string]any)
		kind, _ := metric["kind"].(string)
		value, _ := series["value"].([]any)
		if len(value) != 2 {
			continue
		}
		s, _ := value[1].(string)
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			out[kind] = v
		}
	}
	return out
}

// TestIssue1837_ThroughputAndLatencyComeFromTheMetrics is criterion 3's
// metrics half: a job the platform's own worker completes is counted by
// indexjob_jobs_total and timed by indexjob_duration_seconds, and the queries
// the Throughput and Embed latency panels send answer with it through the
// proxy, whatever the job table's newest page holds.
func TestIssue1837_ThroughputAndLatencyComeFromTheMetrics(t *testing.T) {
	c := connect(t)
	seedBacklog1837(t, "acceptance-1837-"+unique1837()+"-")

	// A real pass: re-index the tools kind, which the worker claims and
	// completes through the embedder the stack runs.
	status, body := c.rest(http.MethodPost, "/api/v1/admin/index-jobs/reindex",
		jsonBody(t, map[string]any{"kind": kind1837}))
	if status != http.StatusOK && status != http.StatusAccepted {
		t.Fatalf("POST /api/v1/admin/index-jobs/reindex: status %d: %v", status, body)
	}

	const (
		completed = `sum by (kind) (increase(indexjob_jobs_total{outcome="succeeded"}[15m]))`
		p50       = `histogram_quantile(0.5, sum by (kind, le) (rate(indexjob_duration_seconds_bucket{outcome="succeeded"}[24h])))`
		passes    = `sum by (kind) (increase(indexjob_duration_seconds_count{outcome="succeeded"}[24h]))`
		pending   = `max by (kind) (indexjob_queue_jobs{state="pending"})`
		retrying  = `max by (kind) (indexjob_queue_jobs{state="retrying"})`
	)
	deadline := time.Now().Add(4 * time.Minute)
	for {
		done := promQuery1837(t, c, completed)[kind1837]
		latency := promQuery1837(t, c, p50)[kind1837]
		counted := promQuery1837(t, c, passes)[kind1837]
		if done > 0 && counted > 0 && latency > 0 {
			t.Logf("tools: %.2f jobs completed in 15m, %.0f passes timed in 24h, p50 %.2fs", done, counted, latency)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no completed tools pass reached Prometheus: completed=%v passes=%v p50=%v", done, counted, latency)
		}
		time.Sleep(5 * time.Second)
	}

	// The database gauges carry the seeded backlog and its retried job.
	deadline = time.Now().Add(90 * time.Second)
	for {
		depth := promQuery1837(t, c, pending)[kind1837]
		backoff := promQuery1837(t, c, retrying)[kind1837]
		if depth >= backlog1837 && backoff >= 1 {
			t.Logf("tools: indexjob_queue_jobs pending=%v retrying=%v", depth, backoff)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("indexjob_queue_jobs never carried the seeded backlog: pending=%v retrying=%v", depth, backoff)
		}
		time.Sleep(5 * time.Second)
	}
}
