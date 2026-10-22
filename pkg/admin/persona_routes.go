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
