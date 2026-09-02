package apigateway

import (
	"context"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// webdavTestSpec mirrors the shape of a real Nextcloud WebDAV catalog
// spec (issue #876): a single resource path whose WebDAV verbs are
// documented under carrier methods via x-webdav-method. POST->PROPFIND,
// PATCH->MKCOL, HEAD->MOVE/COPY (two verbs in one free-text value);
// GET/PUT/DELETE are their own real verbs and carry no extension. The
// literal /remote.php/dav/files/shared/index.html path exercises
// most-specific-wins: an exact literal must not be shadowed by the
// catch-all template.
const webdavTestSpec = `{
  "openapi": "3.0.3",
  "info": { "title": "webdav", "version": "1.0.0" },
  "paths": {
    "/remote.php/dav/files/{username}/{path}": {
      "parameters": [
        { "name": "username", "in": "path", "required": true, "schema": { "type": "string" } },
        { "name": "path", "in": "path", "required": true, "schema": { "type": "string" } }
      ],
      "get": { "operationId": "webdav-download-file", "responses": { "200": { "description": "ok" } } },
      "put": { "operationId": "webdav-upload-file", "responses": { "201": { "description": "created" } } },
      "delete": { "operationId": "webdav-delete", "responses": { "204": { "description": "deleted" } } },
      "post": {
        "operationId": "webdav-propfind",
        "x-webdav-method": "PROPFIND",
        "responses": { "207": { "description": "multistatus" } }
      },
      "patch": {
        "operationId": "webdav-mkcol",
        "x-webdav-method": "MKCOL",
        "responses": { "201": { "description": "created" } }
      },
      "head": {
        "operationId": "webdav-move-copy",
        "x-webdav-method": "MOVE or COPY",
        "responses": { "201": { "description": "moved" } }
      }
    },
    "/remote.php/dav/files/shared/index.html": {
      "get": { "operationId": "shared-index", "responses": { "200": { "description": "ok" } } }
    }
  }
}`

// newWebDAVTestToolkit builds a toolkit with one connection whose single
// spec is the WebDAV fixture, rebased under basePath. Constructed
// directly (no catalog store) so the test exercises the resolver end to
// end: router build + WebDAV fallback.
func newWebDAVTestToolkit(t *testing.T, connName, basePath string) *Toolkit {
	t.Helper()
	doc, err := parseOpenAPISpec(webdavTestSpec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	tk := New("test")
	tk.connections[connName] = &conn{
		specs: map[string]*specState{
			"webdav": {doc: doc, effectiveBasePath: basePath},
		},
	}
	return tk
}

func TestResolveOperationID_WebDAV(t *testing.T) {
	tk := newWebDAVTestToolkit(t, "nc", "")
	ctx := context.Background()

	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		// Root cause A: real WebDAV verbs the router has no route for
		// (registered under carrier methods) must resolve via the
		// x-webdav-method mapping, on both single-segment and nested tails.
		{"propfind single segment", "PROPFIND", "/remote.php/dav/files/alice/report.pdf", "webdav-propfind"},
		{"propfind nested", "PROPFIND", "/remote.php/dav/files/alice/reports/2026/q1/report.pdf", "webdav-propfind"},
		{"propfind trailing slash dir", "PROPFIND", "/remote.php/dav/files/alice/reports/", "webdav-propfind"},
		{"mkcol nested", "MKCOL", "/remote.php/dav/files/alice/reports/2026", "webdav-mkcol"},
		{"move nested", "MOVE", "/remote.php/dav/files/alice/a/b.pdf", "webdav-move-copy"},
		{"copy nested", "COPY", "/remote.php/dav/files/alice/a/b.pdf", "webdav-move-copy"},
		{"lowercase webdav verb normalized", "propfind", "/remote.php/dav/files/alice/x.pdf", "webdav-propfind"},

		// Root cause B: standard verbs on nested (multi-segment) paths
		// miss the router's single-segment variable and must resolve via
		// the catch-all tail.
		{"put nested", "PUT", "/remote.php/dav/files/alice/reports/2026/q1/report.pdf", "webdav-upload-file"},
		{"get nested", "GET", "/remote.php/dav/files/alice/reports/2026/q1/report.pdf", "webdav-download-file"},
		{"delete nested", "DELETE", "/remote.php/dav/files/alice/a/b/c", "webdav-delete"},

		// Standard verbs on single-segment tails already resolved via the
		// router before this fix; assert they still do (no regression).
		{"put single segment via router", "PUT", "/remote.php/dav/files/alice/report.pdf", "webdav-upload-file"},
		{"get single segment via router", "GET", "/remote.php/dav/files/alice/report.pdf", "webdav-download-file"},

		// A carrier verb (POST/PATCH/HEAD) sent literally resolves to the
		// carried operation on BOTH single-segment (router) and nested
		// (fallback) paths, so the same verb+path family is never split
		// across two metric buckets by path depth.
		{"carrier post single segment via router", "POST", "/remote.php/dav/files/alice/report.pdf", "webdav-propfind"},
		{"carrier post nested via fallback", "POST", "/remote.php/dav/files/alice/a/b.pdf", "webdav-propfind"},

		// A PROPFIND on a user's collection root (no subpath) is a real
		// WebDAV directory-listing resource, so the catch-all tail matches
		// zero remaining segments and it resolves rather than bucketing to
		// unknown.
		{"propfind collection root", "PROPFIND", "/remote.php/dav/files/alice", "webdav-propfind"},
		{"propfind collection root trailing slash", "PROPFIND", "/remote.php/dav/files/alice/", "webdav-propfind"},

		// Genuine misses must still record unknown (empty), never a false
		// resolution: a WebDAV verb on a path outside the template prefix,
		// or a path too short to fill the template's interior segments.
		{"webdav verb outside template prefix", "PROPFIND", "/completely/different/path", ""},
		{"webdav verb partial prefix", "MKCOL", "/remote.php/dav/other/alice/x", ""},
		{"path too short for interior segments", "PROPFIND", "/remote.php/dav/files", ""},
		{"unknown connection", "PROPFIND", "/remote.php/dav/files/alice/x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := "nc"
			if tt.name == "unknown connection" {
				conn = "missing"
			}
			got := tk.ResolveOperationID(ctx, conn, tt.method, tt.path)
			if got != tt.want {
				t.Errorf("ResolveOperationID(%q, %q) = %q; want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

// TestResolveOperationID_WebDAV_MostSpecificWins proves the multi-segment
// catch-all never shadows a more specific literal route: an exact match
// on /remote.php/dav/files/shared/index.html resolves to the literal op,
// not webdav-download-file, because the router (most-specific-wins) runs
// before the WebDAV fallback.
func TestResolveOperationID_WebDAV_MostSpecificWins(t *testing.T) {
	tk := newWebDAVTestToolkit(t, "nc", "")
	ctx := context.Background()

	if got := tk.ResolveOperationID(ctx, "nc", "GET", "/remote.php/dav/files/shared/index.html"); got != "shared-index" {
		t.Errorf("literal path: got %q; want shared-index", got)
	}
	// A nested GET under a different user still falls through to the
	// catch-all download operation.
	if got := tk.ResolveOperationID(ctx, "nc", "GET", "/remote.php/dav/files/bob/a/b.txt"); got != "webdav-download-file" {
		t.Errorf("nested path: got %q; want webdav-download-file", got)
	}
}

// TestResolveOperationID_WebDAV_BasePath verifies the WebDAV fallback
// honors effectiveBasePath: the runtime path the resolver receives is the
// basePath-prefixed full path, matching what api_discover reports.
func TestResolveOperationID_WebDAV_BasePath(t *testing.T) {
	tk := newWebDAVTestToolkit(t, "nc", "/nc")
	ctx := context.Background()

	if got := tk.ResolveOperationID(ctx, "nc", "PROPFIND", "/nc/remote.php/dav/files/alice/a/b.pdf"); got != "webdav-propfind" {
		t.Errorf("with base path: got %q; want webdav-propfind", got)
	}
	// Without the base-path prefix the path is genuinely absent.
	if got := tk.ResolveOperationID(ctx, "nc", "PROPFIND", "/remote.php/dav/files/alice/a/b.pdf"); got != "" {
		t.Errorf("missing base path: got %q; want empty", got)
	}
}

func TestWebdavMethodsFromExtension(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want []string
	}{
		{"single verb", `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{"/p":{"post":{"operationId":"a","x-webdav-method":"PROPFIND","responses":{"200":{"description":"ok"}}}}}}`, []string{"PROPFIND"}},
		{"two verbs free text", `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{"/p":{"head":{"operationId":"a","x-webdav-method":"MOVE or COPY","responses":{"200":{"description":"ok"}}}}}}`, []string{"MOVE", "COPY"}},
		{"json array value", `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{"/p":{"head":{"operationId":"a","x-webdav-method":["MOVE","COPY"],"responses":{"200":{"description":"ok"}}}}}}`, []string{"MOVE", "COPY"}},
		{"lowercase value", `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{"/p":{"patch":{"operationId":"a","x-webdav-method":"mkcol","responses":{"200":{"description":"ok"}}}}}}`, []string{"MKCOL"}},
		{"no extension", `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{"/p":{"get":{"operationId":"a","responses":{"200":{"description":"ok"}}}}}}`, nil},
		{"unknown verb ignored", `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{"/p":{"post":{"operationId":"a","x-webdav-method":"REPORT","responses":{"200":{"description":"ok"}}}}}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := parseOpenAPISpec(tt.spec)
			if err != nil {
				t.Fatalf("parseOpenAPISpec: %v", err)
			}
			item := doc.Paths.Value("/p")
			got := webdavMethodsFromExtension(firstOperation(item))
			if !slices.Equal(got, tt.want) {
				t.Errorf("webdavMethodsFromExtension = %v; want %v", got, tt.want)
			}
		})
	}
}

// TestResolveWebDAVOperation_Specificity drives the fallback directly with
// overlapping WebDAV templates so most-specific-wins is exercised in
// isolation: a more-literal template beats a broader one, and a
// literal-tail template beats an equal-placeholder catch-all template for
// the same concrete path.
func TestResolveWebDAVOperation_Specificity(t *testing.T) {
	c := &conn{
		operationWebDAVRoutes: []webdavRoute{
			wdRoute("/dav/{user}/{path}", "generic"),
			wdRoute("/dav/admin/{path}", "admin-specific"),
			wdRoute("/dav/{user}/{file}/meta", "meta-specific"),
			wdRoute("/dav/{a}/{b}/{c}", "triple"),
		},
	}
	// Mark the sync.Once as already done so webdavRoutes() does not rebuild
	// from (nil) specs and clobber the hand-built routes.
	c.operationRouterOnce.Do(func() {})

	tests := []struct {
		name string
		path string
		want string
	}{
		{"more-literal prefix wins", "/dav/admin/a/b", "admin-specific"},
		{"literal tail beats equal-placeholder catch-all", "/dav/alice/x/meta", "meta-specific"},
		// Equal literal count (both 2), so the longer template (more
		// required segments) is the more specific match.
		{"longer template wins on equal literals", "/dav/x/y/z", "triple"},
		// Only the two-placeholder catch-all reaches a collection root; the
		// longer templates require more segments than the path supplies.
		{"catch-all resolves collection root", "/dav/alice", "generic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.resolveWebDAVOperation("PROPFIND", tt.path); got != tt.want {
				t.Errorf("resolveWebDAVOperation(%q) = %q; want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestResolveWebDAVOperation_EqualSpecificityDeterministic proves that when
// two templates match with identical specificity (e.g. the same WebDAV
// template declared in two component specs), the lexically smaller
// operationId wins regardless of the order buildWebDAVRoutes appended
// them, so the metric label cannot flip across restarts.
func TestResolveWebDAVOperation_EqualSpecificityDeterministic(t *testing.T) {
	routeA := wdRoute("/dav/{user}/{path}", "aaa")
	routeB := wdRoute("/dav/{user}/{path}", "bbb")

	for _, order := range [][]webdavRoute{{routeA, routeB}, {routeB, routeA}} {
		c := &conn{operationWebDAVRoutes: order}
		c.operationRouterOnce.Do(func() {})
		if got := c.resolveWebDAVOperation("PROPFIND", "/dav/alice/x"); got != "aaa" {
			t.Errorf("equal-specificity tiebreak: got %q; want aaa (deterministic)", got)
		}
	}
}

// TestWebdavVerbs_SubsetOfSupportedMethods guards against drift between
// webdavVerbs and supportedMethods: webdavVerbs must be exactly the
// supported methods that are NOT standard OpenAPI PathItem methods (the
// verbs that can only be documented via x-webdav-method carriers). Adding
// a new WebDAV verb to supportedMethods without updating webdavVerbs would
// silently drop it from extension resolution; this test fails first.
func TestWebdavVerbs_SubsetOfSupportedMethods(t *testing.T) {
	standard := make(map[string]bool)
	for _, m := range pathItemMethods {
		standard[m.method] = true
	}
	want := make(map[string]bool)
	for m := range supportedMethods {
		if !standard[m] {
			want[m] = true
		}
	}
	if len(want) != len(webdavVerbs) {
		t.Fatalf("webdavVerbs size = %d; want %d (non-standard supported methods)", len(webdavVerbs), len(want))
	}
	for v := range want {
		if !webdavVerbs[v] {
			t.Errorf("supported non-standard method %q missing from webdavVerbs", v)
		}
	}
}

// TestWebdavRouteMatches drives the segment matcher directly across the
// branches the catalog fixtures do not all reach: a trailing-literal
// template, a bare-trailing-slash tail, and a too-short path.
func TestWebdavRouteMatches(t *testing.T) {
	catchAll := webdavRoute{segments: splitPathTemplate("/dav/{user}/{path}")}
	literalTail := webdavRoute{segments: splitPathTemplate("/dav/{user}/config")}

	tests := []struct {
		name string
		r    webdavRoute
		path string
		want bool
	}{
		{"catch-all single tail", catchAll, "/dav/alice/a", true},
		{"catch-all nested tail", catchAll, "/dav/alice/a/b/c", true},
		{"catch-all matches collection root", catchAll, "/dav/alice", true},
		{"catch-all matches collection root trailing slash", catchAll, "/dav/alice/", true},
		{"catch-all rejects missing interior segment", catchAll, "/dav", false},
		{"catch-all rejects wrong prefix", catchAll, "/other/alice/a", false},
		{"catch-all rejects empty interior", catchAll, "/dav//a", false},
		{"literal tail exact", literalTail, "/dav/alice/config", true},
		{"literal tail mismatch", literalTail, "/dav/alice/other", false},
		{"literal tail rejects extra segment", literalTail, "/dav/alice/config/x", false},
		{"empty template never matches", webdavRoute{segments: nil}, "/anything", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.matches(splitPathTemplate(tt.path)); got != tt.want {
				t.Errorf("matches(%q) = %v; want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestWebdavMethodOps_DuplicateVerbDeterministic covers the malformed-spec
// case where two carrier operations on one PathItem name the same WebDAV
// verb: the first in pathItemMethods order (POST before PATCH) wins
// deterministically rather than the map write silently keeping the later.
func TestWebdavMethodOps_DuplicateVerbDeterministic(t *testing.T) {
	spec := `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1" },
  "paths": {
    "/p": {
      "post": { "operationId": "post-op", "x-webdav-method": "MOVE", "responses": { "200": { "description": "ok" } } },
      "patch": { "operationId": "patch-op", "x-webdav-method": "MOVE", "responses": { "200": { "description": "ok" } } }
    }
  }
}`
	doc, err := parseOpenAPISpec(spec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	ops := webdavMethodOps(doc.Paths.Value("/p"), "/p")
	if got := ops["MOVE"].operationID; got != "post-op" {
		t.Errorf("duplicate MOVE verb resolved to %q; want post-op (first in pathItemMethods order)", got)
	}
}

// wdMethods builds a webdavRoute.methods map with PROPFIND mapped to an
// operationId (no content types), for tests that drive resolution directly
// with hand-built routes.
func wdMethods(operationID string) map[string]webdavOp {
	return map[string]webdavOp{"PROPFIND": {operationID: operationID}}
}

// wdRoute builds a full webdavRoute (segments, cached literal count, and a
// single PROPFIND->operationId method) for tests that exercise
// resolveWebDAVRoute's specificity ranking directly.
func wdRoute(template, operationID string) webdavRoute {
	segs := splitPathTemplate(template)
	return webdavRoute{segments: segs, literals: countLiteralSegments(segs), methods: wdMethods(operationID)}
}

// TestBuildWebDAVRoutes_NonWebDAVEmpty confirms a catalog with no
// x-webdav-method operation produces no WebDAV routes, so the fallback is
// a no-op and standard connections are wholly unaffected.
func TestBuildWebDAVRoutes_NonWebDAVEmpty(t *testing.T) {
	doc, err := parseOpenAPISpec(resolverTestSpec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec: %v", err)
	}
	routes := buildWebDAVRoutes(map[string]*specState{"users": {doc: doc}})
	if len(routes) != 0 {
		t.Errorf("buildWebDAVRoutes on non-WebDAV spec = %d routes; want 0", len(routes))
	}
}

// firstOperation returns the first non-nil operation on a PathItem in
// pathItemMethods order, for tests that declare a single operation.
func firstOperation(item *openapi3.PathItem) *openapi3.Operation {
	for _, m := range pathItemMethods {
		if op := m.get(item); op != nil {
			return op
		}
	}
	return nil
}
