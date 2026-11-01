//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #2381: the catalog search index mirrors at most
// knowledge.catalog_index.max_entries datasets. It took them in reference
// order, so API operations ("api" sorts before "trino") filled it before any
// table, and its coverage compared the mirror with itself and read healthy.
//
// The criterion starts this tree's binary twice on a fresh database of its
// own, because the cap is a start-time setting and the dev stack's catalog is
// far under the default: once to scan a catalog holding Trino tables, the
// api-test fixture's operations and the platform's own platform-admin
// operations, then again with the cap set two above the number of tables.
//
// Wire forms: search's intent is a string, sent as one; the admin routes take
// JSON bodies, sent as objects.

const issue2381Key = "acceptance-2381-key"

// issue2381Wait is how long each platform's client session lasts. The
// criterion waits up to three minutes for a scan and six for the capped index
// to embed its entries, which on a CI runner outlasts the suite's default
// two-minute session; a session shorter than the wait ends it with "context
// deadline exceeded" rather than with the platform's answer (#1738).
const issue2381Wait = 8 * time.Minute

const issue2381Config = `
server:
  name: acceptance-2381
  transport: http
  address: "%%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "` + issue2381Key + `"
        name: "acceptance"
        roles: ["admin"]
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "%s"
admin:
  enabled: true
memory:
  embedding:
    provider: ollama
    ollama:
      url: "http://localhost:%s"
      model: "nomic-embed-text"
knowledge:
  enabled: true
  index_reconcile_interval: 2s
  catalog_index:
    max_entries: %d
workflow:
  require_search: false
  require_schema: false
scripts:
  worker:
    enabled: false
notifications:
  enabled: false
thumbnails:
  enabled: false
toolkits:
  trino:
    enabled: true
    instances:
      dev:
        host: "localhost"
        port: 9283
        user: "dev"
        catalog: "memory"
        schema: "default"
        ssl: false
`

func issue2381ConfigFor(dsn string, maxEntries int) string {
	port := os.Getenv("DEV_OLLAMA_PORT")
	if port == "" {
		port = "11434"
	}
	return fmt.Sprintf(issue2381Config, dsn, port, maxEntries)
}

// issue2381Count runs one counting query against the criterion's database.
func issue2381Count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// issue2381Fixture registers the api-test fixture connection with its spec,
// the way the dev stack registers it.
func issue2381Fixture(t *testing.T, c *client) {
	t.Helper()
	res, err := http.Get("http://localhost:9282/openapi.yaml") //nolint:noctx // a loopback fixture
	if err != nil {
		t.Fatalf("reading the api-test fixture's spec: %v", err)
	}
	spec, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if status, out := c.rest(http.MethodPost, "/api/v1/admin/api-catalogs", jsonBody(t, map[string]any{
		"id": "api-test-fixture", "name": "api-test-fixture", "display_name": "API Test Fixture",
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("creating the fixture catalog answered %d %v", status, out)
	}
	if status, out := c.rest(http.MethodPut, "/api/v1/admin/api-catalogs/api-test-fixture/specs/default", jsonBody(t, map[string]any{
		"source_kind": "inline", "content": string(spec),
	})); status/100 != 2 {
		t.Fatalf("storing the fixture spec answered %d %v", status, out)
	}
	if status, out := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/api-test-fixture", jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url": "http://localhost:9282", "auth_mode": "api_key", "credential": "apitest-dev-key-2024",
			"api_key_placement": "header", "api_key_header": "X-API-Key", "connection_name": "api-test-fixture",
			"trust_level": "untrusted", "catalog_id": "api-test-fixture",
		},
		"description": "Acceptance #2381: API operations for the catalog.",
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("creating the fixture connection answered %d %v", status, out)
	}
}

// issue2381Await polls until ok reports true or within passes.
func issue2381Await(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, within)
		}
		time.Sleep(time.Second)
	}
}

const (
	issue2381LiveDatasets = `SELECT COUNT(*) FROM catalog_entities WHERE kind = 'dataset' AND removed_at IS NULL`
	issue2381Tables       = issue2381LiveDatasets + ` AND connection_kind = 'trino'`
	issue2381AdminOps     = issue2381LiveDatasets + ` AND connection_kind = 'api' AND connection_name = 'platform-admin'`
	issue2381FixtureOps   = issue2381LiveDatasets + ` AND connection_kind = 'api' AND connection_name = 'api-test-fixture'`
)

// TestIssue2381_ACappedIndexKeepsTablesAndReadsShort is the ticket's three
// criteria: on a catalog larger than the cap, the index keeps every table
// (the stated rule: tables, then GraphQL operations, then API operations),
// a curated table is reachable by search, the platform's own platform-admin
// operations are never mirrored, and the Indexing coverage holds the index to
// the catalog's total (less platform-admin's) and is not healthy while
// datasets are left out.
func TestIssue2381_ACappedIndexKeepsTablesAndReadsShort(t *testing.T) {
	dev := connect(t)
	schema := schema1918(t, dev, "2381", "ledger")
	exec1918(dev, "CREATE TABLE memory."+schema+".ledger (entry_id bigint, amount decimal(12,2))")

	name := fmt.Sprintf("acceptance_2381_%d", time.Now().UnixNano())
	devPSQL(t, "mcp_platform", "CREATE DATABASE "+name)
	t.Cleanup(func() { devPSQL(t, "mcp_platform", "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	dsn := "postgres://platform:platform_secret@localhost:" + devPostgresPort() + "/" + name + "?sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// First start: the catalog is scanned, the table curated.
	first := startPlatform(t, issue2381ConfigFor(dsn, 5000))
	first.serving(t, 3*time.Minute)
	admin := connectAtFor(t, first.base, issue2381Key, issue2381Wait)
	issue2381Fixture(t, admin)
	ref := catalog.DatasetRef("trino", "dev", "memory", schema, "ledger").String()
	issue2381Await(t, 3*time.Minute, "the scan cataloging the table, the fixture and platform-admin", func() bool {
		admin.rest(http.MethodPost, "/api/v1/admin/catalog/scans", http.NoBody)
		return issue2381Count(t, db, issue2381LiveDatasets+` AND reference = $1`, ref) == 1 &&
			issue2381Count(t, db, issue2381FixtureOps) > 2 && issue2381Count(t, db, issue2381AdminOps) > 0
	})
	phrase := fmt.Sprintf("settlement ledger reconciled nightly marker%d", time.Now().UnixNano())
	issue1921Write(t, admin, ref, "description", phrase)
	tables := issue2381Count(t, db, issue2381Tables)
	first.stop(t)

	// Second start, with room for every table and two operations.
	capped := tables + 2
	second := startPlatform(t, issue2381ConfigFor(dsn, capped))
	second.serving(t, 3*time.Minute)
	admin = connectAtFor(t, second.base, issue2381Key, issue2381Wait)

	var cov map[string]any
	var verdict string
	issue2381Await(t, 6*time.Minute, "the capped index embedding its entries", func() bool {
		kind := summaryKinds1837(t, admin)["catalog-datasets"]
		cov, _ = kind["coverage"].(map[string]any)
		verdict, _ = kind["verdict"].(string)
		indexed, _ := cov["indexed"].(float64)
		if int(indexed) == capped && verdict != "indexing" {
			return true
		}
		t.Logf("waiting on the capped index: verdict %q, coverage %v, want %d indexed", verdict, cov, capped)
		return false
	})
	expected := issue2381Count(t, db, issue2381LiveDatasets) - issue2381Count(t, db, issue2381AdminOps)
	if got, _ := cov["expected"].(float64); int(got) != expected || expected <= capped {
		t.Errorf("coverage %v: expected %v, want the catalog's %d live datasets less platform-admin's, above the cap %d",
			cov, cov["expected"], expected, capped)
	}
	if verdict != "degraded" {
		t.Errorf("the capped index reads %q, want degraded while datasets are left out", verdict)
	}

	// Every table is mirrored, and no platform-admin operation is.
	mirroredTables := issue2381Count(t, db, `SELECT COUNT(*) FROM catalog_datasets WHERE urn LIKE 'mcp:dataset:(trino,%'`)
	mirroredAdmin := issue2381Count(t, db, `SELECT COUNT(*) FROM catalog_datasets WHERE urn LIKE 'mcp:dataset:(api,platform-admin,%'`)
	if mirroredTables != tables || mirroredAdmin != 0 {
		t.Errorf("the index mirrors %d of %d tables and %d platform-admin operations, want every table and none",
			mirroredTables, tables, mirroredAdmin)
	}

	res, text, err := admin.callRaw("search", map[string]any{
		"intent": phrase, "purpose": "Acceptance #2381: a curated table under a binding cap.",
	})
	if err != nil || res.IsError {
		t.Fatalf("search failed: %v %s", err, text)
	}
	if !strings.Contains(text, ref) {
		t.Errorf("search for the table's curated description did not return %s:\n%s", ref, firstRunes(text, 1500))
	}
}
