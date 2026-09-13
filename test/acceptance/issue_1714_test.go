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

// Issue #1714: a GraphQL connection registered through one replica was
// unknown to the other replicas until the reload event reached them and they
// had read the endpoint themselves. In that window the other replica's
// GET .../schema answered 404 or reported no operations, and graphql_discover,
// graphql_query and graphql_export were refused with "the platform could not
// read one from its endpoint", a read that had not happened.
//
// What these hold, with two replicas over one database reached one at a time:
// the moment `PUT /api/v1/admin/connection-instances/graphql/{name}` returns on
// one replica, the other reports the schema the first one read, serves every
// graphql tool on the connection, and takes a re-read of it; a refusal it gives
// names the read that failed. The same holds for a schema changed after the
// registration: an upload through one replica is served by the other on the
// next request, and a re-read refused on one is reported identically by both.
// A deleted connection is not taken back on from the store.
//
// Every criterion registers its own connection and asks the other replica on
// the next request, because a replica that has once loaded a connection answers
// correctly from then on: the defect lives only in the first request.
//
// Wire forms: graphql_query's and graphql_export's `variables` is typed
// ["object", "string"]; both forms are sent as literal tools/call params to
// each tool, each against a connection the answering replica has not yet been
// asked about (TestIssue1714_TheOtherReplicaServesEveryGraphQLToolAtOnce).
// `connection`, `query` and `name` are strings only. The refresh route is sent
// all three bodies it takes: empty (a re-read), SDL and an introspection result
// (TestIssue1714_TheOtherReplicaTakesAReReadAtOnce,
// TestIssue1714_AnUploadThroughOneReplicaIsServedByTheOtherAtOnce).

const issue1714Purpose = "Acceptance for #1714: a graphql connection registered on one replica is served by every replica."

// issue1714SchemaPath is the schema state route for one connection.
func issue1714SchemaPath(name string) string {
	return "/api/v1/admin/connection-instances/graphql/" + name + "/schema"
}

// issue1714Register registers a connection on c, against the repository's
// real GraphQL endpoint unless endpoint names another, and returns its name.
func issue1714Register(t *testing.T, c *client, label, endpoint string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1714-%s-%d", label, time.Now().UnixNano())
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/graphql/"+name, map[string]any{
		"config": map[string]any{
			"endpoint_url":    endpoint,
			"connection_name": name,
			"connect_timeout": "10s",
			"call_timeout":    "30s",
		},
		"description": "Acceptance 1714: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the %s connection on %s: HTTP %d", label, c.base, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/graphql/"+name, http.NoBody)
	})
	return name
}

// issue1714Call calls a tool through c and returns its JSON result, failing t
// rather than the test c was opened on, so one subtest's failure does not stop
// the others.
func issue1714Call(t *testing.T, c *client, tool string, args map[string]any) map[string]any {
	t.Helper()
	res, text, err := c.callRaw(tool, args)
	if err != nil {
		t.Fatalf("%s through %s: transport error: %v", tool, c.base, err)
	}
	if res.IsError {
		t.Fatalf("%s through %s was refused: %s", tool, c.base, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s through %s: result is not a JSON object: %v\n%s", tool, c.base, err, text)
	}
	return out
}

// TestIssue1714_TheOtherReplicaReportsTheSchemaTheRegisteringReplicaRead is
// the Schema card a person opens after saving a connection behind a load
// balancer: the replica that answers holds the schema the save read.
func TestIssue1714_TheOtherReplicaReportsTheSchemaTheRegisteringReplicaRead(t *testing.T) {
	requireGraphQLUpstream(t)
	a, b := connectReplicaPair(t)
	name := issue1714Register(t, a, "state", issue1277Endpoint())

	status, other := b.rest(http.MethodGet, issue1714SchemaPath(name), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("%s answered GET .../schema with HTTP %d right after %s registered the connection: %v",
			b.base, status, a.base, other)
	}
	registering := issue1676Schema(t, a, name)
	_, wantHash, _, wantCount := issue1676Fields(registering)
	if wantHash == "" || wantCount == 0 {
		t.Fatalf("the registering replica read no schema from %s, so this criterion proves nothing: %v",
			issue1277Endpoint(), registering)
	}
	_, hash, schemaErr, count := issue1676Fields(other)
	if hash != wantHash || count != wantCount {
		t.Errorf("%s reports schema_hash %q with %v operations; %s read %q with %v: %v",
			b.base, hash, count, a.base, wantHash, wantCount, other)
	}
	if schemaErr != "" {
		t.Errorf("%s reports a failed read that did not happen: %v", b.base, other)
	}
}

// TestIssue1714_TheOtherReplicaServesEveryGraphQLToolAtOnce calls each tool,
// in each form its parameters admit, on the other replica as its first request
// about a freshly registered connection.
func TestIssue1714_TheOtherReplicaServesEveryGraphQLToolAtOnce(t *testing.T) {
	requireGraphQLUpstream(t)
	a, b := connectReplicaPair(t)

	t.Run("graphql_discover", func(t *testing.T) {
		name := issue1714Register(t, a, "discover", issue1277Endpoint())
		out := issue1714Call(t, b, issue1277DiscoverTool, map[string]any{
			"connection": name, "query": "dataset", "purpose": issue1714Purpose,
		})
		if ids := issue1277Operations(t, out); len(ids) == 0 {
			t.Errorf("%s listed no operations on a connection %s had read: %v", b.base, a.base, out)
		}
	})

	forms := map[string]any{
		"object": issue1675Variables(),
		"string": issue1675VariablesJSON(t),
	}
	for form, variables := range forms {
		t.Run("graphql_query/"+form, func(t *testing.T) {
			name := issue1714Register(t, a, "query-"+form, issue1277Endpoint())
			out := issue1714Call(t, b, issue1277QueryTool, map[string]any{
				"connection": name, "query": issue1675Document, "variables": variables,
				"purpose": issue1714Purpose,
			})
			if out["upstream_error"] == true {
				t.Fatalf("the endpoint refused the document through %s: %v", b.base, out["errors"])
			}
			if data, _ := out["data"].(map[string]any); data == nil {
				t.Errorf("%s returned no data: %v", b.base, out)
			}
		})
		t.Run("graphql_export/"+form, func(t *testing.T) {
			name := issue1714Register(t, a, "export-"+form, issue1277Endpoint())
			out := issue1714Call(t, b, issue1675ExportTool, map[string]any{
				"connection": name, "query": issue1675Document, "variables": variables,
				"name":    "acc-1714-" + form + "-" + name,
				"tags":    []any{"acceptance", "issue-1714"},
				"purpose": issue1714Purpose,
			})
			if id, _ := out["asset_id"].(string); id == "" {
				t.Errorf("%s created no asset: %v", b.base, out)
			}
		})
	}
}

// TestIssue1714_TheOtherReplicaTakesAReReadAtOnce is the Schema card's
// Re-read button pressed on the replica that did not take the save.
func TestIssue1714_TheOtherReplicaTakesAReReadAtOnce(t *testing.T) {
	requireGraphQLUpstream(t)
	a, b := connectReplicaPair(t)
	name := issue1714Register(t, a, "reread", issue1277Endpoint())

	status, info := b.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("%s answered the re-read with HTTP %d right after %s registered the connection: %v",
			b.base, status, a.base, info)
	}
	source, hash, schemaErr, _ := issue1676Fields(info)
	if hash == "" || source != "introspection" || schemaErr != "" {
		t.Errorf("%s re-read the endpoint and reports %v", b.base, info)
	}
}

// TestIssue1714_ARefusalOnTheOtherReplicaNamesTheReadThatFailed registers a
// connection whose endpoint refuses introspection. Both replicas hold no
// schema, and the refusal the other replica gives on its first request names
// the endpoint's answer, as the registering replica's does.
func TestIssue1714_ARefusalOnTheOtherReplicaNamesTheReadThatFailed(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1714Register(t, a, "refused", issue1676Upstream(t))

	refusal := issue1277Refuse(t, b, issue1277DiscoverTool, map[string]any{"connection": name})
	if !strings.Contains(refusal, "HTTP 302 to the introspection query") {
		t.Errorf("%s refused graphql_discover without naming the endpoint's answer: %s", b.base, refusal)
	}
	issue1676AssertNoSchema(t, b.base, issue1676Schema(t, b, name))
	issue1676AssertNoSchema(t, a.base, issue1676Schema(t, a, name))
}

// TestIssue1714_AnUploadThroughOneReplicaIsServedByTheOtherAtOnce: a schema
// supplied through one replica is what the other reports and discovers from on
// the next request, in both forms an upload takes. Each upload goes to a
// connection whose schema both replicas already agree on, so what the other
// replica answers can only be the upload or what it held before.
func TestIssue1714_AnUploadThroughOneReplicaIsServedByTheOtherAtOnce(t *testing.T) {
	a, b := connectReplicaPair(t)
	for form, payload := range issue1703Forms {
		t.Run(form, func(t *testing.T) {
			name := issue1714Register(t, a, "upload-"+form, issue1676Upstream(t))
			issue1676WaitSchema(t, b, name, func(info map[string]any) bool {
				e, _ := info["error"].(string)
				return strings.Contains(e, "302")
			})

			uploaded := issue1676Upload(t, a, name, payload)
			_, hash, _, count := issue1676Fields(uploaded)
			if hash == "" || count != 1 {
				t.Fatalf("the upload through %s installed no schema: %v", a.base, uploaded)
			}

			issue1676AssertUpload(t, b.base, issue1676Schema(t, b, name), hash, count, false)
			out := issue1714Call(t, b, issue1277DiscoverTool, map[string]any{"connection": name, "purpose": issue1714Purpose})
			if got, _ := out["schema_hash"].(string); got != hash {
				t.Errorf("%s discovered from schema %q; %s had just installed %q: %v", b.base, got, a.base, hash, out)
			}
		})
	}
}

// TestIssue1714_ARefusedReReadIsReportedTheSameWayByBothReplicasAtOnce is the
// Schema card's Re-read pressed on one replica right after an upload through
// the other, and the card read again through the first: the refusal is
// recorded beside the uploaded schema, and both replicas report exactly that.
func TestIssue1714_ARefusedReReadIsReportedTheSameWayByBothReplicasAtOnce(t *testing.T) {
	a, b := connectReplicaPair(t)
	name := issue1714Register(t, a, "reread-refused", issue1676Upstream(t))
	issue1676WaitSchema(t, b, name, func(info map[string]any) bool {
		e, _ := info["error"].(string)
		return strings.Contains(e, "302")
	})

	uploaded := issue1676Upload(t, a, name, issue1676SDL)
	_, hash, _, count := issue1676Fields(uploaded)

	status, answered := b.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("%s answered the re-read with HTTP %d: %v", b.base, status, answered)
	}
	issue1676AssertUpload(t, b.base+" (the re-read's answer)", answered, hash, count, true)

	reported := issue1676Schema(t, a, name)
	issue1676AssertUpload(t, a.base, reported, hash, count, true)
	if reported["error"] != answered["error"] {
		t.Errorf("%s reports %q; %s answered the re-read with %q", a.base, reported["error"], b.base, answered["error"])
	}
}

// TestIssue1714_ADeletedConnectionIsNotTakenBackOnFromTheStore is the control
// on answering from the connection store: once a deletion has reached a
// replica, a request there for the deleted connection finds nothing to take
// on, however it asks.
func TestIssue1714_ADeletedConnectionIsNotTakenBackOnFromTheStore(t *testing.T) {
	requireGraphQLUpstream(t)
	a, b := connectReplicaPair(t)
	name := issue1714Register(t, a, "deleted", issue1277Endpoint())
	issue1676Schema(t, b, name)
	if status, body := a.rest(http.MethodDelete, "/api/v1/admin/connection-instances/graphql/"+name, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("deleting %s on %s: HTTP %d %v", name, a.base, status, body)
	}

	deadline := time.Now().Add(issue1676Settle)
	for {
		status, _ := b.rest(http.MethodGet, issue1714SchemaPath(name), http.NoBody)
		if status == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still served %s %s after it was deleted", b.base, name, issue1676Settle)
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, replica := range []*client{b, a, b, a} {
		refusal := issue1277Refuse(t, replica, issue1277DiscoverTool, map[string]any{"connection": name})
		if !strings.Contains(refusal, "not found") {
			t.Errorf("%s did not report a deleted connection as not found: %s", replica.base, refusal)
		}
		if status, info := replica.rest(http.MethodGet, issue1714SchemaPath(name), http.NoBody); status != http.StatusNotFound {
			t.Errorf("%s answered GET .../schema for a deleted connection with HTTP %d: %v", replica.base, status, info)
		}
	}
}
