//go:build integration

package acceptance

// Issue #1703: a GraphQL schema re-read the endpoint refused was recorded in
// the memory of the replica that ran it. GET .../schema answered the refusal
// from that replica only, and a restart forgot it while the schema survived.
//
// Every criterion runs against two replicas of the platform over one database
// (the two behind the dev stack's proxy, or MCP_BASE_URL and
// MCP_PEER_BASE_URL; see replicas), with a connection whose endpoint
// refuses the introspection query the way the ticket's did: HTTP 302 and no
// GraphQL body, from the forwarder #1676's criteria put in front of DataHub's
// GMS. The re-read is sent to one replica only, which is the path the ticket
// was reported on; a configuration save re-reads on every replica and would
// hide the defect.
//
// Wire forms: `POST .../refresh-schema` takes an empty body (a re-read), SDL,
// or a saved introspection result. The re-read is the form this ticket is
// about and is sent as an empty body; the schema it is refused beside is
// uploaded in both other forms
// (TestIssue1703_ARefusedRereadOnOneReplicaIsReportedByEveryReplica), and an
// upload in each form clears the refusal on both replicas
// (TestIssue1703_AnUploadAfterARefusalClearsItOnEveryReplica).

import (
	"net/http"
	"strings"
	"testing"
)

// issue1703Forms are the two bodies that supply a schema.
var issue1703Forms = map[string]string{"sdl": issue1676SDL, "introspection": issue1676Introspection}

// issue1703Reread asks one replica to re-read the schema from the endpoint and
// returns what the route answered.
func issue1703Reread(t *testing.T, c *client, name string) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema", http.NoBody)
}

// issue1703Uploaded registers a connection on replica A, uploads the schema in
// one form, and waits until replica B serves it with nothing beside it.
func issue1703Uploaded(t *testing.T, a, b *client, label, payload string) (name, hash string, count float64) {
	t.Helper()
	name, _ = issue1676Connect(t, a, label, issue1676Upstream(t))
	uploaded := issue1676Upload(t, a, name, payload)
	_, hash, _, count = issue1676Fields(uploaded)
	if hash == "" || count != 1 {
		t.Fatalf("the upload did not install a schema with its one operation: %v", uploaded)
	}
	clean := func(info map[string]any) bool {
		h, _ := info["schema_hash"].(string)
		e, _ := info["error"].(string)
		return h == hash && e == ""
	}
	issue1676WaitSchema(t, a, name, clean)
	issue1676WaitSchema(t, b, name, clean)
	return name, hash, count
}

// TestIssue1703_ARefusedRereadOnOneReplicaIsReportedByEveryReplica is the
// ticket's first expectation: a re-read the endpoint refuses, run on one
// replica, is reported by GET .../schema from every replica, beside the schema
// that survived it.
func TestIssue1703_ARefusedRereadOnOneReplicaIsReportedByEveryReplica(t *testing.T) {
	for form, payload := range issue1703Forms {
		t.Run(form, func(t *testing.T) {
			a, b := connectReplicaPair(t)
			name, hash, count := issue1703Uploaded(t, a, b, "reread-"+form, payload)

			status, answered := issue1703Reread(t, a, name)
			if status != http.StatusOK {
				t.Fatalf("the re-read answered HTTP %d: %v", status, answered)
			}
			issue1676AssertUpload(t, "replica A (the refresh answer)", answered, hash, count, true)

			refused := func(info map[string]any) bool { e, _ := info["error"].(string); return strings.Contains(e, "302") }
			issue1676AssertUpload(t, "replica B", issue1676WaitSchema(t, b, name, refused), hash, count, true)

			// Repeated reads of either replica agree, which is the ticket's
			// observation turned around: which answer a reader got depended on
			// which replica served the request.
			for _, replica := range []*client{a, b, a, b, a, b} {
				info := issue1676Schema(t, replica, name)
				issue1676AssertUpload(t, replica.base, info, hash, count, true)
				if got, _ := info["error"].(string); got != answered["error"] {
					t.Errorf("%s reports %q; the replica that ran the re-read reported %q", replica.base, got, answered["error"])
				}
			}
		})
	}
}

// TestIssue1703_AnUploadAfterARefusalClearsItOnEveryReplica: the refusal is
// state of the stored schema, so a schema installed afterwards, in either
// form, is reported with nothing beside it on both replicas. Without it the
// record would be a refusal no later success could retract.
func TestIssue1703_AnUploadAfterARefusalClearsItOnEveryReplica(t *testing.T) {
	for form, payload := range issue1703Forms {
		t.Run(form, func(t *testing.T) {
			a, b := connectReplicaPair(t)
			name, hash, count := issue1703Uploaded(t, a, b, "clear-"+form, payload)

			if status, answered := issue1703Reread(t, b, name); status != http.StatusOK {
				t.Fatalf("the re-read on replica B answered HTTP %d: %v", status, answered)
			}
			refused := func(info map[string]any) bool { e, _ := info["error"].(string); return strings.Contains(e, "302") }
			issue1676WaitSchema(t, a, name, refused)

			issue1676Upload(t, a, name, payload)
			clean := func(info map[string]any) bool { e, _ := info["error"].(string); return e == "" }
			issue1676AssertUpload(t, "replica A", issue1676WaitSchema(t, a, name, clean), hash, count, false)
			issue1676AssertUpload(t, "replica B", issue1676WaitSchema(t, b, name, clean), hash, count, false)
		})
	}
}
