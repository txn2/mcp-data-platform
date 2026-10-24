//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2276: an api connection's operation embeddings are held in each
// replica's memory, read when the replica builds the connection. The embed job
// runs on one replica, and only that replica reloaded the connection when it
// finished, so the other kept answering
//
//	ranking "hybrid" fell back to lexical: operation embeddings not indexed for this catalog
//
// until it restarted. Only a stack whose replicas built the connection before
// the job finished showed it, which is every fresh deployment and every spec
// added after start.
//
// What this holds, with two replicas over one database reached one at a time:
// both hold the connection before its spec is embedded, and once the
// embeddings are written, each ranks the connection's operations semantically.
//
// Wire forms: api_discover's `connection`, `query`, `ranking` and `purpose`
// are typed `string` in the tool schema, so each admits one JSON form and each
// is sent as literal tools/call params.

const issue2276Purpose = "Acceptance for #2276: every replica ranks with the embeddings a peer wrote."

// TestIssue2276_EveryReplicaRanksWithTheEmbeddingsAPeerWrote: a spec embedded
// after both replicas built the connection is ranked semantically on each.
func TestIssue2276_EveryReplicaRanksWithTheEmbeddingsAPeerWrote(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := fmt.Sprintf("acc-2276-%d", time.Now().UnixNano())
	if status := a.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Acceptance 2276",
		"description": "One spec over the api-test fixture for the #2276 acceptance run.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create catalog on %s: HTTP %d", a.base, status)
	}
	t.Cleanup(func() { a.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
	name := issue1746Register(t, a, "2276", catalogID)

	// Both replicas build the connection while its catalog holds nothing to
	// embed yet.
	for _, c := range []*client{a, b} {
		if res, text, err := c.callRaw("api_discover", map[string]any{"connection": name, "purpose": issue2276Purpose}); err != nil || res.IsError {
			t.Fatalf("%s did not serve the connection before its spec was added: %v %s", c.base, err, text)
		}
	}

	if status := a.restJSON(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/identity", map[string]any{
		"source_kind": "inline", "content": issue1592IdentitySpec,
	}); status != http.StatusCreated && status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("upsert spec on %s: HTTP %d", a.base, status)
	}
	requireEmbeddedCatalog1626(t, a, catalogID)

	for _, c := range []*client{a, b} {
		waitFor(t, c.base+" ranking the connection semantically", func() (bool, string) {
			out := c.call("api_discover", map[string]any{
				"connection": name, "query": "who am I", "ranking": "hybrid", "purpose": issue2276Purpose,
			})
			note, _ := out["note"].(string)
			return !strings.Contains(note, "fell back to lexical"), fmt.Sprintf("%v", out)
		})
	}
}
