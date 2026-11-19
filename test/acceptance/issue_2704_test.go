//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance for #2704: a connection saved through one replica reached the
// other twice, as a stored row its first request took on and as the save's
// announcement after it, and the announcement replaced the connection the
// replica already served, closing the client a call in flight was using
// (`sql: database is closed`, the E2E nightly of #2703). An announcement of
// the configuration a replica already serves now changes nothing, and a
// changed configuration still replaces the connection.
//
// Each criterion saves through one replica and then calls through the other
// at once and for the announcement window after it, so the announcement
// lands while the other replica is serving calls on the connection it took
// on. The GraphQL half of the ticket (a schema uploaded while the other
// replica applies the announcement) is TestIssue1714_AnUploadThroughOneReplica
// IsServedByTheOtherAtOnce, run twenty times over against this change.
//
// Wire forms: the connection routes take kind and name in the path and a JSON
// object body whose config is a literal object; trino_query takes connection,
// sql and purpose as strings; the proxied echo tool takes message and purpose
// as strings. No parameter admits a second form.

// announcementWindow is how long a criterion keeps calling after a save: past
// the time the reload bus takes to deliver it on the dev stack.
const announcementWindow = 8 * time.Second

// trino2704 stores a Trino connection as user through c and returns its name.
func trino2704(t *testing.T, c *client, name, user string) {
	t.Helper()
	host, port := issue1888Trino()
	path := "/api/v1/admin/connection-instances/trino/" + name
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"description": "Acceptance #2704: a connection saved through one replica and used through the other.",
		"config": map[string]any{
			"host": host, "port": port, "ssl": false, "user": user,
			"catalog": "scratch_resources", "schema": "uploads", "read_only": true,
		},
	}))
	if status != http.StatusOK {
		t.Fatalf("storing connection %s answered %d: %v", name, status, body)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, path, http.NoBody) })
}

// currentUser2704 is who the connection's next query runs as, through c, or
// the tool's refusal.
func currentUser2704(c *client, name string) (user, refusal string) {
	res, text, err := c.callRaw("trino_query", map[string]any{
		"connection": name, "sql": "SELECT current_user AS u",
		"purpose": "Acceptance #2704: the connection answers on the replica that did not take the save.",
	})
	switch {
	case err != nil:
		return "", err.Error()
	case res.IsError:
		return "", text
	}
	i := strings.Index(text, `"u"`)
	if i < 0 {
		return "", "no user in " + text
	}
	rest := text[i:]
	for _, u := range []string{"mcp-scratch-no-procedures", "mcp-scratch"} {
		if strings.Contains(rest, `"`+u+`"`) {
			return u, ""
		}
	}
	return "", "an unexpected user in " + text
}

// TestIssue2704_ATrinoConnectionKeepsAnsweringThroughTheAnnouncement: the
// connection test and trino_query through the replica that did not take the
// save succeed at once and through the announcement, with no
// "database is closed".
func TestIssue2704_ATrinoConnectionKeepsAnsweringThroughTheAnnouncement(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := "acc2704-trino-" + unique1805()
	trino2704(t, a, name, "mcp-scratch")
	path := "/api/v1/admin/connection-instances/trino/" + name
	if status, result := b.rest(http.MethodPost, path+"/test", http.NoBody); status != http.StatusOK {
		t.Fatalf("testing the connection on the other replica answered %d: %v", status, result)
	}
	calls := 0
	for end := time.Now().Add(announcementWindow); time.Now().Before(end); calls++ {
		if user, refusal := currentUser2704(b, name); user != "mcp-scratch" {
			t.Fatalf("call %d through the other replica failed while the announcement landed: %s", calls+1, refusal)
		}
	}
	t.Logf("%d calls through the other replica answered", calls)
}

// TestIssue2704_AChangedConfigurationStillReplacesTheConnection: saving a
// different user through one replica is the user the other replica's next
// queries run as, once the announcement reaches it.
func TestIssue2704_AChangedConfigurationStillReplacesTheConnection(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := "acc2704-changed-" + unique1805()
	trino2704(t, a, name, "mcp-scratch")
	if user, refusal := currentUser2704(b, name); user != "mcp-scratch" {
		t.Fatalf("the first query through the other replica: %s", refusal)
	}
	trino2704(t, a, name, "mcp-scratch-no-procedures")
	deadline := time.Now().Add(30 * time.Second)
	for {
		user, refusal := currentUser2704(b, name)
		if user == "mcp-scratch-no-procedures" {
			return
		}
		if refusal != "" && strings.Contains(refusal, "database is closed") {
			t.Fatalf("a query failed while the changed connection replaced the old one: %s", refusal)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the other replica still queries as %q after the change (%s)", user, refusal)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue2704_AGatewayConnectionsToolsStayThroughTheAnnouncement: an MCP
// connection saved through one replica keeps its tools listed on the other,
// and a call through them answers, while the announcement lands.
func TestIssue2704_AGatewayConnectionsToolsStayThroughTheAnnouncement(t *testing.T) {
	a, b := connectReplicaPair(t)
	stamp := strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	name := "acc2704gw" + stamp
	path := "/api/v1/admin/connection-instances/mcp/" + name
	t.Cleanup(func() { _, _ = a.rest(http.MethodDelete, path, http.NoBody) })
	status, body := a.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"endpoint": mcpTestFixture1967, "auth_mode": "api_key", "credential": fixtureKey1967(),
			"connection_name": name, "connect_timeout": "5s", "call_timeout": "10s",
		},
		"description": "Acceptance #2704: a gateway connection saved through one replica and used through the other.",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("PUT %s: HTTP %d: %v", path, status, body)
	}
	echo := name + "__echo"
	ready := time.Now().Add(30 * time.Second)
	for {
		res, _, err := b.callRaw(echo, map[string]any{"message": "ready", "purpose": "Acceptance #2704: the proxied tool answers."})
		if err == nil && !res.IsError {
			break
		}
		if time.Now().After(ready) {
			t.Fatalf("%s never answered on the other replica after the save", echo)
		}
		time.Sleep(250 * time.Millisecond)
	}
	for i, end := 0, time.Now().Add(announcementWindow); time.Now().Before(end); i++ {
		msg := fmt.Sprintf("call %d", i)
		res, text, err := b.callRaw(echo, map[string]any{"message": msg, "purpose": "Acceptance #2704: the proxied tool answers."})
		if err != nil || res.IsError || !strings.Contains(text, msg) {
			t.Fatalf("%s through the other replica failed while the announcement landed: %v %s", echo, err, text)
		}
		if !slices.ContainsFunc(b.tools(), func(tool *mcp.Tool) bool { return tool.Name == echo }) {
			t.Fatalf("%s left the other replica's tool list while the announcement landed", echo)
		}
	}
}
