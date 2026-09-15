//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1746: an `api` connection saved through the admin API was applied on
// the replica that served the request and reached the others over the reload
// bus. Until that message arrived, a call landing on another replica was told
//
//	connection "acc-..." not found (use list_connections to discover api connections)
//
// about a connection whose save had already returned. Nothing distinguished it
// from a genuine misconfiguration.
//
// What these hold, with two replicas over one database reached one at a time:
// the moment `PUT /api/v1/admin/connection-instances/api/{name}` returns on one
// replica, the other answers api_discover and api_invoke_endpoint on that
// connection, serves its browse routes, and reports its operations from the
// catalog it names. A connection nobody saved is still refused on both. A
// connection deleted on one replica is not taken back on by the other.
//
// Every criterion registers its own connection and asks the other replica on
// its FIRST request about it, because a replica that has once loaded a
// connection answers correctly from then on: the defect lives only there.
//
// Wire forms: every parameter these send is typed `string` or `integer` in the
// tool schemas — api_discover's `connection`/`query`/`spec`/`operation_id` and
// api_invoke_endpoint's `connection`/`method`/`path`/`purpose` — so each admits
// one JSON form and each is sent as literal tools/call params.
// api_invoke_endpoint's `body` admits an object and a string of JSON; it is not
// touched here (this ticket changes how a connection NAME resolves, not how a
// body is read), and the #1548 criteria cover both of its forms.

const issue1746Purpose = "Acceptance for #1746: an api connection saved on one replica is served by every replica."

// issue1746Register saves an api connection over the api-test fixture through
// c and returns its name. It does not wait for anything: the point of every
// criterion below is what the OTHER replica answers the instant this returns.
func issue1746Register(t *testing.T, c *client, label, catalogID string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1746-%s-%d", label, time.Now().UnixNano())
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config":      fixtureConnectionConfig(name, catalogID),
		"description": "Acceptance 1746: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the %s connection on %s: HTTP %d", label, c.base, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// issue1746Catalog creates a catalog over the api-test fixture and returns its
// id, so a connection registered against it has operations to report.
func issue1746Catalog(t *testing.T, c *client) string {
	t.Helper()
	catalogID := fmt.Sprintf("acc-1746-%d", time.Now().UnixNano())
	if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Acceptance 1746",
		"description": "One spec over the api-test fixture for the #1746 acceptance run.",
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

// TestIssue1746_TheOtherReplicaDiscoversAConnectionAtOnce is the shape an
// operator hit: add an API connection in the portal, use it immediately, and
// the first call is told the connection does not exist.
func TestIssue1746_TheOtherReplicaDiscoversAConnectionAtOnce(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := issue1746Catalog(t, a)
	name := issue1746Register(t, a, "discover", catalogID)

	res, text, err := b.callRaw("api_discover", map[string]any{
		"connection": name,
		"purpose":    issue1746Purpose,
	})
	if err != nil {
		t.Fatalf("api_discover through %s: transport error: %v", b.base, err)
	}
	if res.IsError {
		t.Fatalf("%s refused api_discover on a connection %s had already saved: %s", b.base, a.base, text)
	}
}

// TestIssue1746_TheOtherReplicaInvokesAConnectionAtOnce is the same window on
// the tool that actually reaches the upstream: the call must go through, not
// merely be recognized.
func TestIssue1746_TheOtherReplicaInvokesAConnectionAtOnce(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1746Register(t, a, "invoke", "")

	res, text, err := b.callRaw("api_invoke_endpoint", map[string]any{
		"connection": name,
		"method":     "GET",
		"path":       "/identity",
		"purpose":    issue1746Purpose,
	})
	if err != nil {
		t.Fatalf("api_invoke_endpoint through %s: transport error: %v", b.base, err)
	}
	if res.IsError {
		t.Fatalf("%s refused api_invoke_endpoint on a connection %s had already saved: %s", b.base, a.base, text)
	}
}

// TestIssue1746_TheOtherReplicaBrowsesAConnectionAtOnce covers the portal's
// own read of the same state: the connection page opened on either replica
// once the save has returned.
func TestIssue1746_TheOtherReplicaBrowsesAConnectionAtOnce(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := issue1746Catalog(t, a)
	name := issue1746Register(t, a, "browse", catalogID)

	status, body := b.rest(http.MethodGet, "/api/v1/apis/"+name+"/operations", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("%s answered GET /api/v1/apis/%s/operations with HTTP %d right after %s registered it: %v",
			b.base, name, status, a.base, body)
	}
	operations, _ := body["operations"].([]any)
	if len(operations) == 0 {
		t.Errorf("%s browsed the connection but reports none of its catalog's operations: %v", b.base, body)
	}
}

// TestIssue1746_TheOtherReplicaReportsTheCatalogsOperations holds that the
// connection the other replica takes on is the WHOLE connection: it resolves
// its catalog and reports the operations the saving replica reports, not an
// empty index that reads as a connection with nothing in it.
func TestIssue1746_TheOtherReplicaReportsTheCatalogsOperations(t *testing.T) {
	a, b := connectReplicaPair(t)
	catalogID := issue1746Catalog(t, a)
	name := issue1746Register(t, a, "operations", catalogID)

	other := issue1746Discover(t, b, name)
	registering := issue1746Discover(t, a, name)
	if len(other) == 0 {
		t.Fatalf("%s reports no operations for a catalog-backed connection %s had saved", b.base, a.base)
	}
	if len(other) != len(registering) {
		t.Errorf("%s reports %d operations; %s reports %d", b.base, len(other), a.base, len(registering))
	}
}

// issue1746Discover lists a connection's operations through one replica.
func issue1746Discover(t *testing.T, c *client, connection string) []any {
	t.Helper()
	out := c.call("api_discover", map[string]any{
		"connection": connection,
		"query":      "identity",
		"purpose":    issue1746Purpose,
	})
	ops, _ := out["operations"].([]any)
	return ops
}

// TestIssue1746_AConnectionNobodySavedIsStillRefused is the other half: the
// catch-up must not invent a connection. A name nothing saved is refused on
// both replicas, with the message that names how to find the real ones.
func TestIssue1746_AConnectionNobodySavedIsStillRefused(t *testing.T) {
	forEachReplica(t, func(t *testing.T, c *client) {
		name := fmt.Sprintf("acc-1746-absent-%d", time.Now().UnixNano())
		res, text, err := c.callRaw("api_discover", map[string]any{
			"connection": name,
			"purpose":    issue1746Purpose,
		})
		if err != nil {
			t.Fatalf("api_discover: transport error: %v", err)
		}
		if !res.IsError {
			t.Fatalf("%s answered api_discover for a connection nobody saved", c.base)
		}
		if !strings.Contains(text, "not found") {
			t.Errorf("the refusal does not say the connection is unknown: %s", text)
		}
	})
}

// TestIssue1746_AConnectionDeletedOnOneReplicaIsNotTakenOnByTheOther holds the
// delete side. A connection removed through one replica must not be read back
// out of the store by another and served again.
func TestIssue1746_AConnectionDeletedOnOneReplicaIsNotTakenOnByTheOther(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1746Register(t, a, "deleted", "")

	if status, body := a.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("deleting the connection on %s: HTTP %d %v", a.base, status, body)
	}

	res, text, err := b.callRaw("api_discover", map[string]any{
		"connection": name,
		"purpose":    issue1746Purpose,
	})
	if err != nil {
		t.Fatalf("api_discover through %s: transport error: %v", b.base, err)
	}
	if !res.IsError {
		t.Fatalf("%s served a connection %s had deleted: %s", b.base, a.base, text)
	}
}
