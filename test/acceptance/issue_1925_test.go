//go:build integration

package acceptance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #1925: the DataHub adapter, toolkit, configuration and dev stack are
// gone. The module no longer requires the DataHub client, the dev stack runs
// with no DataHub and lists none, and trino_describe_table's enrichment comes
// from the platform catalog.
//
// Wire forms: platform_info takes no parameter this ticket touches;
// list_connections and trino_describe_table take strings, each sent in the one
// form its schema admits.

// TestIssue1925_ModuleRequiresNoDataHubClient: go.mod names no DataHub
// module.
func TestIssue1925_ModuleRequiresNoDataHubClient(t *testing.T) {
	raw, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	if strings.Contains(string(raw), "mcp-datahub") {
		t.Fatal("go.mod still requires github.com/txn2/mcp-datahub")
	}
}

// TestIssue1925_TheStackRunsWithNoDataHub: the running platform reports no
// DataHub toolkit, connection or tool.
func TestIssue1925_TheStackRunsWithNoDataHub(t *testing.T) {
	c := connect(t)
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"platform_info", map[string]any{}},
		{"list_connections", map[string]any{"purpose": "Acceptance: the stack lists no DataHub connection."}},
	} {
		out := c.call(call.tool, call.args)
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(raw)), "datahub") {
			t.Errorf("%s still names DataHub:\n%s", call.tool, raw)
		}
	}
}

// TestIssue1925_DescribeEnrichmentComesFromTheCatalog: trino_describe_table on
// a table the catalog curates returns that curation in its enrichment.
func TestIssue1925_DescribeEnrichmentComesFromTheCatalog(t *testing.T) {
	c := connectFor(t, scanTimeout+time.Minute)
	description := "Storefront orders, curated for acceptance 1925."
	schema, termName := curated1919(t, c, description)

	res, text, err := c.callRaw("trino_describe_table", map[string]any{
		"connection": conn1918, "catalog": "memory", "schema": schema, "table": "orders",
		"purpose": "Acceptance: reading a table's catalog context.",
	})
	if err != nil || res.IsError {
		t.Fatalf("trino_describe_table: %v %s", err, text)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{description, termName, "mcp:dataset:(trino," + conn1918 + ",memory." + schema + ".orders)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the enrichment does not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "urn:li:") {
		t.Errorf("the enrichment carries a DataHub URN:\n%s", got)
	}
}
