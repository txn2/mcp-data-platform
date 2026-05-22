package apigateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// jsonOpSpec declares a single POST operation whose requestBody
// content is application/json. This is the minimum shape needed to
// drive catalog-aware Content-Type selection.
const jsonOpSpec = `
openapi: 3.0.3
info:
  title: Test API
  version: "1.0"
paths:
  /v1/query:
    post:
      operationId: runQuery
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                sql:
                  type: string
      responses:
        "200":
          description: ok
`

// jsonOpSpecTemplated declares a POST whose path carries a
// placeholder, exercising the path-template matcher for the
// representative fixture in issue #453 (an upstream dataset query
// keyed by a runtime UUID).
const jsonOpSpecTemplated = `
openapi: 3.0.3
info:
  title: Test API
  version: "1.0"
paths:
  /v1/datasets/query/execute/{datasetId}:
    post:
      operationId: queryDataset
      parameters:
        - name: datasetId
          in: path
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
      responses:
        "200":
          description: ok
`

// xmlOpSpec exercises the non-JSON declared media-type branch: a
// string body should leave the gateway with application/xml on the
// wire, no transformation.
const xmlOpSpec = `
openapi: 3.0.3
info:
  title: Test API
  version: "1.0"
paths:
  /v1/feed:
    post:
      operationId: postFeed
      requestBody:
        required: true
        content:
          application/xml:
            schema:
              type: string
      responses:
        "200":
          description: ok
`

// literalVsTemplatedSpec exercises specificity ranking: a concrete
// path that matches both a literal entry and a templated entry
// should resolve to the literal one. Without ranking, Go's
// randomized map iteration would pick non-deterministically.
const literalVsTemplatedSpec = `
openapi: 3.0.3
info:
  title: Test API
  version: "1.0"
paths:
  /v1/users/me:
    post:
      operationId: updateMe
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
      responses:
        "200":
          description: ok
  /v1/users/{id}:
    post:
      operationId: updateUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/xml:
            schema:
              type: string
      responses:
        "200":
          description: ok
`

func mustParseSpec(t *testing.T, raw string) *specState {
	t.Helper()
	doc, err := parseOpenAPISpec(raw)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	return &specState{doc: doc}
}

func TestPathMatchesTemplate(t *testing.T) {
	cases := []struct {
		concrete string
		template string
		want     bool
	}{
		{"/v1/users/42", "/v1/users/{id}", true},
		{"/v1/users/me", "/v1/users/{id}", true},
		{"/v1/users", "/v1/users/{id}", false},
		{"/v1/users/42/posts", "/v1/users/{id}", false},
		{"/v1/users/", "/v1/users", true},
		{"/v1/users", "/v1/users/", true},
		{
			"/v1/datasets/query/execute/b72dfd07-dc16-4335-b539-3badcf728959",
			"/v1/datasets/query/execute/{datasetId}", true,
		},
		{"/v1/users//posts", "/v1/users/{id}/posts", false},
		{"/v1/users/me", "/v1/users/me", true},
		{"/v2/users/me", "/v1/users/me", false},
	}
	for _, tc := range cases {
		got := pathMatchesTemplate(tc.concrete, tc.template)
		if got != tc.want {
			t.Errorf("pathMatchesTemplate(%q, %q) = %v; want %v",
				tc.concrete, tc.template, got, tc.want)
		}
	}
}

func TestResolveDeclaredContentTypes_LiteralPath(t *testing.T) {
	specs := map[string]*specState{"main": mustParseSpec(t, jsonOpSpec)}
	got := resolveDeclaredContentTypes(specs, "POST", "/v1/query")
	if len(got) != 1 || got[0] != "application/json" {
		t.Errorf("declared = %v; want [application/json]", got)
	}
}

func TestResolveDeclaredContentTypes_TemplatedPath(t *testing.T) {
	specs := map[string]*specState{"main": mustParseSpec(t, jsonOpSpecTemplated)}
	got := resolveDeclaredContentTypes(specs, "POST",
		"/v1/datasets/query/execute/b72dfd07-dc16-4335-b539-3badcf728959")
	if len(got) != 1 || got[0] != "application/json" {
		t.Errorf("declared = %v; want [application/json]", got)
	}
}

func TestResolveDeclaredContentTypes_NoMatch(t *testing.T) {
	specs := map[string]*specState{"main": mustParseSpec(t, jsonOpSpec)}
	if got := resolveDeclaredContentTypes(specs, "POST", "/v2/other"); got != nil {
		t.Errorf("declared = %v; want nil for unmatched path", got)
	}
	if got := resolveDeclaredContentTypes(specs, "GET", "/v1/query"); got != nil {
		t.Errorf("declared = %v; want nil for unmatched method", got)
	}
}

func TestResolveDeclaredContentTypes_NilSpecs(t *testing.T) {
	if got := resolveDeclaredContentTypes(nil, "POST", "/v1/query"); got != nil {
		t.Errorf("declared = %v; want nil for nil specs (preserves today's behavior)", got)
	}
}

// TestOperationForMethod_UnknownVerb covers the defensive fallthrough
// branch for HTTP verbs not in pathItemMethods. The branch is
// unreachable from the resolver in practice (validateMethod gates the
// invoke entry point to the six verbs the toolkit supports), but the
// nil-return is documented behavior and must stay tested per the
// per-function coverage rule in CLAUDE.md.
func TestOperationForMethod_UnknownVerb(t *testing.T) {
	st := mustParseSpec(t, jsonOpSpec)
	var item *openapi3.PathItem
	for _, raw := range st.doc.Paths.Map() {
		item = raw
		break
	}
	if item == nil {
		t.Fatal("test spec has no path items")
	}
	if got := operationForMethod(item, "OPTIONS"); got != nil {
		t.Errorf("operationForMethod(OPTIONS) = %#v; want nil", got)
	}
}

// TestResolveDeclaredContentTypes_PrefersLiteralOverTemplated runs
// multiple times against a randomized-iteration map to confirm the
// literal /v1/users/me wins over /v1/users/{id} deterministically.
// Without specificity ranking this test would flake.
func TestResolveDeclaredContentTypes_PrefersLiteralOverTemplated(t *testing.T) {
	specs := map[string]*specState{"main": mustParseSpec(t, literalVsTemplatedSpec)}
	for i := range 50 {
		got := resolveDeclaredContentTypes(specs, "POST", "/v1/users/me")
		if len(got) != 1 || got[0] != "application/json" {
			t.Fatalf("iteration %d: declared = %v; want [application/json] (literal path)", i, got)
		}
	}
}

func TestResolveDeclaredContentTypes_RespectsEffectiveBasePath(t *testing.T) {
	st := mustParseSpec(t, jsonOpSpec)
	st.effectiveBasePath = "/api"
	specs := map[string]*specState{"main": st}
	got := resolveDeclaredContentTypes(specs, "POST", "/api/v1/query")
	if len(got) != 1 || got[0] != "application/json" {
		t.Errorf("declared with basePath = %v; want [application/json]", got)
	}
	if got := resolveDeclaredContentTypes(specs, "POST", "/v1/query"); got != nil {
		t.Errorf("declared without basePath prefix = %v; want nil", got)
	}
}

// TestEncodeBody_CatalogJSON_StringBodyParses is the issue #453
// fixture-2 case at the unit level: catalog declares JSON, no caller
// header, body is a JSON-looking string. The fix must send the bytes
// verbatim with Content-Type: application/json.
func TestEncodeBody_CatalogJSON_StringBodyParses(t *testing.T) {
	body, ct, err := encodeBody("POST", `{"sql":"SELECT 1"}`,
		[]string{"application/json"}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q; want application/json", ct)
	}
	if string(body) != `{"sql":"SELECT 1"}` {
		t.Errorf("body altered: %q", string(body))
	}
}

// TestEncodeBody_CatalogJSON_StringBodyNotJSON proves fixture 3:
// when the string is not JSON, the algorithm falls back to today's
// text/plain behavior even though the catalog declared JSON. The
// user explicitly meant a literal text payload that happens to be
// a string.
func TestEncodeBody_CatalogJSON_StringBodyNotJSON(t *testing.T) {
	body, ct, err := encodeBody("POST", "this is not json",
		[]string{"application/json"}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q; want text/plain*", ct)
	}
	if string(body) != "this is not json" {
		t.Errorf("body altered: %q", string(body))
	}
}

// TestEncodeBody_CatalogJSON_DictBodyUnchanged proves the fix does
// not change today's path for dict bodies. They still serialize as
// JSON with Content-Type: application/json.
func TestEncodeBody_CatalogJSON_DictBodyUnchanged(t *testing.T) {
	body, ct, err := encodeBody("POST", map[string]any{"sql": "SELECT 1"},
		[]string{"application/json"}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(string(body), `"sql":"SELECT 1"`) {
		t.Errorf("body = %s", string(body))
	}
}

// TestEncodeBody_CallerHeaderWins proves fixture 4: when the caller
// supplies Content-Type explicitly, the catalog hint is ignored and
// the string body passes through verbatim with today's type-driven
// content type (text/plain). buildRequest then overrides with the
// caller's header on the wire. We test the encodeBody-layer
// contract here; the buildRequest override is exercised by the
// integration test below.
func TestEncodeBody_CallerHeaderWins(t *testing.T) {
	body, ct, err := encodeBody("POST", "this is not json",
		[]string{"application/json"},
		map[string]string{"Content-Type": "application/json"})
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("encodeBody emitted ct = %q; want text/plain* (caller-header path)", ct)
	}
	if string(body) != "this is not json" {
		t.Errorf("body altered: %q", string(body))
	}
}

// TestEncodeBody_CallerHeaderWins_StringJSONBody confirms that even
// when the body would parse as JSON, an explicit caller Content-Type
// keeps encodeBody on the today's-behavior path (text/plain for
// strings). buildRequest preserves the caller's header on the wire.
func TestEncodeBody_CallerHeaderWins_StringJSONBody(t *testing.T) {
	_, ct, err := encodeBody("POST", `{"a":1}`,
		[]string{"application/json"},
		map[string]string{"content-type": "application/json"})
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("ct = %q; want today's type-driven text/plain when caller pinned Content-Type", ct)
	}
}

// TestEncodeBody_CatalogNonJSON_StringBodyVerbatim proves the
// non-JSON declared media-type branch: the string body lands on the
// wire untouched with the declared Content-Type.
func TestEncodeBody_CatalogNonJSON_StringBodyVerbatim(t *testing.T) {
	body, ct, err := encodeBody("POST", "<feed/>",
		[]string{"application/xml"}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if ct != "application/xml" {
		t.Errorf("content-type = %q; want application/xml", ct)
	}
	if string(body) != "<feed/>" {
		t.Errorf("body altered: %q", string(body))
	}
}

// TestEncodeBody_CatalogNonJSON_DictFallsBack proves we do NOT
// transform a structured body into a non-JSON media type. The
// gateway has no XML/CSV serializer, so dict bodies still emit JSON
// when the catalog declares only a non-JSON media type. The model
// can pass a string explicitly when it wants the declared media
// type.
func TestEncodeBody_CatalogNonJSON_DictFallsBack(t *testing.T) {
	body, ct, err := encodeBody("POST", map[string]any{"k": "v"},
		[]string{"application/xml"}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q; want application/json (no XML serializer)", ct)
	}
	if !strings.Contains(string(body), `"k":"v"`) {
		t.Errorf("body = %s", string(body))
	}
}

// TestEncodeBody_MultipleDeclared_PrefersJSON confirms that when
// multiple media types are declared and JSON is one of them, JSON
// wins for both dict and JSON-string bodies.
func TestEncodeBody_MultipleDeclared_PrefersJSON(t *testing.T) {
	body, ct, err := encodeBody("POST", `{"a":1}`,
		[]string{"application/json", "application/xml"}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("ct = %q; want application/json (json wins among multiple)", ct)
	}
	if string(body) != `{"a":1}` {
		t.Errorf("body altered: %q", string(body))
	}
}

func TestCallerSetsContentType_CaseInsensitive(t *testing.T) {
	cases := []map[string]string{
		{"Content-Type": "application/json"},
		{"content-type": "application/json"},
		{"CONTENT-TYPE": "application/json"},
	}
	for _, h := range cases {
		if !callerSetsContentType(h) {
			t.Errorf("callerSetsContentType(%v) = false; want true", h)
		}
	}
	if callerSetsContentType(map[string]string{"Accept-Language": "en"}) {
		t.Error("callerSetsContentType found a non-existent Content-Type")
	}
	if callerSetsContentType(nil) {
		t.Error("callerSetsContentType(nil) = true; want false")
	}
}

// invokeWithSpecsHarness is a small helper that wires up a real
// httptest.Server, a *conn with parsed specs, and runs invoke()
// against the assembled pipeline. The returned record reflects what
// the upstream actually saw (Content-Type header and body bytes),
// which is the only signal that proves the fix landed end to end.
type invokeRecord struct {
	contentType string
	body        []byte
	status      int
}

func runInvokeWithSpecs(t *testing.T, specYAML string, in InvokeInput) invokeRecord {
	t.Helper()
	rec := invokeRecord{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.contentType = r.Header.Get("Content-Type")
		rec.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		BaseURL:          srv.URL,
		AuthMode:         AuthModeNone,
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	specs := map[string]*specState{"main": mustParseSpec(t, specYAML)}
	out, err := invoke(context.Background(), invocation{
		cfg: cfg, auth: auth, client: newHTTPClient(cfg), specs: specs,
	}, in)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	rec.status = out.Status
	return rec
}

// TestInvoke_EndToEnd_SpecDrivenJSON_DictBody is fixture 1 in
// issue #453: catalog declares JSON, body is an object, no explicit
// header. Today's behavior already worked for dicts because
// json.Marshal sets application/json, but we keep the test so any
// future refactor that breaks it gets caught.
func TestInvoke_EndToEnd_SpecDrivenJSON_DictBody(t *testing.T) {
	rec := runInvokeWithSpecs(t, jsonOpSpecTemplated, InvokeInput{
		Connection: "x", Method: "POST",
		Path: "/v1/datasets/query/execute/b72dfd07-dc16-4335-b539-3badcf728959",
		Body: map[string]any{"sql": "SELECT 1"},
	})
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.status)
	}
	if rec.contentType != "application/json" {
		t.Errorf("upstream saw Content-Type=%q; want application/json (fixture 1)", rec.contentType)
	}
	var parsed map[string]any
	if err := json.Unmarshal(rec.body, &parsed); err != nil {
		t.Errorf("upstream body did not round-trip as JSON: %v (body=%s)", err, string(rec.body))
	}
	if parsed["sql"] != "SELECT 1" {
		t.Errorf("upstream body = %s", string(rec.body))
	}
}

// TestInvoke_EndToEnd_SpecDrivenJSON_StringBody is fixture 2: a
// pre-serialized JSON string as the body lands on the wire with
// Content-Type: application/json. THIS is the test that today's
// gateway fails: without the fix, the upstream would see
// text/plain.
func TestInvoke_EndToEnd_SpecDrivenJSON_StringBody(t *testing.T) {
	rec := runInvokeWithSpecs(t, jsonOpSpecTemplated, InvokeInput{
		Connection: "x", Method: "POST",
		Path: "/v1/datasets/query/execute/b72dfd07-dc16-4335-b539-3badcf728959",
		Body: `{"sql":"SELECT 1"}`,
	})
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.status)
	}
	if rec.contentType != "application/json" {
		t.Errorf("upstream saw Content-Type=%q; want application/json (fixture 2)", rec.contentType)
	}
	if string(rec.body) != `{"sql":"SELECT 1"}` {
		t.Errorf("upstream body altered: %q", string(rec.body))
	}
}

// TestInvoke_EndToEnd_SpecDrivenJSON_StringBodyNotJSON is fixture 3:
// a literal string that does NOT parse as JSON keeps today's
// text/plain behavior even when the catalog declares JSON. The
// upstream's 400 is its decision; the gateway must not silently
// upgrade arbitrary text to application/json.
func TestInvoke_EndToEnd_SpecDrivenJSON_StringBodyNotJSON(t *testing.T) {
	rec := runInvokeWithSpecs(t, jsonOpSpecTemplated, InvokeInput{
		Connection: "x", Method: "POST",
		Path: "/v1/datasets/query/execute/b72dfd07-dc16-4335-b539-3badcf728959",
		Body: "this is not json",
	})
	if !strings.HasPrefix(rec.contentType, "text/plain") {
		t.Errorf("upstream saw Content-Type=%q; want text/plain* (fixture 3 unchanged)", rec.contentType)
	}
	if string(rec.body) != "this is not json" {
		t.Errorf("body altered: %q", string(rec.body))
	}
}

// TestInvoke_EndToEnd_CallerHeaderWins is fixture 4: an explicit
// Content-Type from the model survives end-to-end, even when the
// catalog could have driven a different choice. This is the
// pre-fix workaround that must continue to work.
func TestInvoke_EndToEnd_CallerHeaderWins(t *testing.T) {
	rec := runInvokeWithSpecs(t, jsonOpSpecTemplated, InvokeInput{
		Connection: "x", Method: "POST",
		Path:    "/v1/datasets/query/execute/b72dfd07-dc16-4335-b539-3badcf728959",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"sql":"SELECT 1"}`,
	})
	if rec.contentType != "application/json" {
		t.Errorf("upstream saw Content-Type=%q; want application/json (caller header wins)", rec.contentType)
	}
	if string(rec.body) != `{"sql":"SELECT 1"}` {
		t.Errorf("body altered: %q", string(rec.body))
	}
}

// TestInvoke_EndToEnd_NoCatalogMatch_StringBody confirms that a
// model-supplied path that does not match any catalog operation
// falls back to today's type-driven behavior: text/plain for a
// string body. This is the "no rush, workaround still applies"
// fallback the issue calls out.
func TestInvoke_EndToEnd_NoCatalogMatch_StringBody(t *testing.T) {
	rec := runInvokeWithSpecs(t, jsonOpSpecTemplated, InvokeInput{
		Connection: "x", Method: "POST",
		Path: "/v2/unrelated",
		Body: `{"sql":"SELECT 1"}`,
	})
	if !strings.HasPrefix(rec.contentType, "text/plain") {
		t.Errorf("upstream saw Content-Type=%q; want text/plain* (no catalog match)", rec.contentType)
	}
}

// TestInvoke_EndToEnd_NonJSONDeclared_StringBody confirms a string
// body lands on the wire with the declared non-JSON media type
// (application/xml here). Mirrors fixture 4 from the algorithm
// section.
func TestInvoke_EndToEnd_NonJSONDeclared_StringBody(t *testing.T) {
	rec := runInvokeWithSpecs(t, xmlOpSpec, InvokeInput{
		Connection: "x", Method: "POST",
		Path: "/v1/feed",
		Body: "<feed/>",
	})
	if rec.contentType != "application/xml" {
		t.Errorf("upstream saw Content-Type=%q; want application/xml", rec.contentType)
	}
	if string(rec.body) != "<feed/>" {
		t.Errorf("body altered: %q", string(rec.body))
	}
}
