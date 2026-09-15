//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #1757: `list_connections` enumerated the connections the answering
// replica held. A connection saved through the admin API is applied on the
// replica that served the request and reaches the others over the reload bus,
// so until that announcement arrived the other replicas enumerated without it —
// an operator who added a connection and asked what exists was told a different
// answer depending on which replica the load balancer picked.
//
// #1746 closed the same window for every surface that resolves a connection BY
// NAME. An enumeration asks a different question — "what is there?" — and has no
// name to resolve, so it was never reached by that.
//
// What these hold, with two replicas over one database reached one at a time:
// the moment a save returns on one replica, the other names the connection in
// `list_connections` with the catalog and operation count that are facts about
// the connection rather than about a replica, lists it in the API browser the
// portal reads, and answers a tool call against it on a kind that has no
// catch-up of its own. A connection deleted on one replica stops being listed by
// the other, because the same rows answer in both directions.
//
// Every criterion registers its own connection and asks the OTHER replica on its
// first request about it: a replica that has once served a connection answers
// correctly from then on, so the defect lives only there.
//
// Wire forms: every parameter these send is typed `string` in the tool schemas —
// `list_connections`'s `purpose`, `s3_list`'s `connection` and `purpose` — so
// each admits one JSON form, and each is sent as literal tools/call params. The
// REST routes are read with no body.

const issue1757Purpose = "Acceptance for #1757: a connection saved on one replica is named by every replica."

// issue1757Register saves an api connection over the api-test fixture through c
// and returns its name. It waits for nothing: the point of every criterion is
// what the OTHER replica answers the instant this returns.
func issue1757Register(t *testing.T, c *client, label, catalogID string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1757-%s-%d", label, time.Now().UnixNano())
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config":      fixtureConnectionConfig(name, catalogID),
		"description": "Acceptance 1757: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the %s connection on %s: HTTP %d", label, c.base, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// issue1757Catalog creates a catalog over the api-test fixture and returns its
// id, so a connection registered against it has a surface to report.
func issue1757Catalog(t *testing.T, c *client) string {
	t.Helper()
	catalogID := fmt.Sprintf("acc-1757-%d", time.Now().UnixNano())
	if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Acceptance 1757",
		"description": "One spec over the api-test fixture for the #1757 acceptance run.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create catalog on %s: HTTP %d", c.base, status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/identity", map[string]any{
		"source_kind": "inline", "content": issue1592IdentitySpec,
	}); status != http.StatusCreated && status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("upsert spec on %s: HTTP %d", c.base, status)
	}
	return catalogID
}

// issue1757Listed is one row of the list_connections view, as far as these
// criteria read it.
type issue1757Listed struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Description    string `json:"description"`
	CatalogID      string `json:"catalog_id"`
	OperationCount int    `json:"operation_count"`
}

// issue1757List calls list_connections through c and returns its rows.
func issue1757List(t *testing.T, c *client) []issue1757Listed {
	t.Helper()
	out := c.call("list_connections", map[string]any{"purpose": issue1757Purpose})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("re-marshalling list_connections: %v", err)
	}
	var listed struct {
		Connections []issue1757Listed `json:"connections"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decoding list_connections: %v\n%s", err, raw)
	}
	if len(listed.Connections) == 0 {
		t.Fatalf("list_connections named nothing at all, so no criterion here proves anything: %s", raw)
	}
	return listed.Connections
}

// find returns the named row, or false.
func issue1757Find(rows []issue1757Listed, name string) (issue1757Listed, bool) {
	for _, row := range rows {
		if row.Name == name {
			return row, true
		}
	}
	return issue1757Listed{}, false
}

// TestIssue1757_TheOtherReplicaNamesAConnectionAtOnce is the shape an operator
// hit: add a connection in the portal, ask what connections exist, and be told
// about a deployment that does not include the one just added.
func TestIssue1757_TheOtherReplicaNamesAConnectionAtOnce(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := issue1757Catalog(t, a)
	name := issue1757Register(t, a, "named", catalogID)

	row, found := issue1757Find(issue1757List(t, b), name)
	if !found {
		t.Fatalf("%s does not name %s, which %s saved before this call was made", b.base, name, a.base)
	}
	if row.Kind != "api" {
		t.Errorf("%s reports kind %q for %s; want api", b.base, row.Kind, name)
	}
	if row.Description != "Acceptance 1757: named" {
		t.Errorf("%s reports description %q for %s; want the saved one", b.base, row.Description, name)
	}
}

// TestIssue1757_TheOtherReplicaReportsTheConnectionsSurface: a listing that
// named the connection but reported it as empty would send a caller looking
// elsewhere. The catalog and its operation count are facts about the
// connection, not about the replica that answered.
func TestIssue1757_TheOtherReplicaReportsTheConnectionsSurface(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := issue1757Catalog(t, a)
	name := issue1757Register(t, a, "surface", catalogID)

	row, found := issue1757Find(issue1757List(t, b), name)
	if !found {
		t.Fatalf("%s does not name %s at all", b.base, name)
	}
	if row.CatalogID != catalogID {
		t.Errorf("%s reports catalog_id %q for %s; want %q", b.base, row.CatalogID, name, catalogID)
	}
	if row.OperationCount == 0 {
		t.Errorf("%s reports no operations for %s; want the catalog's", b.base, name)
	}
}

// TestIssue1757_TheOtherReplicaListsTheConnectionInTheAPIBrowser holds the
// portal's half: GET /api/v1/apis enumerates through the same view, so the page
// an operator opens after saving must show what they just saved.
func TestIssue1757_TheOtherReplicaListsTheConnectionInTheAPIBrowser(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := issue1757Catalog(t, a)
	name := issue1757Register(t, a, "browsed", catalogID)

	status, out := b.rest(http.MethodGet, "/api/v1/apis", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/apis on %s: HTTP %d", b.base, status)
	}
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("re-marshalling the API browser listing: %v", err)
	}
	if !strings.Contains(string(body), name) {
		t.Errorf("the API browser on %s does not list %s, which %s saved: %s", b.base, name, a.base, body)
	}
}

// TestIssue1757_AConnectionDeletedOnOneReplicaIsNotNamedByTheOther is the other
// direction, and the one a store-backed enumeration could get wrong: the rows
// are the inventory, so a connection removed from them stops being listed even
// where the replica still holds one.
func TestIssue1757_AConnectionDeletedOnOneReplicaIsNotNamedByTheOther(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := issue1757Catalog(t, a)
	name := issue1757Register(t, a, "deleted", catalogID)

	// Serve it on b first, so this proves the deletion is honored rather than
	// that b never knew about the connection.
	if _, found := issue1757Find(issue1757List(t, b), name); !found {
		t.Fatalf("%s does not name %s before the delete, so the delete proves nothing", b.base, name)
	}
	if status, _ := a.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody); status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("deleting %s on %s: HTTP %d", name, a.base, status)
	}

	if _, found := issue1757Find(issue1757List(t, b), name); found {
		t.Errorf("%s still names %s after %s deleted it", b.base, name, a.base)
	}
}

// issue1757S3Endpoint is the dev stack's SeaweedFS, which dev/start.sh relocates
// when the default port is busy and records in dev/.dev-ports.env; make
// acceptance exports it. It fails rather than skips: the fixture is part of the
// stack this suite runs against.
func issue1757S3Endpoint() string {
	port := os.Getenv("DEV_S3_PORT")
	if port == "" {
		port = "9000"
	}
	return "http://localhost:" + port
}

// TestIssue1757_TheOtherReplicaCallsAKindWithNoCatchUpOfItsOwn is the by-name
// half on a kind that never had one. api and graphql read the connection store
// inside their own toolkits (#1746, #1714); s3's tool handlers are the upstream
// toolkit's, so its window was closed in the one place every tool call passes
// through. A call against a connection saved a moment ago on another replica is
// answered, not refused as a connection that does not exist.
func TestIssue1757_TheOtherReplicaCallsAKindWithNoCatchUpOfItsOwn(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := fmt.Sprintf("acc-1757-s3-%d", time.Now().UnixNano())
	status := a.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/s3/"+name, map[string]any{
		"config": map[string]any{
			"endpoint": issue1757S3Endpoint(), "region": "us-east-1",
			"access_key_id": "dev-access-key", "secret_access_key": "dev-secret-key",
			"use_path_style": true,
		},
		"description": "Acceptance 1757: a kind with no catch-up of its own",
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the s3 connection on %s: HTTP %d", a.base, status)
	}
	t.Cleanup(func() {
		a.rest(http.MethodDelete, "/api/v1/admin/connection-instances/s3/"+name, http.NoBody)
	})

	res, text, err := b.callRaw("s3_list", map[string]any{
		"connection": name,
		"purpose":    issue1757Purpose,
	})
	if err != nil {
		t.Fatalf("s3_list through %s: transport error: %v", b.base, err)
	}
	if res.IsError {
		t.Fatalf("%s refused s3_list on %s, which %s saved before this call was made: %s",
			b.base, name, a.base, text)
	}
}
