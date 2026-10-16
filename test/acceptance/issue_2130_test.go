//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #2130: through v1.145 a column with no curation of its own showed its
// upstream column's along DataHub lineage; the platform catalog that replaced
// DataHub returned each column's own curation only. These criteria make a
// table from another with CREATE TABLE ... AS SELECT on the real Trino (which
// records the lineage edge), scan both into the catalog, describe a column of
// the upstream table in the portal, and describe the downstream table through
// trino_describe_table, the call an agent makes, reading the enrichment it
// carries.
//
// Wire forms: trino_execute and trino_describe_table take connection, sql,
// catalog, schema, table and purpose as strings; the portal aspect write's
// value is sent as a string (description) and an array of strings
// (glossary_terms are not set here, so the form is the description's).

// lineageTimeout bounds the wait for the audit pipeline to record the edge a
// CREATE TABLE ... AS SELECT makes.
const lineageTimeout = 2 * time.Minute

// columnContext2130 calls trino_describe_table and returns the column_context
// its semantic enrichment carries, and the inheritance sources it names.
func columnContext2130(t *testing.T, c *client, schema, table string) (cols map[string]any, sources []any) {
	t.Helper()
	res, text, err := c.callRaw("trino_describe_table", map[string]any{
		"connection": conn1918, "catalog": "memory", "schema": schema, "table": table,
		"purpose": "Acceptance #2130: the column context an agent reads for this table.",
	})
	if err != nil || res.IsError {
		t.Fatalf("trino_describe_table: %v %s", err, text)
	}
	for _, content := range res.Content {
		tc, ok := content.(*mcp.TextContent)
		if !ok || !strings.Contains(tc.Text, "column_context") {
			continue
		}
		var enrichment map[string]any
		if json.Unmarshal([]byte(tc.Text), &enrichment) != nil {
			continue
		}
		cols, _ = enrichment["column_context"].(map[string]any)
		sources, _ = enrichment["inheritance_sources"].([]any)
		return cols, sources
	}
	t.Fatalf("trino_describe_table carried no column_context: %d content items, first %.400s", len(res.Content), text)
	return nil, nil
}

// waitUpstream2130 waits for the catalog to hold the lineage edge from origin
// to mirror, which the audit pipeline records after the statement returns.
func waitUpstream2130(t *testing.T, c *client, mirror, origin string) {
	t.Helper()
	path := "/api/v1/portal/catalog/dataset?ref=" + url.QueryEscape(mirror)
	deadline := time.Now().Add(lineageTimeout)
	for time.Now().Before(deadline) {
		if status, ds := c.rest(http.MethodGet, path, nil); status == http.StatusOK {
			if strings.Contains(fmt.Sprint(ds["upstream"]), origin) {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("the catalog never recorded %s upstream of %s", origin, mirror)
}

func TestIssue2130_AColumnInheritsItsUpstreamColumnsDescription(t *testing.T) {
	c := connect(t)
	schema := schema1918(t, c, "2130", "origin", "mirror")
	exec1918(c, "CREATE TABLE memory."+schema+".origin (email VARCHAR, tier VARCHAR, region VARCHAR)")
	exec1918(c, "CREATE TABLE memory."+schema+".mirror AS SELECT * FROM memory."+schema+".origin")
	origin, mirror := ref1918(schema, "origin"), ref1918(schema, "mirror")
	scan1918(t, c)
	waitUpstream2130(t, c, mirror, origin)

	originRef, err := catalog.ParseRef(origin)
	if err != nil {
		t.Fatalf("parsing %s: %v", origin, err)
	}
	mirrorRef, err := catalog.ParseRef(mirror)
	if err != nil {
		t.Fatalf("parsing %s: %v", mirror, err)
	}
	issue1921Write(t, c, catalog.ColumnRef(originRef, "email").String(), "description", "Where the customer is contacted.")
	issue1921Write(t, c, catalog.ColumnRef(originRef, "region").String(), "description", "The origin's words for region.")
	issue1921Write(t, c, catalog.ColumnRef(mirrorRef, "region").String(), "description", "The mirror's own words for region.")

	cols, sources := columnContext2130(t, connect(t), schema, "mirror")

	email, _ := cols["email"].(map[string]any)
	if email["description"] != "Where the customer is contacted." {
		t.Fatalf("the mirror's email column does not inherit the origin's description: %v", cols)
	}
	from, _ := email["inherited_from"].(map[string]any)
	if from["source_dataset"] != origin || from["source_column"] != "email" || from["hops"] != float64(1) || from["match_method"] != "name_exact" {
		t.Fatalf("inherited_from does not name the origin's column: %v", email["inherited_from"])
	}
	if len(sources) != 1 || sources[0] != origin {
		t.Fatalf("inheritance_sources = %v, want [%s]", sources, origin)
	}

	// A column's own description is kept, never replaced by its upstream's.
	region, _ := cols["region"].(map[string]any)
	if region["description"] != "The mirror's own words for region." || region["inherited_from"] != nil {
		t.Fatalf("the mirror's own region description was not kept: %v", region)
	}

	// A column nothing upstream describes is not described.
	if tier, ok := cols["tier"]; ok {
		t.Fatalf("the tier column, described nowhere, carries context: %v", tier)
	}
}
