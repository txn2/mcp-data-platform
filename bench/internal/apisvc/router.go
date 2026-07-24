package apisvc

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// route is one catalog operation compiled for matching: the method plus
// the path split into segments, where a "{...}" segment captures the id.
type route struct {
	method   string // uppercase
	segments []string
	op       apigen.Operation
}

// compileRoutes builds the route table from the full catalog. The service
// always serves the complete tier-2 surface; per-tier exposure is decided
// by which spec is registered with the platform (or placed in the b2
// workspace), not by the service.
func compileRoutes(c *apigen.Catalog) []route {
	routes := make([]route, 0, len(c.Operations))
	for _, op := range c.Operations {
		routes = append(routes, route{
			method:   strings.ToUpper(op.Method),
			segments: strings.Split(strings.TrimPrefix(op.Path, "/"), "/"),
			op:       op,
		})
	}
	return routes
}

// match finds the route for a request and extracts the id segment, if
// any. Returns ok=false when no route matches.
func matchRoute(routes []route, r *http.Request) (apigen.Operation, string, bool) {
	segs := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	for _, rt := range routes {
		if rt.method != r.Method || len(rt.segments) != len(segs) {
			continue
		}
		id, ok := matchSegments(rt.segments, segs)
		if ok {
			return rt.op, id, true
		}
	}
	return apigen.Operation{}, "", false
}

// matchSegments compares one route's segments against a request's,
// capturing the "{...}" wildcard. A wildcard segment may carry a ":verb"
// suffix ("{id}:cancel"), which must match literally after the capture.
func matchSegments(pattern, actual []string) (string, bool) {
	id := ""
	for i, p := range pattern {
		a := actual[i]
		if !strings.Contains(p, "{") {
			if p != a {
				return "", false
			}
			continue
		}
		captured, ok := matchWildcard(p, a)
		if !ok {
			return "", false
		}
		id = captured
	}
	return id, true
}

// matchWildcard matches one "{id}" or "{id}:verb" pattern segment.
func matchWildcard(pattern, actual string) (string, bool) {
	_, suffix, ok := strings.Cut(pattern, "}") // e.g. ":cancel", usually ""
	if !ok {
		return "", false
	}
	if suffix == "" {
		return actual, actual != ""
	}
	captured, found := strings.CutSuffix(actual, suffix)
	if !found || captured == "" {
		return "", false
	}
	return captured, true
}
