//go:build integration

package acceptance

// Issue #1676: a graphql connection lost its uploaded schema on every
// configuration save whose re-read failed, and two replicas of one deployment
// answered different schema state for one connection.
//
// Every criterion runs against two replicas of the platform over one database
// (the two behind the dev stack's proxy, or MCP_BASE_URL and
// MCP_PEER_BASE_URL; see replicas), with a
// connection whose endpoint refuses the introspection query the way the
// ticket's did: HTTP 302 and no GraphQL body. The endpoint is the real GraphQL
// server #1277's criteria run against, DataHub's GMS, reached through a
// forwarder the test starts that answers the introspection query itself and
// hands every other document to the server unchanged. The forwarder is the
// firewall in the ticket; every answer a document gets is the server's.
//
// Wire forms: `POST .../refresh-schema` takes the schema as SDL or as a saved
// introspection result. Both forms are uploaded, and each is carried through a
// failed re-read on both replicas
// (TestIssue1676_AConfigurationSaveKeepsTheUploadedSchemaOnEveryReplica).
// `graphql_query.variables` is typed ["object", "string"]; both forms are sent
// on both replicas and assert the same dotted operation
// (TestIssue1676_ANamespacedDocumentReportsTheDottedOperationFromEveryReplica).
// `PUT .../connection-instances/graphql/{name}` takes one body shape (`config`
// and `description`), and `read_only` inside it is a boolean.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	issue1676Purpose = "Acceptance for #1676: a graphql connection's stored schema survives a failed re-read and reaches every replica."
	// issue1676DottedOperation is the one operation the uploaded schema
	// exposes, under the namespace the document reaches it through.
	issue1676DottedOperation = "appConfig.analyticsConfig"
	// issue1676Settle bounds how long a replica is given to apply a peer's
	// reload broadcast. The bus is PostgreSQL LISTEN/NOTIFY and arrives
	// within a second; the bound is generous so a loaded machine does not
	// fail a criterion about consistency on a timing.
	issue1676Settle = 20 * time.Second
)

// issue1676SDL is the schema an operator uploads: a namespaced subset of the
// real server's, so a document written against it runs there. appConfig holds
// no scalar of its own, which makes analyticsConfig the only operation and
// appConfig the namespace on the way to it.
const issue1676SDL = `
schema { query: Query }
type Query {
  "The platform's configuration."
  appConfig: AppConfig
}
type AppConfig {
  "Analytics settings."
  analyticsConfig: AnalyticsConfig
}
type AnalyticsConfig {
  enabled: Boolean!
}
`

// issue1676Introspection is the same schema as a saved introspection result,
// the other form the upload route accepts.
const issue1676Introspection = `{"data":{"__schema":{
  "queryType": {"name": "Query"},
  "types": [
    {"kind": "OBJECT", "name": "Query", "fields": [
      {"name": "appConfig", "description": "The platform's configuration.", "args": [],
       "type": {"kind": "OBJECT", "name": "AppConfig", "ofType": null}, "isDeprecated": false}
    ]},
    {"kind": "OBJECT", "name": "AppConfig", "fields": [
      {"name": "analyticsConfig", "description": "Analytics settings.", "args": [],
       "type": {"kind": "OBJECT", "name": "AnalyticsConfig", "ofType": null}, "isDeprecated": false}
    ]},
    {"kind": "OBJECT", "name": "AnalyticsConfig", "fields": [
      {"name": "enabled", "args": [],
       "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "Boolean", "ofType": null}},
       "isDeprecated": false}
    ]},
    {"kind": "SCALAR", "name": "Boolean"}
  ],
  "directives": []
}}}`

// issue1676Document reaches the operation through its namespace. The
// variable gives both wire forms of `variables` something the server acts on.
const issue1676Document = `query Acc1676($all: Boolean!) { appConfig { analyticsConfig { enabled @include(if: $all) } } }`

// issue1676Upstream starts the forwarder and returns its URL. The
// introspection query is answered HTTP 302 with no GraphQL body, which is
// the refusal the ticket was reported against; every other document is sent
// to the real server and its answer returned unchanged.
func issue1676Upstream(t *testing.T) string {
	t.Helper()
	requireGraphQLUpstream(t)
	target := issue1277Endpoint()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if bytes.Contains(raw, []byte("__schema")) {
			w.Header().Set("Location", "/login")
			w.WriteHeader(http.StatusFound)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(raw))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close() //nolint:errcheck // best-effort close
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// issue1676Connect registers one graphql connection against the forwarder on
// the first replica and returns its name and the config it was saved with,
// which a later save sends back with one key changed.
func issue1676Connect(t *testing.T, c *client, label, endpoint string) (name string, cfg map[string]any) {
	t.Helper()
	name = fmt.Sprintf("acc-1676-%s-%d", label, time.Now().UnixNano())
	cfg = map[string]any{
		"endpoint_url":    endpoint,
		"connection_name": name,
		"connect_timeout": "10s",
		"call_timeout":    "30s",
	}
	issue1676Save(t, c, name, cfg, http.StatusCreated, http.StatusOK)
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/graphql/"+name, http.NoBody)
	})
	return name, cfg
}

// issue1676Save writes a connection's configuration through the admin API.
func issue1676Save(t *testing.T, c *client, name string, cfg map[string]any, want ...int) {
	t.Helper()
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/graphql/"+name, map[string]any{
		"config":      cfg,
		"description": "Acceptance 1676",
	})
	for _, ok := range want {
		if status == ok {
			return
		}
	}
	t.Fatalf("saving connection %s: HTTP %d", name, status)
}

// issue1676Schema reads what one replica holds for a connection.
func issue1676Schema(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, info := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/graphql/"+name+"/schema", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("%s: GET schema answered HTTP %d: %v", c.base, status, info)
	}
	return info
}

// issue1676WaitSchema reads a replica's schema state until it satisfies the
// condition, which is how a peer's asynchronous reload is observed.
func issue1676WaitSchema(t *testing.T, c *client, name string, ready func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(issue1676Settle)
	for {
		status, info := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/graphql/"+name+"/schema", http.NoBody)
		if status == http.StatusOK && ready(info) {
			return info
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not reach the expected schema state for %s within %s; last answer HTTP %d: %v",
				c.base, name, issue1676Settle, status, info)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// issue1676Upload supplies the schema through the refresh route and returns
// the state the route reports.
func issue1676Upload(t *testing.T, c *client, name, payload string) map[string]any {
	t.Helper()
	status, info := c.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema", strings.NewReader(payload))
	if status != http.StatusOK {
		t.Fatalf("uploading the schema: HTTP %d: %v", status, info)
	}
	return info
}

// issue1676Fields reads the fields a criterion compares across replicas.
func issue1676Fields(info map[string]any) (source, hash, schemaErr string, count float64) {
	source, _ = info["source"].(string)
	hash, _ = info["schema_hash"].(string)
	schemaErr, _ = info["error"].(string)
	count, _ = info["operation_count"].(float64)
	return source, hash, schemaErr, count
}

// issue1676AssertNoSchema is the state a connection whose endpoint refuses
// introspection starts in, on any replica: nothing held, and the refusal
// named.
func issue1676AssertNoSchema(t *testing.T, replica string, info map[string]any) {
	t.Helper()
	source, hash, schemaErr, count := issue1676Fields(info)
	if count != 0 || hash != "" || source != "" {
		t.Errorf("%s: a connection whose endpoint refused the read holds a schema: %v", replica, info)
	}
	if !strings.Contains(schemaErr, "302") {
		t.Errorf("%s: the refusal is not named on the connection: %v", replica, info)
	}
}

// issue1676AssertUpload asserts a replica holds the uploaded schema, with the
// re-read's failure beside it when one has happened since.
func issue1676AssertUpload(t *testing.T, replica string, info map[string]any, wantHash string, wantCount float64, wantFailedReread bool) {
	t.Helper()
	source, hash, schemaErr, count := issue1676Fields(info)
	if source != "upload" {
		t.Errorf("%s: source = %q; want upload: %v", replica, source, info)
	}
	if hash != wantHash {
		t.Errorf("%s: schema_hash = %q; want %q (the uploaded version)", replica, hash, wantHash)
	}
	if count != wantCount {
		t.Errorf("%s: operation_count = %v; want %v", replica, count, wantCount)
	}
	switch {
	case wantFailedReread && !strings.Contains(schemaErr, "302"):
		t.Errorf("%s: the failed re-read is not reported beside the schema: %v", replica, info)
	case !wantFailedReread && schemaErr != "":
		t.Errorf("%s: an error is reported on a connection whose schema was just supplied: %q", replica, schemaErr)
	}
}

// TestIssue1676_AConfigurationSaveKeepsTheUploadedSchemaOnEveryReplica is the
// ticket's "done when" verbatim: with two replicas, an upload followed by a
// configuration save leaves GET .../schema answering the uploaded version
// from every replica, with the failed re-read reported in `error`. Both
// upload forms are carried through it.
func TestIssue1676_AConfigurationSaveKeepsTheUploadedSchemaOnEveryReplica(t *testing.T) {
	forms := map[string]string{"sdl": issue1676SDL, "introspection": issue1676Introspection}
	for form, payload := range forms {
		t.Run(form, func(t *testing.T) {
			a, b := connectReplicaPair(t)
			name, cfg := issue1676Connect(t, a, form, issue1676Upstream(t))

			// Registration reads the endpoint on both replicas, and the
			// endpoint refuses it on both.
			refused := func(info map[string]any) bool { e, _ := info["error"].(string); return e != "" }
			issue1676AssertNoSchema(t, "replica A", issue1676WaitSchema(t, a, name, refused))
			issue1676AssertNoSchema(t, "replica B", issue1676WaitSchema(t, b, name, refused))

			// The upload lands on the replica that served it, and reaches
			// the other through the store.
			uploaded := issue1676Upload(t, a, name, payload)
			_, hash, _, count := issue1676Fields(uploaded)
			if hash == "" || count != 1 {
				t.Fatalf("the upload did not install a schema with its one operation: %v", uploaded)
			}
			issue1676AssertUpload(t, "replica A", issue1676Schema(t, a, name), hash, count, false)
			fromStore := func(info map[string]any) bool { s, _ := info["source"].(string); return s == "upload" }
			issue1676AssertUpload(t, "replica B", issue1676WaitSchema(t, b, name, fromStore), hash, count, false)

			// One configuration key changes. The save re-reads the endpoint
			// on every replica, the read fails on every replica, and every
			// replica keeps the upload with the failure reported beside it.
			cfg["read_only"] = true
			issue1676Save(t, a, name, cfg, http.StatusOK)
			issue1676AssertUpload(t, "replica A", issue1676Schema(t, a, name), hash, count, true)
			issue1676AssertUpload(t, "replica B", issue1676WaitSchema(t, b, name, refused), hash, count, true)

			// Two consecutive reads of either replica agree with each other
			// and with the other replica, which the ticket's third
			// observation was the absence of.
			for _, replica := range []*client{a, b, a, b} {
				issue1676AssertUpload(t, replica.base, issue1676Schema(t, replica, name), hash, count, true)
			}
		})
	}
}

// TestIssue1676_ANamespacedDocumentReportsTheDottedOperationFromEveryReplica
// is the ticket's last "done when": graphql_query on a namespaced document
// reports the dotted operation from every replica, in both forms `variables`
// admits, and the document runs against the real server through the
// forwarder.
func TestIssue1676_ANamespacedDocumentReportsTheDottedOperationFromEveryReplica(t *testing.T) {
	a, b := connectReplicaPair(t)
	name, _ := issue1676Connect(t, a, "dotted", issue1676Upstream(t))
	issue1676Upload(t, a, name, issue1676SDL)
	fromStore := func(info map[string]any) bool { s, _ := info["source"].(string); return s == "upload" }
	issue1676WaitSchema(t, b, name, fromStore)

	variables := map[string]any{"all": true}
	asString, err := json.Marshal(variables)
	if err != nil {
		t.Fatalf("marshal the variables: %v", err)
	}
	forms := map[string]any{"object": variables, "string": string(asString)}
	for _, replica := range []*client{a, b} {
		for form, vars := range forms {
			out := replica.call(issue1277QueryTool, map[string]any{
				"connection": name, "query": issue1676Document, "variables": vars, "purpose": issue1676Purpose,
			})
			ops, _ := out["operations"].([]any)
			if len(ops) != 1 || ops[0] != issue1676DottedOperation {
				t.Errorf("%s, %s form: operations = %v; want [%s]", replica.base, form, ops, issue1676DottedOperation)
			}
			if failed, _ := out["upstream_error"].(bool); failed {
				t.Errorf("%s, %s form: the document failed upstream: %v", replica.base, form, out)
			}
			raw, _ := json.Marshal(out["data"])
			if !strings.Contains(string(raw), `"enabled"`) {
				t.Errorf("%s, %s form: the server's answer did not come back: %s", replica.base, form, raw)
			}
		}
	}
}

// TestIssue1676_AConnectionWithNoSchemaRefusesADocumentRatherThanAuthorizingItsRootField
// is the ticket's fourth observation: on the replica without an operation
// index, a document was authorized and cataloged under its root field.
// Without an index there is nothing to reduce a document to, and the call is
// refused with the cause, on every replica.
func TestIssue1676_AConnectionWithNoSchemaRefusesADocumentRatherThanAuthorizingItsRootField(t *testing.T) {
	a, b := connectReplicaPair(t)
	name, _ := issue1676Connect(t, a, "noschema", issue1676Upstream(t))
	refused := func(info map[string]any) bool { e, _ := info["error"].(string); return e != "" }
	issue1676WaitSchema(t, a, name, refused)
	issue1676WaitSchema(t, b, name, refused)

	for _, replica := range []*client{a, b} {
		text := issue1277Refuse(t, replica, issue1277QueryTool, map[string]any{
			"connection": name, "query": issue1676Document, "variables": map[string]any{"all": true},
		})
		if !strings.Contains(text, "has no schema") || !strings.Contains(text, "302") {
			t.Errorf("%s: the refusal does not name the missing schema and its cause: %s", replica.base, text)
		}
		if strings.Contains(text, `"operations"`) {
			t.Errorf("%s: a document was reduced against no index: %s", replica.base, text)
		}
	}
}
