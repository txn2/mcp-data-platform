//go:build integration

package acceptance

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Issue #1888: the scratch features need a Trino setup the platform could not
// check until an administrator created a webhook source, and when the check
// failed it named the engine's refusal rather than the rule to add.
//
// The local stack's Trino runs the access-control rules
// docs/server/scratch-catalog.md publishes (dev/trino/rules.json): mcp-scratch
// is the documented scratch identity, and mcp-scratch-no-procedures holds the
// same catalogs with no procedures rule, which is the setup the reference
// install had when it failed. These criteria put a connection on each and ask
// the platform through the admin routes an operator uses.
//
// Each connection is saved through one replica and tested through the other
// at once, with no pause: the other replica holds only the stored row until the
// reload bus delivers the save, and the test route takes the connection on from
// the store rather than answering that it does not exist.
//
// Wire forms: the connection routes take their kind and name as path segments
// and a JSON object body; the connection `config` is a freeform object, sent as
// a literal object, and its `scratch` a nested literal object. The test route
// takes no body. The webhook source route takes one JSON object.

// issue1888Trino is where the platform process reaches the local Trino, as the
// file-declared connections do.
func issue1888Trino() (string, int) {
	host := os.Getenv("TRINO_HOST")
	if host == "" {
		host = "localhost"
	}
	port, err := strconv.Atoi(os.Getenv("TRINO_PORT"))
	if err != nil {
		port = 9283
	}
	return host, port
}

// issue1888Connection stores a scratch connection authenticating as user and
// removes it when the test ends. It answers the connection's admin path.
func issue1888Connection(t *testing.T, c *client, name, user string) string {
	t.Helper()
	host, port := issue1888Trino()
	path := "/api/v1/admin/connection-instances/trino/" + name
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"description": "Acceptance #1888: a scratch connection as " + user + ".",
		"config": map[string]any{
			"host": host, "port": port, "ssl": false, "user": user,
			"catalog": "scratch_resources", "schema": "uploads", "read_only": false,
			"scratch": map[string]any{"catalog": "scratch_resources", "schema": "uploads"},
		},
	}))
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, path, http.NoBody) })
	if status != http.StatusOK {
		t.Fatalf("storing connection %s answered %d: %v", name, status, body)
	}
	return path
}

// TestIssue1888_TheDocumentedScratchIdentityPassesTheConnectionTest is the
// documented rules, run on a real coordinator: a connection as mcp-scratch
// passes, and the success names what it checked.
func TestIssue1888_TheDocumentedScratchIdentityPassesTheConnectionTest(t *testing.T) {
	a, b := connectReplicaPair(t)
	path := issue1888Connection(t, a, "acc1888-scratch-"+unique1805(), "mcp-scratch")

	status, result := b.rest(http.MethodPost, path+"/test", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("testing the documented scratch identity answered %d: %v", status, result)
	}
	detail, _ := result["detail"].(string)
	want := "the scratch catalog scratch_resources allows this connection to call sync_partition_metadata, register_partition, unregister_partition"
	if !strings.Contains(detail, want) {
		t.Errorf("the success does not say what it checked:\n got %q\nwant %q", detail, want)
	}
}

// TestIssue1888_AScratchUserWithoutProceduresFailsNamingTheRule is the
// reference install's setup: "all" on the catalog, no procedures rule. The
// connection test fails before any source exists, naming each procedure and
// the rule to add.
func TestIssue1888_AScratchUserWithoutProceduresFailsNamingTheRule(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := "acc1888-noproc-" + unique1805()
	path := issue1888Connection(t, a, name, "mcp-scratch-no-procedures")

	status, result := b.rest(http.MethodPost, path+"/test", http.NoBody)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("testing a scratch user with no procedures rule answered %d, want 503: %v", status, result)
	}
	detail, _ := result["detail"].(string)
	for _, procedure := range []string{"sync_partition_metadata", "register_partition", "unregister_partition"} {
		want := "the Trino user of connection " + name + " may not EXECUTE scratch_resources.system." + procedure
		if !strings.Contains(detail, want) {
			t.Errorf("the failure does not name %s:\n%s", procedure, detail)
		}
	}
	for _, want := range []string{"add a procedures rule", "docs/server/scratch-catalog.md#access-control"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the failure does not say %q:\n%s", want, detail)
		}
	}
	if errText, _ := result["error"].(string); !strings.Contains(errText, "Access Denied: Cannot execute procedure") {
		t.Errorf("the engine's own words are missing: %q", errText)
	}
}

// TestIssue1888_AWebhookSourceOnThatConnectionIsRefusedNamingTheRule is the
// pre-check the ticket asks to improve: creating a source on the same
// connection is refused with the rule to add, not only the engine's refusal.
func TestIssue1888_AWebhookSourceOnThatConnectionIsRefusedNamingTheRule(t *testing.T) {
	c := connect(t)
	conn := "acc1888-noproc-src-" + unique1805()
	issue1888Connection(t, c, conn, "mcp-scratch-no-procedures")

	source := issue1870Name("acc1888-refused")
	status, body := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, map[string]any{
		"name": source, "connection": conn,
		"auth":   map[string]any{"mode": "hmac", "secret": "whsec_1888", "signature_header": "X-Signature", "prefix": "sha256="},
		"config": map[string]any{"split": "$", "persona": "admin"},
	}))
	if status == http.StatusCreated {
		_, _ = c.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+source, http.NoBody)
		t.Fatalf("a source was created on a connection whose user may not call the partition procedures: %v", body)
	}
	detail, _ := body["detail"].(string)
	for _, want := range []string{
		"the connection cannot hold this source's table",
		"the Trino user of connection " + conn + " may not EXECUTE scratch_resources.system.",
		"add a procedures rule",
		"docs/server/scratch-catalog.md#access-control",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal (status %d) does not say %q:\n%v", status, want, body)
		}
	}
}
