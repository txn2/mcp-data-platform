package graphqlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

const introspectionResult = `{"data":{"__schema":{
  "queryType": {"name": "Query"},
  "types": [
    {"kind": "OBJECT", "name": "Query", "fields": [
      {"name": "health", "args": [], "type": {"kind": "SCALAR", "name": "String"}, "isDeprecated": false}
    ]}
  ],
  "directives": []
}}}`

// endpoint starts a GraphQL endpoint whose introspection answer the test
// controls.
func endpoint(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// mount builds a toolkit against an endpoint and registers these routes.
func mount(t *testing.T, endpointURL string, mutable bool) (*http.ServeMux, *graphqlkit.Toolkit) {
	mux, tk, _ := mountAnnouncing(t, endpointURL, mutable)
	return mux, tk
}

// mountAnnouncing is mount with the stored-schema announcements recorded.
func mountAnnouncing(t *testing.T, endpointURL string, mutable bool) (*http.ServeMux, *graphqlkit.Toolkit, *[]string) {
	t.Helper()
	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": endpointURL})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.ConnectionName = "gql"
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: "gql", Instances: map[string]graphqlkit.Config{"gql": cfg},
	})
	mux := http.NewServeMux()
	announced := &[]string{}
	Register(mux, Config{
		Toolkits:     func() []registry.Toolkit { return []registry.Toolkit{tk} },
		Mutable:      mutable,
		SchemaStored: func(kind, name string) { *announced = append(*announced, kind+"/"+name) },
	})
	return mux, tk, announced
}

// call issues one request against the mounted routes.
func call(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeInfo(t *testing.T, rec *httptest.ResponseRecorder) graphqlkit.SchemaInfo {
	t.Helper()
	var info graphqlkit.SchemaInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decoding: %v\n%s", err, rec.Body.String())
	}
	return info
}

func TestGetSchemaReportsWhatThePlatformHolds(t *testing.T) {
	server := endpoint(t, introspectionResult)
	mux, tk := mount(t, server.URL, true)
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := call(t, mux, http.MethodGet, "/api/v1/admin/connection-instances/graphql/gql/schema", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	info := decodeInfo(t, rec)
	if info.Connection != "gql" || info.OperationCount != 1 || info.Hash == "" {
		t.Errorf("info = %+v", info)
	}
}

func TestGetSchemaOnAConnectionThisDeploymentDoesNotHold(t *testing.T) {
	server := endpoint(t, introspectionResult)
	mux, _ := mount(t, server.URL, true)
	rec := call(t, mux, http.MethodGet, "/api/v1/admin/connection-instances/graphql/absent/schema", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestRefreshWithNoBodyReadsTheEndpoint(t *testing.T) {
	server := endpoint(t, introspectionResult)
	mux, _ := mount(t, server.URL, true)

	rec := call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	info := decodeInfo(t, rec)
	if info.Source != graphqlkit.SchemaSourceIntrospection || info.OperationCount != 1 {
		t.Errorf("info = %+v", info)
	}
}

func TestRefreshWithABodyTakesTheBodyAsTheSchema(t *testing.T) {
	// An endpoint that refuses introspection is exactly why this path
	// exists.
	server := endpoint(t, `{"errors":[{"message":"introspection disabled"}]}`)
	mux, _ := mount(t, server.URL, true)
	sdl, err := os.ReadFile("../../../internal/gqlschema/testdata/flat.graphql")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	rec := call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", string(sdl))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	info := decodeInfo(t, rec)
	if info.Source != graphqlkit.SchemaSourceUpload || info.OperationCount == 0 {
		t.Errorf("info = %+v", info)
	}
}

func TestRefreshAcceptsAnIntrospectionResultAsWellAsSDL(t *testing.T) {
	server := endpoint(t, `{"errors":[{"message":"introspection disabled"}]}`)
	mux, _ := mount(t, server.URL, true)
	rec := call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", introspectionResult)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if info := decodeInfo(t, rec); info.OperationCount != 1 {
		t.Errorf("info = %+v", info)
	}
}

func TestRefreshSeparatesTheOperatorsInputFromTheUpstreamsFailure(t *testing.T) {
	server := endpoint(t, `{"errors":[{"message":"introspection disabled"}]}`)
	mux, _ := mount(t, server.URL, true)

	// The endpoint would not answer: that is the upstream's failure.
	rec := call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", "")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d; an endpoint that refused is a 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "introspection disabled") {
		t.Errorf("body = %s; the upstream's own words are the diagnosis", rec.Body)
	}

	// A schema they supplied that does not parse is their input.
	rec = call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", "type Query {")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; an unparseable upload is a 400", rec.Code)
	}
}

// TestAStoredSchemaIsAnnouncedToPeers: an upload and a re-read both replace
// the stored schema, and each is announced so the other replicas install it
// (#1676). A refresh the endpoint refused and an upload that did not parse
// stored nothing, and announce nothing.
func TestAStoredSchemaIsAnnouncedToPeers(t *testing.T) {
	server := endpoint(t, introspectionResult)
	mux, _, announced := mountAnnouncing(t, server.URL, true)
	sdl, err := os.ReadFile("../../../internal/gqlschema/testdata/flat.graphql")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", string(sdl))
	call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", "")
	call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", "type Query {")

	if got := *announced; len(got) != 2 || got[0] != "graphql/gql" || got[1] != "graphql/gql" {
		t.Errorf("announced = %v; want one announcement per stored schema and none for the refused upload", got)
	}

	refusing := endpoint(t, `{"errors":[{"message":"introspection disabled"}]}`)
	mux, _, announced = mountAnnouncing(t, refusing.URL, true)
	call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", "")
	if got := *announced; len(got) != 0 {
		t.Errorf("announced = %v; a re-read the endpoint refused stored nothing", got)
	}
}

func TestRefreshOnAConnectionThisDeploymentDoesNotHold(t *testing.T) {
	server := endpoint(t, introspectionResult)
	mux, _ := mount(t, server.URL, true)
	rec := call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/absent/refresh-schema", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestReadOnlyModeMountsTheReadRouteOnly(t *testing.T) {
	server := endpoint(t, introspectionResult)
	mux, _ := mount(t, server.URL, false)
	if rec := call(t, mux, http.MethodGet, "/api/v1/admin/connection-instances/graphql/gql/schema", ""); rec.Code == http.StatusNotFound {
		t.Error("the read route was not mounted")
	}
	// A refresh writes the schema to the store, and a file-configured
	// deployment has nowhere to put it.
	rec := call(t, mux, http.MethodPost, "/api/v1/admin/connection-instances/graphql/gql/refresh-schema", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; the write route must not be mounted", rec.Code)
	}
}

func TestNothingIsMountedWithoutAToolkitSource(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Config{Mutable: true})
	rec := call(t, mux, http.MethodGet, "/api/v1/admin/connection-instances/graphql/gql/schema", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestAToolkitOfAnotherKindIsSkipped(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Config{
		Toolkits: func() []registry.Toolkit { return []registry.Toolkit{nil} },
		Mutable:  true,
	})
	rec := call(t, mux, http.MethodGet, "/api/v1/admin/connection-instances/graphql/gql/schema", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}
