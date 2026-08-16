package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// --- issue #1297: more than one placeholder inside a single segment ---

// TestSubstitutePathParams_MultiPlaceholderSegment proves a segment
// carrying two placeholders resolves to the two parameter names the
// spec declares. Before the fix the whole segment was taken as one
// placeholder named "latitude},{longitude", which no caller could
// supply, so every such call died with a missing-parameter error
// naming a parameter nobody wrote.
func TestSubstitutePathParams_MultiPlaceholderSegment(t *testing.T) {
	cases := []struct {
		name     string
		template string
		params   map[string]string
		want     string
		wantErr  string
	}{
		{
			name:     "two placeholders joined by a comma",
			template: "/points/{latitude},{longitude}",
			params:   map[string]string{"latitude": "37.41", "longitude": "-94.70"},
			want:     "/points/37.41,-94.70",
		},
		{
			name:     "mixed literal and placeholder segments",
			template: "/gridpoints/{office}/{gridX},{gridY}/forecast",
			params:   map[string]string{"office": "EAX", "gridX": "50", "gridY": "60"},
			want:     "/gridpoints/EAX/50,60/forecast",
		},
		{
			name:     "placeholder with a literal suffix",
			template: "/files/{name}.json",
			params:   map[string]string{"name": "report"},
			want:     "/files/report.json",
		},
		{
			name:     "placeholder with a literal prefix",
			template: "/v1/id-{id}",
			params:   map[string]string{"id": "42"},
			want:     "/v1/id-42",
		},
		{
			name:     "each value is escaped in place, the separator is not",
			template: "/points/{latitude},{longitude}",
			params:   map[string]string{"latitude": "a/b", "longitude": "c d"},
			want:     "/points/a%2Fb,c%20d",
		},
		{
			name:     "one of two supplied still reports the other missing",
			template: "/points/{latitude},{longitude}",
			params:   map[string]string{"latitude": "37.41"},
			wantErr:  "missing required path parameter(s): longitude",
		},
		{
			name:     "empty value in a shared segment is refused",
			template: "/points/{latitude},{longitude}",
			params:   map[string]string{"latitude": "37.41", "longitude": ""},
			wantErr:  `path parameter "longitude" is empty`,
		},
		{
			name:     "stray parameter is still a typo guard",
			template: "/points/{latitude},{longitude}",
			params:   map[string]string{"latitude": "1", "longitude": "2", "lat": "3"},
			wantErr:  "not present in the operation path template: lat",
		},
		{
			name:     "degenerate empty braces are literal",
			template: "/v1/{}",
			params:   nil,
			want:     "/v1/{}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := substitutePathParams(c.template, c.params)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v; want substring %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q; want %q", got, c.want)
			}
		})
	}
}

// TestSubstitutePathParams_MultiPlaceholderReportsAllMissing proves both
// names in a shared segment are reported at once, matching the
// one-placeholder-per-segment behavior.
func TestSubstitutePathParams_MultiPlaceholderReportsAllMissing(t *testing.T) {
	_, err := substitutePathParams("/points/{latitude},{longitude}", nil)
	if err == nil {
		t.Fatal("expected error for missing params")
	}
	for _, want := range []string{"latitude", "longitude"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name missing param %q", err.Error(), want)
		}
	}
	// The mangled name the old whole-segment parse produced must not
	// survive anywhere in the message.
	if strings.Contains(err.Error(), "},{") {
		t.Errorf("error %q still names a merged placeholder", err.Error())
	}
}

// TestPathMatchesTemplate_MultiPlaceholderSegment proves the router side
// agrees with the substitution side: a concrete path produced by
// substitutePathParams matches the template it came from. Without this
// the two halves of issue #1297 would disagree and Content-Type
// negotiation would silently miss the operation.
func TestPathMatchesTemplate_MultiPlaceholderSegment(t *testing.T) {
	cases := []struct {
		name     string
		concrete string
		template string
		want     bool
	}{
		{"comma-joined pair matches", "/points/37.41,-94.70", "/points/{latitude},{longitude}", true},
		{"missing separator does not match", "/points/37.41", "/points/{latitude},{longitude}", false},
		{"empty first hole does not match", "/points/,-94.70", "/points/{latitude},{longitude}", false},
		{"empty second hole does not match", "/points/37.41,", "/points/{latitude},{longitude}", false},
		{"extra segment does not match", "/points/37.41,-94.70/x", "/points/{latitude},{longitude}", false},
		{"literal suffix matches", "/files/report.json", "/files/{name}.json", true},
		{"literal suffix required", "/files/report.yaml", "/files/{name}.json", false},
		{"literal suffix needs a non-empty hole", "/files/.json", "/files/{name}.json", false},
		{"literal prefix matches", "/v1/id-42", "/v1/id-{id}", true},
		{"literal prefix required", "/v1/42", "/v1/id-{id}", false},
		{"nested template segment matches", "/gridpoints/EAX/50,60/forecast", "/gridpoints/{office}/{gridX},{gridY}/forecast", true},
		{"whole-segment placeholder still matches", "/things/abc", "/things/{id}", true},
		{"literal segment still exact", "/things", "/things", true},
		{"value containing the separator still matches greedily", "/points/a,b,c", "/points/{latitude},{longitude}", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pathMatchesTemplate(c.concrete, c.template); got != c.want {
				t.Errorf("pathMatchesTemplate(%q, %q) = %v; want %v",
					c.concrete, c.template, got, c.want)
			}
		})
	}
}

// TestCountTemplatePlaceholders_CountsOccurrences proves specificity
// ranking counts holes rather than templated segments, so a template
// spending two placeholders on one segment ranks below one that spends
// a whole segment per placeholder when both match.
func TestCountTemplatePlaceholders_CountsOccurrences(t *testing.T) {
	cases := []struct {
		template string
		want     int
	}{
		{"/v1/things", 0},
		{"/v1/things/{id}", 1},
		{"/v1/orgs/{org}/things/{id}", 2},
		{"/points/{latitude},{longitude}", 2},
		{"/gridpoints/{office}/{gridX},{gridY}/forecast", 3},
		{"/files/{name}.json", 1},
		{"/v1/{}", 0},
	}
	for _, c := range cases {
		t.Run(c.template, func(t *testing.T) {
			if got := countTemplatePlaceholders(c.template); got != c.want {
				t.Errorf("countTemplatePlaceholders(%q) = %d; want %d", c.template, got, c.want)
			}
		})
	}
}

// TestIsPlaceholderSegment_WholeSegmentOnly proves the whole-segment
// test rejects the shapes that must route through the literal-aware
// matcher instead, so a segment with literal text is never treated as a
// match-anything wildcard.
func TestIsPlaceholderSegment_WholeSegmentOnly(t *testing.T) {
	cases := map[string]bool{
		"{id}":                   true,
		"{datasetId}":            true,
		"things":                 false,
		"":                       false,
		"{}":                     false,
		"{latitude},{longitude}": false,
		"{name}.json":            false,
		"id-{id}":                false,
		"{a}{b}":                 false,
	}
	for seg, want := range cases {
		t.Run(seg, func(t *testing.T) {
			if got := isPlaceholderSegment(seg); got != want {
				t.Errorf("isPlaceholderSegment(%q) = %v; want %v", seg, got, want)
			}
		})
	}
}

// nwsPointsSpec models the shape issue #1297 was filed against: a path
// whose single segment carries two declared path parameters. No servers
// block, so the effective base path is empty.
const nwsPointsSpec = `openapi: 3.0.0
info:
  title: points
  version: "1"
paths:
  /points/{latitude},{longitude}:
    get:
      operationId: resolvePoint
      summary: Resolve a point to a forecast grid
      parameters:
        - name: latitude
          in: path
          required: true
          schema:
            type: string
        - name: longitude
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok`

// TestHandleInvoke_MultiPlaceholderSegment is the end-to-end proof for
// issue #1297: the real handler, the real catalog, and the real HTTP
// client turn an operation_id plus two path_params into the concrete
// request the upstream sees. Asserting on the upstream's escaped path
// also pins that the separator stays a literal comma rather than being
// percent-escaped along with the values.
func TestHandleInvoke_MultiPlaceholderSegment(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	tk := New("api")
	setupCatalogWithSpec(t, tk, "nws", "points", nwsPointsSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url": srv.URL, "catalog_id": "nws",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	res, payload, err := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection:  "c",
		OperationID: "resolvePoint",
		PathParams:  map[string]string{"latitude": "37.41", "longitude": "-94.70"},
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("unexpected error result: %s", textContent(res))
	}
	if want := "/points/37.41,-94.70"; gotPath != want {
		t.Errorf("upstream saw %q; want %q", gotPath, want)
	}
	out, ok := payload.(InvokeOutput)
	if !ok {
		t.Fatalf("payload is %T; want InvokeOutput", payload)
	}
	if out.Status != http.StatusOK {
		t.Errorf("status = %d; want 200", out.Status)
	}
	if want := "/points/37.41,-94.70"; out.ResolvedPath != want {
		t.Errorf("resolved_path = %q; want %q", out.ResolvedPath, want)
	}
}

// --- issue #1298: one spec shared across connections with different bases ---

// sharedFeatureServerSpec declares two servers whose paths differ,
// modeling one curated spec mounted by two connections that speak the
// identical API against different deployments.
const sharedFeatureServerSpec = `openapi: 3.0.0
info:
  title: features
  version: "1"
servers:
  - url: https://ks.example.gov/adaptor/rest/FeatureServer
  - url: https://mo.example.gov/server/rest/Hosted/FeatureServer
paths:
  /{layerId}/query:
    get:
      operationId: queryLayer
      summary: Query a feature layer
      parameters:
        - name: layerId
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok`

// TestSharedSpec_EachConnectionKeepsItsOwnBasePath is the end-to-end
// proof for issue #1298. Two connections mount one catalog spec that
// declares both of their base paths. Each must route to its own prefix;
// before the fix only servers[0] was consulted, so the second connection
// had the first's prefix appended to its own base_url and every
// operation_id call landed on a concatenation of two deployments'
// paths — which the upstream rejected with a generic 400.
func TestSharedSpec_EachConnectionKeepsItsOwnBasePath(t *testing.T) {
	ksPath := "/adaptor/rest/FeatureServer"
	moPath := "/server/rest/Hosted/FeatureServer"

	var ksSeen, moSeen string
	ksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ksSeen = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"features":[]}`))
	}))
	t.Cleanup(ksSrv.Close)
	moSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		moSeen = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"features":[]}`))
	}))
	t.Cleanup(moSrv.Close)

	tk := New("api")
	setupCatalogWithSpec(t, tk, "dot", "aadt", sharedFeatureServerSpec)
	for name, baseURL := range map[string]string{
		"dot-aadt-ks": ksSrv.URL + ksPath,
		"dot-aadt-mo": moSrv.URL + moPath,
	} {
		if err := tk.AddConnection(name, map[string]any{
			"base_url": baseURL, "catalog_id": "dot",
		}); err != nil {
			t.Fatalf("AddConnection %s: %v", name, err)
		}
	}

	call := func(connection string) InvokeOutput {
		t.Helper()
		res, payload, err := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
			Connection:  connection,
			OperationID: "queryLayer",
			PathParams:  map[string]string{"layerId": "0"},
		})
		if err != nil {
			t.Fatalf("handleInvoke %s: %v", connection, err)
		}
		if res == nil || res.IsError {
			t.Fatalf("%s: unexpected error result: %s", connection, textContent(res))
		}
		out, ok := payload.(InvokeOutput)
		if !ok {
			t.Fatalf("%s: payload is %T; want InvokeOutput", connection, payload)
		}
		return out
	}

	ksOut := call("dot-aadt-ks")
	if want := ksPath + "/0/query"; ksSeen != want {
		t.Errorf("kansas upstream saw %q; want %q", ksSeen, want)
	}
	if want := "/0/query"; ksOut.ResolvedPath != want {
		t.Errorf("kansas resolved_path = %q; want %q", ksOut.ResolvedPath, want)
	}

	moOut := call("dot-aadt-mo")
	if want := moPath + "/0/query"; moSeen != want {
		t.Errorf("missouri upstream saw %q; want %q", moSeen, want)
	}
	if want := "/0/query"; moOut.ResolvedPath != want {
		t.Errorf("missouri resolved_path = %q; want %q", moOut.ResolvedPath, want)
	}
	if strings.Contains(moSeen, ksPath) {
		t.Errorf("missouri request %q carries the kansas prefix %q", moSeen, ksPath)
	}
}

// TestSharedSpec_UnmatchedConnectionKeepsFirstServerPrefix pins the
// behavior for a connection whose base_url matches no declared server:
// the spec's first server path is still applied, which is what a
// connection pointed at a proxy mount below the documented base needs.
// The multi-server lookup must not turn that case into a dropped prefix.
func TestSharedSpec_UnmatchedConnectionKeepsFirstServerPrefix(t *testing.T) {
	tk := New("api")
	setupCatalogWithSpec(t, tk, "dot", "aadt", sharedFeatureServerSpec)
	if err := tk.AddConnection("proxied", map[string]any{
		"base_url": "https://proxy.example.com/mount", "catalog_id": "dot",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	c := tk.connections["proxied"]
	if got, want := c.specs["aadt"].effectiveBasePath, "/adaptor/rest/FeatureServer"; got != want {
		t.Errorf("effectiveBasePath = %q; want %q", got, want)
	}
	_, path, err := resolveOperationTarget(c, operationAddressing{
		OperationID: "queryLayer", PathParams: map[string]string{"layerId": "0"},
	})
	if err != nil {
		t.Fatalf("resolveOperationTarget: %v", err)
	}
	if want := "/adaptor/rest/FeatureServer/0/query"; path != want {
		t.Errorf("resolved path = %q; want %q", path, want)
	}
}

// TestBasePathOverride_IsTheOnlyCandidate proves an operator's per-spec
// base_path override still behaves as a single authoritative prefix: the
// spec's declared servers are not consulted alongside it, so an override
// cannot be silently canceled by a servers[] entry that happens to match
// the connection's base_url.
func TestBasePathOverride_IsTheOnlyCandidate(t *testing.T) {
	tk := New("api")
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	if err := store.CreateCatalog(context.Background(), catalog.Catalog{
		ID: "dot", Name: "dot", DisplayName: "dot",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	entry := newSpecEntry("aadt", sharedFeatureServerSpec)
	entry.BasePath = "/override"
	if err := store.UpsertSpec(context.Background(), "dot", entry); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	// base_url ends with the spec's second declared server path. With the
	// override in force that must not cancel the override.
	if err := tk.AddConnection("mo", map[string]any{
		"base_url":   "https://mo.example.gov/server/rest/Hosted/FeatureServer",
		"catalog_id": "dot",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if got, want := tk.connections["mo"].specs["aadt"].effectiveBasePath, "/override"; got != want {
		t.Errorf("effectiveBasePath = %q; want %q", got, want)
	}
}

// TestHandleInvoke_MethodPathFormOmitsResolvedPath proves resolved_path
// is reported only for operation_id addressing. In the method+path form
// the caller wrote the path, so echoing it back is noise.
func TestHandleInvoke_MethodPathFormOmitsResolvedPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	tk := setupOpTargetToolkit(t, srv.URL)
	_, payload, err := tk.handleInvoke(context.Background(), &mcp.CallToolRequest{}, InvokeInput{
		Connection: "c", Method: "GET", Path: "/things",
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	out, ok := payload.(InvokeOutput)
	if !ok {
		t.Fatalf("payload is %T; want InvokeOutput", payload)
	}
	if out.ResolvedPath != "" {
		t.Errorf("resolved_path = %q; want empty for method+path addressing", out.ResolvedPath)
	}
}

// TestResolveOperationID_MultiPlaceholderSegment proves the metrics
// operation-id resolver — a separate matcher (kin-openapi's gorillamux
// router, with the WebDAV index as fallback) from the one invoke uses —
// also resolves a two-placeholder segment, so such a call is attributed
// to its operation instead of the "unknown" label bucket.
func TestResolveOperationID_MultiPlaceholderSegment(t *testing.T) {
	tk := New("api")
	setupCatalogWithSpec(t, tk, "nws", "points", nwsPointsSpec)
	if err := tk.AddConnection("c", map[string]any{
		"base_url": "https://api.weather.example", "catalog_id": "nws",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	got := tk.ResolveOperationID(context.Background(), "c", "GET", "/points/37.41,-94.70")
	if want := "resolvePoint"; got != want {
		t.Errorf("ResolveOperationID = %q; want %q", got, want)
	}
}

// TestTemplatedSegmentMatches_DegenerateTemplates covers the guards that
// keep a malformed template from being read as a wildcard: braces that
// form no well-formed placeholder are compared literally, and a concrete
// segment that runs out before an interior literal does not match.
func TestTemplatedSegmentMatches_DegenerateTemplates(t *testing.T) {
	cases := []struct {
		name     string
		concrete string
		template string
		want     bool
	}{
		{"unclosed brace matches itself", "{", "{", true},
		{"unclosed brace is not a wildcard", "anything", "{", false},
		{"empty braces match themselves", "{}", "{}", true},
		{"empty braces are not a wildcard", "anything", "{}", false},
		{"concrete exhausted before an interior literal", "x,", "{a},{b},{c}", false},
		{"concrete too short for the interior literal", "x", "{a},{b},{c}", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := segmentMatches(c.concrete, c.template); got != c.want {
				t.Errorf("segmentMatches(%q, %q) = %v; want %v",
					c.concrete, c.template, got, c.want)
			}
		})
	}
}

// TestSpecBasePaths_Positional proves the result is positional rather
// than filtered: a server that contributes no prefix still occupies its
// index, so index 0 stays servers[0] and a bare-host first server keeps
// contributing nothing even when a later server declares a path.
func TestSpecBasePaths_Positional(t *testing.T) {
	if got := specBasePaths(nil); got != nil {
		t.Errorf("specBasePaths(nil) = %v; want nil", got)
	}
	doc := &openapi3.T{Servers: openapi3.Servers{
		nil,
		{URL: "https://api.example.com"},
		{URL: "://not-a-url"},
		{URL: "https://api.example.com/"},
		{URL: "https://api.example.com/v1"},
	}}
	got := specBasePaths(doc)
	want := []string{"", "", "", "", "/v1"}
	if len(got) != len(want) {
		t.Fatalf("specBasePaths = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("specBasePaths[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}
