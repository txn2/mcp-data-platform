//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1900: the ClickStack bundle (deployments/observability/clickstack)
// receives everything the platform emits through the edge collector, with the
// deployment's identity on it; it carries the substrate's own metrics; and two
// deployments sharing it appear and filter apart.
//
// Wire forms: list_connections takes no arguments and is sent none; the
// caller's trace context rides as the traceparent header, the one form the W3C
// header has. The object seeding is #1899's s3_object, the same typed strings.
//
// Stack: the bundle's compose profile with the substrate override, its edge
// collector exporting to ClickStack, and `make dev` pointed at the edge
// collector with each replica a deployment of its own:
//
//	cd deployments/observability/clickstack
//	docker compose -f compose.yaml -f compose.substrate.yaml up -d
//	DEV_OTLP_ENDPOINT=localhost:4317 DEV_DEPLOYMENT_ID_B=acme-dev-b make dev
//
// CLICKSTACK_CONTAINER names the ClickStack container when the compose
// project is not the default.

const issue1900DeploymentB = "acme-dev-b"

// clickstackContainer is the ClickStack container ClickHouse is queried in:
// its default user answers on loopback only, inside the container.
func clickstackContainer() string {
	if v := os.Getenv("CLICKSTACK_CONTAINER"); v != "" {
		return v
	}
	return "mcp-observability-clickstack-1"
}

// clickhouse runs one query in ClickStack's ClickHouse and returns its rows as
// tab-separated lines.
func clickhouse(t *testing.T, query string) []string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "docker", "exec", clickstackContainer(), //nolint:gosec // a test querying its own stack
		"clickhouse-client", "-q", query+" FORMAT TSV")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ClickHouse in %s: %v\n%s\nThe bundle's compose profile must be running (deployments/observability/clickstack)", clickstackContainer(), err, out)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// clickhouseNumber is a query's single numeric result.
func clickhouseNumber(t *testing.T, query string) float64 {
	t.Helper()
	rows := clickhouse(t, query)
	if len(rows) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(rows[0], 64)
	return v
}

// waitForRows polls a query until it returns a row, and returns its rows.
func waitForRows(t *testing.T, query string, within time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if rows := clickhouse(t, query); len(rows) > 0 || time.Now().After(deadline) {
			return rows
		}
		time.Sleep(2 * time.Second)
	}
}

// resourceKeys is the resource every signal must carry: the service, its
// build and instance, and the deployment's identity.
var resourceKeys = []string{"service.name", "service.version", "service.instance.id", "mcp_platform.deployment.id", "deployment.environment.name"}

// resourceProjection selects resourceKeys from a table's ResourceAttributes.
func resourceProjection() string {
	cols := make([]string, len(resourceKeys))
	for i, k := range resourceKeys {
		cols[i] = fmt.Sprintf("ResourceAttributes['%s']", k)
	}
	return strings.Join(cols, ", ")
}

// assertResource checks one row of resourceProjection.
func assertResource(t *testing.T, signal, row string) {
	t.Helper()
	vals := strings.Split(row, "\t")
	if len(vals) < len(resourceKeys) {
		t.Fatalf("%s: resource row %q", signal, row)
	}
	for i, k := range resourceKeys {
		if vals[i] == "" {
			t.Errorf("%s carries no %s", signal, k)
		}
	}
	if vals[0] != "mcp-data-platform" {
		t.Errorf("%s service.name = %q", signal, vals[0])
	}
	if vals[4] != "dev" {
		t.Errorf("%s deployment.environment.name = %q, want dev", signal, vals[4])
	}
}

// TestIssue1900_EverySignalReachesClickHouseWithTheResource: traces, metrics
// and logs reach ClickHouse through the edge collector carrying the full
// resource attribute set, and a log line joins its trace by trace id. The
// call is the analyst's persona-refused list_connections, which is traced as
// an error (so tail sampling keeps it), logged, and counted.
func TestIssue1900_EverySignalReachesClickHouseWithTheResource(t *testing.T) {
	traceparent, traceID, _ := callerTrace(t)
	analyst := connectVia(t, baseURL(), analystKey1892, traceparentRoundTripper{traceparent: traceparent, base: http.DefaultTransport}, sessionTimeout)
	res, text, err := analyst.callRaw("list_connections", map[string]any{})
	if err != nil || !res.IsError {
		t.Fatalf("list_connections as the analyst should be refused: err=%v %s", err, text)
	}

	spans := waitForRows(t, fmt.Sprintf("SELECT %s, SpanName FROM otel_traces WHERE TraceId = '%s' AND SpanName LIKE 'tools/call%%'", resourceProjection(), traceID), 90*time.Second)
	if len(spans) == 0 {
		t.Fatalf("no tools/call span of trace %s reached ClickHouse", traceID)
	}
	assertResource(t, "the span", spans[0])

	logs := waitForRows(t, fmt.Sprintf("SELECT %s, Body FROM otel_logs WHERE TraceId = '%s' AND Body = '%s'", resourceProjection(), traceID, deniedLogMessage), 90*time.Second)
	if len(logs) == 0 {
		t.Fatalf("no %q log line of trace %s reached ClickHouse", deniedLogMessage, traceID)
	}
	assertResource(t, "the log line", logs[0])

	joined := clickhouseNumber(t, fmt.Sprintf("SELECT count() FROM otel_logs AS l INNER JOIN otel_traces AS s ON l.TraceId = s.TraceId WHERE l.TraceId = '%s' AND l.Body = '%s'", traceID, deniedLogMessage))
	if joined == 0 {
		t.Fatalf("the log line does not join its trace by trace id %s", traceID)
	}

	metrics := waitForRows(t, fmt.Sprintf("SELECT %s FROM otel_metrics_sum WHERE MetricName = 'mcp_tool_call_denials' AND TimeUnix > now() - INTERVAL 5 MINUTE LIMIT 1", resourceProjection()), 90*time.Second)
	if len(metrics) == 0 {
		t.Fatal("mcp_tool_call_denials did not reach ClickHouse")
	}
	assertResource(t, "the metric", metrics[0])
}

// TestIssue1900_TheSubstrateArrives: with SeaweedFS as the object store, its
// per-volume capacity, the platform's own bytes per bucket and purpose, and
// the PostgreSQL receiver's metrics arrive; and one seeded orphan and one
// dangling reference are counted in ClickHouse, as #1899 counts them on the
// platform's own scrape.
func TestIssue1900_TheSubstrateArrives(t *testing.T) {
	recent := "TimeUnix > now() - INTERVAL 5 MINUTE"
	if v := clickhouseNumber(t, "SELECT max(Value) FROM otel_metrics_gauge WHERE MetricName = 'SeaweedFS_volumeServer_resource' AND Attributes['type'] = 'all' AND "+recent); v <= 0 {
		t.Errorf("SeaweedFS_volumeServer_resource{type=\"all\"} = %v, want the volume's capacity", v)
	}
	for _, purpose := range []string{"portal_assets", "resources", "thumbnails"} {
		q := fmt.Sprintf("SELECT max(Value) FROM otel_metrics_gauge WHERE MetricName = 'storage_bucket' AND Attributes['purpose'] = '%s' AND %s", purpose, recent)
		if v := clickhouseNumber(t, q); v <= 0 {
			t.Errorf("storage_bucket_bytes{purpose=%q} did not arrive", purpose)
		}
	}
	for _, m := range []struct{ table, name string }{
		{"sum", "postgresql.db_size"}, {"gauge", "postgresql.connection.max"}, {"sum", "postgresql.deadlocks"},
		{"gauge", "postgresql.xid.age"}, {"gauge", "postgresql.transaction.longest"},
	} {
		q := fmt.Sprintf("SELECT count() FROM otel_metrics_%s WHERE MetricName = '%s' AND %s", m.table, m.name, recent)
		if clickhouseNumber(t, q) == 0 {
			t.Errorf("%s did not arrive", m.name)
		}
	}

	latest := func(metric string) float64 {
		return clickhouseNumber(t, fmt.Sprintf("SELECT argMax(Value, TimeUnix) FROM otel_metrics_gauge WHERE MetricName = '%s' AND Attributes['purpose'] = 'resources' AND ResourceAttributes['mcp_platform.deployment.id'] = 'acme-dev' AND %s", metric, recent))
	}
	// Each count is read after a listing that began after the step before it
	// (an earlier test's seeds, cleaned up since, are not in the baseline),
	// and once the export carrying it has reached ClickHouse.
	settled := func(metric string) float64 {
		want := metricSeries(oneReplica(t), metric, map[string]string{"purpose": "resources"})
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) && latest(metric) != want {
			time.Sleep(2 * time.Second)
		}
		return latest(metric)
	}
	waitForFreshListing(t, time.Now())
	orphans, dangling := settled("storage_orphaned_objects"), settled("storage_dangling_references")
	defer seedOrphanAndDangling1899(t, connect(t))()
	waitForFreshListing(t, time.Now())
	settled("storage_orphaned_objects")
	settled("storage_dangling_references")
	if got := latest("storage_orphaned_objects"); got != orphans+1 {
		t.Errorf("storage_orphaned_objects{purpose=\"resources\"} in ClickHouse = %v, want %v", got, orphans+1)
	}
	if got := latest("storage_dangling_references"); got != dangling+1 {
		t.Errorf("storage_dangling_references{purpose=\"resources\"} in ClickHouse = %v, want %v", got, dangling+1)
	}
}

// TestIssue1900_TwoDeploymentsAppearAndFilterApart: two platform instances
// with different mcp_platform.deployment.id against one ClickStack both
// appear, and a filter on either finds that one alone.
func TestIssue1900_TwoDeploymentsAppearAndFilterApart(t *testing.T) {
	recent := "MetricName = 'mcp_platform_build_info' AND TimeUnix > now() - INTERVAL 5 MINUTE"
	rows := clickhouse(t, "SELECT DISTINCT ResourceAttributes['mcp_platform.deployment.id'] FROM otel_metrics_gauge WHERE "+recent+" ORDER BY 1")
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r] = true
	}
	if !seen["acme-dev"] || !seen[issue1900DeploymentB] {
		t.Fatalf("deployments reporting: %v, want acme-dev and %s (DEV_DEPLOYMENT_ID_B)", rows, issue1900DeploymentB)
	}
	for _, d := range []string{"acme-dev", issue1900DeploymentB} {
		q := fmt.Sprintf("SELECT groupUniqArray(ResourceAttributes['mcp_platform.deployment.id']) FROM otel_metrics_gauge WHERE %s AND ResourceAttributes['mcp_platform.deployment.id'] = '%s'", recent, d)
		if got := clickhouse(t, q); len(got) != 1 || got[0] != fmt.Sprintf("['%s']", d) {
			t.Errorf("filtered to %s, found %v", d, got)
		}
		instances := clickhouseNumber(t, fmt.Sprintf("SELECT uniqExact(ResourceAttributes['service.instance.id']) FROM otel_metrics_gauge WHERE %s AND ResourceAttributes['mcp_platform.deployment.id'] = '%s'", recent, d))
		if instances < 1 {
			t.Errorf("%s reports no instance", d)
		}
	}
}
