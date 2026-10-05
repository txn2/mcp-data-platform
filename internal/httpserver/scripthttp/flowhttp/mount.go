package flowhttp

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// ForPortal builds the portal routes over the script routes' own lookups, so
// a version and a run are found, and not found, the way every other script
// route finds them: the graph under the source's read rule (#1866), a run
// under the run's (the script's owner, an administrator, or whoever requested
// it). calls is where a run's calls are read from; nil draws a run with none.
// tiles serves a script's tile; nil leaves that route unmounted.
func ForPortal(scripts *scripthttp.Handler, deps scripthttp.Deps, calls AuditQuerier, tiles TileReader) *Handler {
	d := Deps{
		Load:     scripts.LoadScriptVersion,
		SignedIn: func(r *http.Request) bool { return deps.PortalUser != nil && deps.PortalUser(r) != nil },
		Version:  deps.Versions.GetVersion,
		Run: func(w http.ResponseWriter, r *http.Request) (*script.Run, bool) {
			return scripts.ReadableRun(w, r, deps.PortalUser(r))
		},
		ActsOn: func(r *http.Request, scriptID string) bool {
			return deps.PortalUser != nil && scripts.ActsOnScript(r, deps.PortalUser(r), scriptID)
		},
	}
	if deps.Runs == nil {
		d.Run = nil
	}
	d.Audit = calls
	d.Tiles = tiles
	return New(d)
}

// ForAdmin builds the admin routes over the script routes' version lookup.
func ForAdmin(scripts *scripthttp.Handler, deps scripthttp.Deps) *Handler {
	return New(Deps{Load: scripts.LoadScriptVersion, Version: deps.Versions.GetVersion})
}
