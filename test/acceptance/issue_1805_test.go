//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1805: where the platform validates a declared, typed argument it is
// strict and the refusal teaches. Where a value sits inside a freeform
// `{"type": "object"}` it was not validated at all: the call was accepted,
// success was reported, and the consequence appeared later.
//
// These criteria run through the real surface on the local stack — the admin
// REST routes an operator and an agent both call, and the MCP tools a session
// calls — and they cover the findings of that report other than the patch key,
// which is #1804 and its own file.
//
// Wire forms. The admin routes take their kind and name as path segments and
// their body as one JSON object, which is the only form each admits; the
// connection `config` is the freeform object this issue is about, and is sent
// below as a literal object. On the tool side, api_export's `connection`,
// `method`, `path`, `name` and `purpose` are typed strings and its `resource`
// is a closed object of typed strings (pkg/toolkit/resourcedest.go), so each
// admits exactly one JSON form; `fetch`'s reference and `list_connections`'s
// (empty) arguments likewise. Each is sent as a literal tools/call parameter of
// that form.

// unique1805 names a connection this run owns.
func unique1805() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// TestIssue1805_APlaceholderConfigIsRefusedOnWrite is finding 3: a connection
// whose credentials were environment placeholders was answered 200 OK, listed,
// looked correct, and was completely unusable — every query against it failed
// at DSN construction with "net/url: invalid userinfo".
//
// Database-managed connections get no environment substitution; file-declared
// ones do. The refusal is the difference between learning that at the write and
// learning it from a pipeline that has already run half of itself.
func TestIssue1805_APlaceholderConfigIsRefusedOnWrite(t *testing.T) {
	c := connect(t)
	name := "acceptance-1805-placeholder-" + unique1805()
	path := "/api/v1/admin/connection-instances/trino/" + name

	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"description": "Acceptance #1805: a config that cannot work must be refused.",
		"config": map[string]any{
			"host": "trino.example.com", "port": 443, "ssl": true,
			"user": "${TRINO_USER}", "password": "${TRINO_PASSWORD}",
			"catalog": "warehouse", "schema": "public",
		},
	}))
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, path, http.NoBody) })

	if status != http.StatusBadRequest {
		t.Fatalf("PUT answered %d, want 400; body: %v", status, body)
	}
	detail, _ := body["detail"].(string)
	for _, want := range []string{"user", "password", "configuration file"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not mention %q: %s", want, detail)
		}
	}

	// The refusal has to mean the connection was not created. A 400 beside a
	// stored row would be the same defect wearing a different status.
	if getStatus, got := c.rest(http.MethodGet, path, http.NoBody); getStatus != http.StatusNotFound {
		t.Errorf("the refused connection exists: GET answered %d: %v", getStatus, got)
	}
}

// TestIssue1805_AResolvedConfigIsAcceptedAndTestable is the control for the
// refusal above and finding 3's third ask: until this endpoint existed there
// was no way to verify a connection except to issue a real query through a
// separate tool and interpret the failure.
//
// The connection here points at nothing that answers, which is the case worth
// pinning: a connection the platform cannot open must say so rather than read
// as healthy.
func TestIssue1805_AConnectionThatCannotBeOpenedSaysSo(t *testing.T) {
	c := connect(t)
	name := "acceptance-1805-dead-" + unique1805()
	path := "/api/v1/admin/connection-instances/trino/" + name

	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"description": "Acceptance #1805: a connection pointed at nothing.",
		// Port 9 is discard: nothing listens, so the failure is the platform's
		// own dial rather than a fixture's behaviour.
		"config": map[string]any{"host": "127.0.0.1", "port": 9, "user": "acceptance"},
	}))
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, path, http.NoBody) })
	if status != http.StatusOK {
		t.Fatalf("PUT answered %d, want 200; body: %v", status, body)
	}

	testStatus, result := c.rest(http.MethodPost, path+"/test", http.NoBody)
	if testStatus != http.StatusServiceUnavailable {
		t.Fatalf("testing a connection that cannot be opened answered %d, want 503: %v", testStatus, result)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Errorf("a connection that did not answer reported ok: %v", result)
	}
	if detail, _ := result["detail"].(string); detail == "" {
		t.Error("the failure says nothing about what was attempted")
	}
	// The upstream's own words are what tell an operator which of the host, the
	// route and the credential is wrong.
	if errText, _ := result["error"].(string); errText == "" {
		t.Error("the failure carries no error from the attempt")
	}
}

// TestIssue1805_AServingConnectionAnswersItsTest is the other half: the
// deployment's own Trino connection opens, and the success says what answered
// rather than only that something did.
func TestIssue1805_AServingConnectionAnswersItsTest(t *testing.T) {
	c := connect(t)

	status, result := c.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/trino/acme/test", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("testing the deployment's Trino connection answered %d: %v. Is the dev stack up?", status, result)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("the connection did not answer: %v", result)
	}
	detail, _ := result["detail"].(string)
	if !strings.Contains(detail, "SELECT 1") {
		t.Errorf("the success does not say what answered: %q", detail)
	}
	// Writability rides on the same answer: the dev warehouse connection is
	// read-only, and a caller planning a write learns it here.
	if !strings.Contains(detail, "read_only") {
		t.Errorf("a read-only connection's test does not mention it: %q", detail)
	}
}

// TestIssue1805_ConnectionKindsDeclareTheirConfig is finding 3's second ask:
// the request body schema for a connection is an opaque object, so there was no
// way to answer "what does a trino connection take?" short of reading the
// deployment's configuration file out of band, which an agent without cluster
// access cannot do.
func TestIssue1805_ConnectionKindsDeclareTheirConfig(t *testing.T) {
	c := connect(t)

	status, one := c.rest(http.MethodGet, "/api/v1/admin/connection-kinds/trino", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET connection-kinds/trino answered %d: %v", status, one)
	}

	raw, err := json.Marshal(one["config_schema"])
	if err != nil {
		t.Fatalf("the config schema does not marshal: %v", err)
	}
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("the config schema is not a JSON object: %v", err)
	}
	for _, key := range []string{"host", "port", "user", "password", "catalog", "read_only"} {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("the trino config schema does not declare %q", key)
		}
	}
	if len(schema.Required) == 0 {
		t.Error("the trino config schema requires nothing, not even a host")
	}
	// The note is part of the answer: a schema read as a guarantee would be a
	// new instance of the same defect.
	if note, _ := one["note"].(string); !strings.Contains(note, "${VAR} is NOT expanded") {
		t.Errorf("the schema carries no note about literal values: %q", note)
	}

	// Every kind an instance can be created under answers, not just trino.
	status, _ = c.rest(http.MethodGet, "/api/v1/admin/connection-kinds", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET connection-kinds answered %d", status)
	}
}

// TestIssue1805_WritabilityIsVisibleBeforeACall is finding 5: list_connections
// returned name, kind and description, and nothing said whether a connection
// could write. The agent found out by running a script that failed mid-run,
// after it had already staged data.
func TestIssue1805_WritabilityIsVisibleBeforeACall(t *testing.T) {
	c := connect(t)

	out := c.call("list_connections", map[string]any{})
	entries, _ := out["connections"].([]any)
	if len(entries) == 0 {
		t.Fatal("list_connections returned no connections")
	}

	seen := map[string]bool{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		kind, _ := entry["kind"].(string)
		name, _ := entry["name"].(string)
		if kind != "trino" && kind != "s3" {
			continue
		}
		readOnly, ok := entry["read_only"].(bool)
		if !ok {
			t.Errorf("%s connection %q reports no read_only", kind, name)
			continue
		}
		seen[kind] = true
		if kind == "trino" && name == "acme" && !readOnly {
			t.Errorf("the read-only warehouse connection reports read_only=false")
		}
		if kind == "trino" && name == "acme-scratch" && readOnly {
			t.Errorf("the scratch connection, which must accept a registration's DDL, reports read_only=true")
		}
	}
	if !seen["trino"] {
		t.Error("no trino connection reported its writability")
	}

	// The same fact reaches a caller that fetched the connection rather than
	// listed it, since that is where an agent reads what a connection is.
	doc := c.call("fetch", map[string]any{
		"reference": "mcp:connection:(trino,acme)",
		"purpose":   "Acceptance #1805: read a connection's writability from its own document.",
	})
	if !strings.Contains(strings.ToLower(fmt.Sprintf("%v", doc)), "readonly") {
		t.Errorf("the connection document carries no writability: %v", doc)
	}
}

// TestIssue1805_AResourceExportNestsItsResult is finding 2: the tool's
// description read as though the reference were on the result. It is not — it
// is under `resource` — and an agent that read result["reference"] got an empty
// string and failed its own run AFTER the export had already happened, leaving
// a real resource version behind.
func TestIssue1805_AResourceExportNestsItsResult(t *testing.T) {
	c := connect(t)
	name := "acceptance-1805-" + unique1805()

	out := c.call("api_export", map[string]any{
		"connection": apiTestConnection,
		"method":     "GET",
		"path":       "/v1/pagination/link",
		"name":       name,
		"purpose":    "Acceptance #1805: land an export in a managed resource and read its result shape.",
		"resource": map[string]any{
			"path":     "acceptance-1805",
			"filename": name + ".json",
		},
	})

	// The shape itself: the fields are nested, and the top level does not
	// pretend otherwise by carrying an empty one.
	if _, present := out["reference"]; present {
		t.Errorf("the result carries a top-level reference beside the nested one: %v", out)
	}
	resource, _ := out["resource"].(map[string]any)
	if resource == nil {
		t.Fatalf("a resource destination returned no resource block: %v", out)
	}
	reference, _ := resource["reference"].(string)
	if !strings.HasPrefix(reference, "mcp:resource:") {
		t.Errorf("resource.reference = %q; want an mcp:resource: reference", reference)
	}
	if uri, _ := resource["uri"].(string); uri == "" {
		t.Error("resource.uri is empty")
	}
	if version, _ := resource["version"].(float64); version < 1 {
		t.Errorf("resource.version = %v; want the version this call recorded", resource["version"])
	}
	t.Cleanup(func() {
		if id, _ := resource["resource_id"].(string); id != "" {
			_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		}
	})

	// And the description an agent reads before calling has to say so, which is
	// the half that makes the shape discoverable rather than surprising.
	for _, tool := range c.tools() {
		if tool.Name != "api_export" {
			continue
		}
		if !strings.Contains(tool.Description, "nested under `resource`") {
			t.Errorf("api_export's description does not state the nesting: %s", tool.Description)
		}
	}
}
