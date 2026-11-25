//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2824: a connection created through one replica and deleted
// at once through the other is served by neither afterwards. The second
// replica read the store for the first replica's save, served the delete
// (which found nothing installed to remove), and then installed the deleted
// connection, which it kept serving: list_connections and platform_info's
// connection kinds answered from one replica named a connection the other did
// not have.
//
// Wire forms: the admin API takes a connection as a JSON object ({config,
// description}) on PUT and nothing on DELETE; list_connections is called with
// empty params ({}).

// TestIssue2824_AConnectionDeletedAsItIsSavedIsServedByNeitherReplica: each
// replica in turn saves a GraphQL connection and the other deletes it at once,
// five times; afterwards neither replica lists any of them.
func TestIssue2824_AConnectionDeletedAsItIsSavedIsServedByNeitherReplica(t *testing.T) {
	found := replicas(t)
	if len(found) < 2 {
		t.Fatalf("this criterion needs two replicas behind the proxy; found %d", len(found))
	}
	clients := make([]*client, len(found))
	for i, r := range found {
		clients[i] = connectAt(t, r.base, devAPIKey())
	}
	var names []string
	for round := range 5 {
		for i := range clients {
			saver, deleter := clients[i], clients[(i+1)%len(clients)]
			name := fmt.Sprintf("acc-2824-%d-%d-%d", round, i, time.Now().UnixNano())
			path := "/api/v1/admin/connection-instances/graphql/" + name
			status := saver.restJSON(http.MethodPut, path, map[string]any{
				"config":      map[string]any{"endpoint_url": issue1277Endpoint(), "connection_name": name, "connect_timeout": "10s"},
				"description": "Acceptance 2824",
			})
			if status != http.StatusCreated && status != http.StatusOK {
				t.Fatalf("saving %s: HTTP %d", name, status)
			}
			if status, body := deleter.rest(http.MethodDelete, path, http.NoBody); status >= http.StatusMultipleChoices {
				t.Fatalf("deleting %s: HTTP %d %v", name, status, body)
			}
			names = append(names, name)
		}
	}
	for i, c := range clients {
		var listed string
		deadline := time.Now().Add(30 * time.Second)
		for {
			listed = asJSON(c.call("list_connections", map[string]any{}))
			if !namesAny2824(listed, names) || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Second)
		}
		for _, name := range names {
			if strings.Contains(listed, `"`+name+`"`) {
				t.Errorf("replica %s still serves %s, deleted 30 seconds ago", found[i].name, name)
			}
		}
	}
}

// namesAny2824 reports whether s names any of names as a JSON string.
func namesAny2824(s string, names []string) bool {
	for _, n := range names {
		if strings.Contains(s, `"`+n+`"`) {
			return true
		}
	}
	return false
}
