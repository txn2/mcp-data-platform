package admin

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// The API-route half of a persona definition (#1479). A persona's APIRouteRule
// list narrows which HTTP methods and paths it may invoke on the api-kind
// connections it already reaches, and until this file existed the admin API
// neither accepted nor returned it — so a persona edited in the portal was
// written back without the rules its file config gave it.

// normalizeAPIRoutes returns the rules as they are stored: whitespace trimmed,
// empty patterns dropped, methods uppercased, and an empty action written as
// the explicit "allow" it means.
//
// Methods are uppercased because the toolkit normalizes an inbound method to
// uppercase before the check runs and the patterns are matched case-sensitively
// — a rule typed as "delete" would otherwise be stored, displayed, and never
// match anything. Paths are left exactly as written: a path glob is the
// operator's, and rewriting one would stop a hand-written pattern round-tripping
// to the editor as the pattern it was typed as.
func normalizeAPIRoutes(rules []persona.APIRouteRule) []persona.APIRouteRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]persona.APIRouteRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, persona.APIRouteRule{
			Connection: strings.TrimSpace(r.Connection),
			Methods:    cleanPatterns(r.Methods, strings.ToUpper),
			Paths:      cleanPatterns(r.Paths, nil),
			Action:     normalizeRouteAction(r.Action),
		})
	}
	return out
}

// cleanPatterns trims each pattern, drops the empty ones, and applies an
// optional per-pattern transform. Returns nil for an all-empty list so the
// "empty means any" reading is preserved.
func cleanPatterns(patterns []string, transform func(string) string) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if transform != nil {
			p = transform(p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeRouteAction maps a recognized action onto the value it is stored as,
// an empty one onto the "allow" it has always meant, and anything else onto
// itself.
//
// Passing an unrecognized action through is deliberate: validation runs on the
// normalized rules, so folding "audit" into "allow" here would store it as a
// grant rather than refusing the request that asked for it.
func normalizeRouteAction(action string) string {
	trimmed := strings.TrimSpace(action)
	switch {
	case trimmed == "":
		return persona.ActionAllow
	case strings.EqualFold(trimmed, persona.ActionDeny):
		return persona.ActionDeny
	case strings.EqualFold(trimmed, persona.ActionAllow):
		return persona.ActionAllow
	default:
		return trimmed
	}
}

// testPersonaRouteAccess answers the route case of
// POST /api/v1/admin/personas/{name}/test-access: may this persona invoke this
// method on this path of this connection, and which rule decided it.
//
// The path is evaluated in both the forms an enforcement point holds it in.
// The operator asks with the path as the catalog declares it, which is what the
// persona editor sends and what a listing surface filters on; the query carries
// it as the template as well, so the answer is the one an invoke of the calls
// that operation serves would get.
func testPersonaRouteAccess(w http.ResponseWriter, p *persona.Persona, req testPersonaAccessRequest) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := strings.TrimSpace(req.Path)
	if method == "" || path == "" {
		writeError(w, http.StatusBadRequest, "method and path are required when connection is set")
		return
	}
	if !strings.HasPrefix(path, "/") {
		writeError(w, http.StatusBadRequest, "path must start with /")
		return
	}
	decision := persona.NewToolFilter(nil).WhyAPIRouteAllowed(p, persona.RouteQuery{
		Connection: strings.TrimSpace(req.Connection),
		Method:     method,
		Path:       path,
		Template:   path,
	})
	writeJSON(w, http.StatusOK, testPersonaAccessResponse{
		Allowed:     decision.Allowed,
		Source:      decision.Source,
		MatchedRule: decision.MatchedRule,
	})
}
