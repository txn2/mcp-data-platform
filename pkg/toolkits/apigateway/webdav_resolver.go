package apigateway

import (
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// extWebDAVMethod is the OpenAPI extension a catalog uses to declare the
// real HTTP verb a carrier operation stands in for. WebDAV verbs
// (PROPFIND, MKCOL, MOVE, COPY) are not representable as OpenAPI PathItem
// methods, so a spec documents them under an unused standard method
// (POST/PATCH/HEAD) and records the true verb here. The value is
// free-text and may name more than one verb (e.g. "MOVE or COPY"); see
// webdavMethodsFromExtension.
const extWebDAVMethod = "x-webdav-method"

// webdavVerbs is the set of extension-declared HTTP methods the resolver
// recognizes inside an x-webdav-method value. These are exactly the
// WebDAV verbs api_invoke_endpoint accepts that OpenAPI cannot express as
// a PathItem field (GET/PUT/DELETE WebDAV operations use their own
// standard method and need no extension), i.e. supportedMethods minus the
// standard pathItemMethods. TestWebdavVerbs_SubsetOfSupportedMethods fails
// if this set drifts from that derivation.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var webdavVerbs = map[string]bool{
	"PROPFIND": true,
	"MKCOL":    true,
	"MOVE":     true,
	"COPY":     true,
}

// webdavOp is the resolution result for one HTTP method on a WebDAV
// route: the operationId used as the metric label and the sorted
// requestBody media types used for invoke-side Content-Type negotiation.
// Both are derived from the same cataloged operation so the metric label
// and the negotiated media type can never disagree.
type webdavOp struct {
	operationID  string
	contentTypes []string
}

// webdavRoute is one WebDAV-flavored path template in a connection's
// catalog: a PathItem that carries at least one x-webdav-method
// operation. WebDAV resource paths hold a slash-bearing subpath in their
// trailing variable, so the template is matched with a multi-segment tail
// rather than the router's single-segment variable.
type webdavRoute struct {
	// segments is the effectiveBasePath-prefixed template split on "/"
	// (e.g. ["", "remote.php", "dav", "files", "{username}", "{path}"]).
	// A trailing placeholder is a catch-all consuming zero or more
	// concrete segments (a collection root supplies none); interior
	// placeholders match a single segment. All specificity ranking is
	// derived from this slice so there is one source of truth.
	segments []string
	// literals is the count of non-placeholder (fixed) segments in
	// segments, precomputed at build time because moreSpecific reads it for
	// every specificity comparison. A higher count means a more constrained,
	// more specific route.
	literals int
	// methods maps a caller's HTTP method to the operation serving it on
	// this path. Every operation contributes its carrier/standard method
	// (so nested-path calls agree with the gorillamux router's
	// single-segment resolution); a WebDAV carrier additionally contributes
	// each real verb its x-webdav-method extension names.
	methods map[string]webdavOp
}

// buildWebDAVRoutes scans a connection's component specs and returns the
// WebDAV-flavored path templates: those whose PathItem declares at least
// one x-webdav-method operation. Non-WebDAV catalogs (the common case)
// produce an empty slice, making the resolver's WebDAV fallback a no-op.
func buildWebDAVRoutes(specs map[string]*specState) []webdavRoute {
	var routes []webdavRoute
	for _, st := range specs {
		if st == nil || st.doc == nil || st.doc.Paths == nil {
			continue
		}
		for rawPath, item := range st.doc.Paths.Map() {
			methods := webdavMethodOps(item, rawPath)
			if len(methods) == 0 {
				continue // not a WebDAV-flavored path item
			}
			segments := splitPathTemplate(st.effectiveBasePath + rawPath)
			routes = append(routes, webdavRoute{
				segments: segments,
				literals: countLiteralSegments(segments),
				methods:  methods,
			})
		}
	}
	return routes
}

// webdavMethodOps returns the method -> webdavOp map for a WebDAV-flavored
// PathItem in a single pass, or nil when the item declares no
// x-webdav-method operation (so buildWebDAVRoutes skips it and standard
// router resolution remains the only path for that template). Each
// operation maps its own carrier/standard method — so a nested-path call
// to a carrier verb resolves the same operation the router resolves on a
// single-segment path — and a carrier additionally maps every real WebDAV
// verb its x-webdav-method extension names. Each webdavOp carries both the
// operationId (metric label) and the declared requestBody media types
// (Content-Type negotiation) so both invoke and metrics read one source.
// Operations without a declared operationId synthesize the same id
// api_list_endpoints advertises so the metric label cannot diverge from
// the listed id.
func webdavMethodOps(item *openapi3.PathItem, rawPath string) map[string]webdavOp {
	if item == nil {
		return nil
	}
	// One pass tokenizes each operation's x-webdav-method exactly once into
	// a small scratch slice and defers the map + content-type work until the
	// item is confirmed WebDAV-flavored, so the common non-WebDAV path item
	// allocates only the scratch slice and never re-parses an extension.
	carriers := make([]webdavCarrier, 0, len(pathItemMethods))
	isWebDAV := false
	for _, m := range pathItemMethods {
		op := m.get(item)
		if op == nil {
			continue
		}
		verbs := webdavMethodsFromExtension(op)
		if len(verbs) > 0 {
			isWebDAV = true
		}
		carriers = append(carriers, webdavCarrier{method: m.method, op: op, verbs: verbs})
	}
	if !isWebDAV {
		return nil
	}
	out := make(map[string]webdavOp, len(carriers))
	for _, c := range carriers {
		entry := webdavOp{
			operationID:  operationIDOrSynthesized(c.op, c.method, rawPath),
			contentTypes: requestBodyContentTypes(c.op),
		}
		out[c.method] = entry
		for _, v := range c.verbs {
			// First carrier in pathItemMethods order wins so a malformed
			// spec that names the same WebDAV verb on two carriers resolves
			// deterministically instead of silently keeping the later one.
			if _, exists := out[v]; !exists {
				out[v] = entry
			}
		}
	}
	return out
}

// webdavCarrier is one operation on a path item during the single-pass
// build in webdavMethodOps: its carrier method, the operation, and the
// real WebDAV verbs its x-webdav-method extension names (nil for a plain
// operation). Holds the tokenized verbs so the extension is parsed once.
type webdavCarrier struct {
	method string
	op     *openapi3.Operation
	verbs  []string
}

// requestBodyContentTypes returns the operation's declared requestBody
// media types, sorted, or nil when it declares no request body. Shared so
// the WebDAV route index carries the same media types the standard
// Content-Type path derives via sortedContentTypes.
func requestBodyContentTypes(op *openapi3.Operation) []string {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	return sortedContentTypes(op.RequestBody.Value.Content)
}

// webdavMethodsFromExtension extracts the real WebDAV verbs a carrier
// operation stands in for from its x-webdav-method value. kin-openapi
// stores an x- extension as its decoded value, so the extension may be a
// free-text string naming one verb ("PROPFIND") or several ("MOVE or
// COPY"), or a natural YAML/JSON sequence ([MOVE, COPY]); both are
// accepted. Every recognized WebDAV verb token is returned, matched
// case-insensitively. Returns nil when the extension is absent, is an
// unusable shape, or names no known verb.
func webdavMethodsFromExtension(op *openapi3.Operation) []string {
	if op == nil || op.Extensions == nil {
		return nil
	}
	raw := webdavExtensionText(op.Extensions[extWebDAVMethod])
	if raw == "" {
		return nil
	}
	var verbs []string
	seen := make(map[string]bool)
	splitOnNonLetter := func(r rune) bool { return !isASCIILetter(r) }
	for _, tok := range strings.FieldsFunc(raw, splitOnNonLetter) {
		v := strings.ToUpper(tok)
		if webdavVerbs[v] && !seen[v] {
			seen[v] = true
			verbs = append(verbs, v)
		}
	}
	return verbs
}

// webdavExtensionText flattens an x-webdav-method extension value to the
// text the verb tokenizer scans. A string ("MOVE or COPY") is returned
// as-is; a sequence ([MOVE, COPY]) is joined with spaces so a natural
// YAML/JSON list is accepted too. Any other shape yields "".
func webdavExtensionText(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

// isASCIILetter reports whether r is an ASCII letter. Used to tokenize a
// free-text x-webdav-method value ("MOVE or COPY" -> "MOVE", "or",
// "COPY") on any non-letter run so each candidate verb is checked in
// isolation.
func isASCIILetter(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// resolveWebDAVOperation returns the operationId a WebDAV request resolves
// to for the metric label, or "" when no WebDAV template matches.
func (c *conn) resolveWebDAVOperation(upper, normPath string) string {
	if op, ok := resolveWebDAVRoute(c.webdavRoutes(), upper, normPath); ok {
		return op.operationID
	}
	return ""
}

// resolveWebDAVRoute matches an already-normalized (upper-cased method,
// leading-slash, query-stripped path) request against a WebDAV route index
// and returns the operation serving it; ok is false when no route matches.
// When more than one template matches, the most specific wins (see
// moreSpecific) so a broad catch-all never shadows a more specific
// template, and ties resolve deterministically. This is the single matcher
// both the metric resolver and the invoke-side Content-Type negotiation
// call, so the operationId label and the negotiated media type always come
// from the same operation (issue #876).
func resolveWebDAVRoute(routes []webdavRoute, upper, normPath string) (op webdavOp, ok bool) {
	if len(routes) == 0 {
		return webdavOp{}, false
	}
	segs := splitPathTemplate(normPath)
	var best webdavRoute
	for i := range routes {
		r := routes[i]
		entry, has := r.methods[upper]
		if !has || !r.matches(segs) {
			continue
		}
		if !ok || moreSpecific(r, entry.operationID, best, op.operationID) {
			best, op, ok = r, entry, true
		}
	}
	return op, ok
}

// moreSpecific reports whether route a (serving operationId aID) is a more
// specific match than the current best b (serving bID): more literal
// (non-placeholder) segments wins, then the longer template (more required
// segments), then the lexically smaller operationId so selection is
// deterministic regardless of the map-iteration order buildWebDAVRoutes
// appended in. Ranking by literal count makes a literal-tail template beat
// an equal-placeholder catch-all template for the same concrete path.
func moreSpecific(a webdavRoute, aID string, b webdavRoute, bID string) bool {
	if a.literals != b.literals {
		return a.literals > b.literals
	}
	if len(a.segments) != len(b.segments) {
		return len(a.segments) > len(b.segments)
	}
	return aID < bID
}

// countLiteralSegments returns the number of fixed (placeholder-free)
// segments in a split template. Computed once at build time and cached on
// webdavRoute.literals because moreSpecific compares it for every matching
// route on every request. A partially-templated segment ("{name}.json")
// is not fixed, so it does not count (issue #1297).
func countLiteralSegments(segments []string) int {
	n := 0
	for _, s := range segments {
		if !segmentIsTemplated(s) {
			n++
		}
	}
	return n
}

// matches reports whether the concrete path segments satisfy this WebDAV
// route's template. Interior segments (all but the last) match under the
// same placeholder/literal rule as the standard router via segmentMatches.
// The trailing placeholder is a catch-all consuming the remaining
// segments, which may be zero (a PROPFIND on a user's collection root,
// e.g. /remote.php/dav/files/alice, supplies no subpath yet is a real
// WebDAV resource) or many (a nested resource path). Only a whole-segment
// placeholder is a catch-all; a trailing segment that mixes literal text
// with placeholders is matched as a single segment by segmentMatches, the
// same rule the interior segments use.
func (r webdavRoute) matches(concrete []string) bool {
	t := r.segments
	if len(t) == 0 {
		return false
	}
	last := len(t) - 1
	catchAll := isPlaceholderSegment(t[last])
	// A trailing catch-all needs only the interior segments (it may consume
	// zero tail segments); a trailing literal needs the exact segment count.
	if catchAll {
		if len(concrete) < last {
			return false
		}
	} else if len(concrete) != len(t) {
		return false
	}
	for i := range last {
		if !segmentMatches(concrete[i], t[i]) {
			return false
		}
	}
	if catchAll {
		// Catch-all tail: concrete[last:] holds the (possibly empty or
		// nested) subpath. Any remaining segments, or none, match.
		return true
	}
	// Trailing non-catch-all: single-segment match under the same rule as
	// the interior segments. len(concrete) == len(t) is guaranteed above,
	// so concrete[last] is in bounds.
	return segmentMatches(concrete[last], t[last])
}
