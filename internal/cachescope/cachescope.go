// Package cachescope decides the cacheScope every cacheable MCP result carries
// under protocol revision 2026-07-28 (#2794).
package cachescope

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The cacheScope values of protocol revision 2026-07-28 (CacheableResult).
const (
	// Private limits reuse of a result to the authorization
	// context that received it.
	Private = "private"
	// Public lets any client or intermediary serve a result across
	// authorization contexts: it holds nothing that depends on the caller.
	Public = "public"
)

// The methods whose results carry ttlMs and cacheScope under 2026-07-28.
const (
	methodToolsList              = "tools/list"
	methodPromptsList            = "prompts/list"
	methodListResources          = "resources/list"
	methodReadResource           = "resources/read"
	methodResourcesTemplatesList = "resources/templates/list"
	methodServerDiscover         = "server/discover"
)

// cacheScopes is the cache scope of every result the protocol makes
// cacheable. go-sdk normalizes an unset scope to "public", which lets
// a shared cache hand one caller's filtered list to another; every method is
// therefore decided here, and TestEveryCacheableMethodHasAScope fails on one
// that is not, or on a middleware that intercepts a public one.
//
//   - tools/list is filtered by persona (MCPToolVisibilityMiddleware).
//   - prompts/list adds the caller's personal and persona prompts.
//   - resources/list adds the caller's user and persona resources.
//   - resources/read answers a managed resource only within its scope, and
//     the semantic templates answer through the caller's persona.
//   - resources/templates/list and server/discover are the same for every
//     caller: the templates are fixed, and discovery describes the server.
var cacheScopes = map[string]string{
	methodToolsList:              Private,
	methodPromptsList:            Private,
	methodListResources:          Private,
	methodReadResource:           Private,
	methodResourcesTemplatesList: Public,
	methodServerDiscover:         Public,
}

// Of is the cache scope a method's result carries, "" for a method whose
// result is not cacheable.
func Of(method string) string {
	return cacheScopes[method]
}

// Stamp sets the cache scope a method's result carries. pkg/middleware's
// outermost result layer calls it, so a result a middleware built itself (a
// managed resource read) is decided by the same table as one the SDK's
// handler built. ttlMs is left as it is: no result here has a freshness bound.
func Stamp(method string, result mcp.Result) {
	scope := cacheScopes[method]
	if scope == "" {
		return
	}
	switch r := result.(type) {
	case *mcp.ListToolsResult:
		r.CacheScope = scope
	case *mcp.ListPromptsResult:
		r.CacheScope = scope
	case *mcp.ListResourcesResult:
		r.CacheScope = scope
	case *mcp.ReadResourceResult:
		r.CacheScope = scope
	case *mcp.ListResourceTemplatesResult:
		r.CacheScope = scope
	case *mcp.DiscoverResult:
		r.CacheScope = scope
	}
}
