package apphttp

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
)

// diagram answers how an application's parts fit together.
//
// @Summary      Draw an application
// @Description  How the application's parts fit together at one version (#2540): its parts and what they reach outside it as nodes (a part, a table in its schema or on another store, a channel, a library, an output), and the links between them (a webhook source starting a script, a script reading or writing a table, loading a library at a version, exporting or refreshing an asset, notifying a channel or submitting a form to its source, an asset calling an endpoint or embedding a form), each read from the parts' sources and stored settings, never from a run: drawing one starts no run and makes no outbound request. A name a source computes when it runs is a node marked computed and is never matched to a part. findings names a script loading one of the application's libraries at a version other than the one the version drawn pins (which a release refuses), a table of the application's schema its scripts read and no part writes, and a script using a schema the application does not hold. version is current (the current release's pins), latest (each part's newest version) or a release's number; compare marks each node and link added, removed or unchanged against that version. An asset the caller cannot open is drawn unnamed and its content is not read.
// @Param        app      path   string  true   "Application name or id"
// @Param        version  query  string  false  "current (default), latest, or a release's number"
// @Param        compare  query  string  false  "current, latest, or a release's number to compare with"
// @Success      200  {object}  appdiagram.Diagram
// @Tags         Applications
// @Produce      json
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/applications/{app}/diagram [get]
func (h *handler) diagram(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var d *appdiagram.Diagram
	d, err := h.cfg.Service.Diagram(r.Context(), actor, appsvc.DiagramRequest{
		App: r.PathValue(pathApp), Version: q.Get("version"), Compare: q.Get("compare"), View: h.cfg.ViewAsset,
	})
	h.answer(w, d, err)
}
