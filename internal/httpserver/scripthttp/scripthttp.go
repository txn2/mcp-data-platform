// Package scripthttp exposes managed scripts over the admin REST API — the
// script list, a script's version history, one version with what its code
// reaches for, schedules, and the run history — and the portal's own script
// routes.
//
// It lives beside the other version seam (internal/httpserver/versionhttp)
// rather than inside pkg/admin so that package stays within its size budget;
// the composition root mounts it under the admin path prefix wrapped in the
// admin authentication middleware and injects the identity accessor, so this
// package never imports the admin surface.
package scripthttp

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Deps carries the collaborators the handlers need. Every store except
// Schedules is required; a deployment without them does not mount these routes.
type Deps struct {
	Scripts  script.Store
	Versions script.VersionStore
	// Schedules is the cadence store. Nil leaves the schedule routes unmounted,
	// which is the honest shape for a deployment that cannot keep a schedule.
	Schedules script.ScheduleStore

	// Runs is the run history. Nil leaves the portal run routes unmounted: a
	// deployment that keeps no runs has no history to show.
	Runs script.RunStore
	// Contracts composes one script's contract document for the portal detail
	// route. Nil leaves that route unmounted.
	Contracts ContractReader
	// LatestRuns reports each script's most recent run for the portal listing.
	// Nil leaves the listing's last-run column empty rather than unmounting it.
	LatestRuns LatestRunReader

	// DryRuns records and resolves the accounts of draft executions (#1364).
	// Nil runs drafts without keeping an account of them, which leaves every
	// version reading as one nobody dry-ran — the state before the account
	// existed — rather than failing the run.
	DryRuns script.DryRunStore

	// Drafts executes a draft under the calling person's own identity. Nil
	// leaves the dry-run route unmounted; validate needs nothing but the
	// source and stays either way.
	Drafts DraftRunner

	// Connections enumerates the connections the portal caller's own persona
	// reaches, which a connection-typed parameter is chosen from (#1361). Nil
	// leaves the choices route unmounted.
	Connections ConnectionEnumerator

	// Audit records administrative script writes — today the owner transfer
	// (#1404). Nil leaves the transfer working and unrecorded, which is what a
	// deployment with no audit store has for every other write too.
	Audit AuditRecorder

	// AdminEmail returns the authenticated administrator's email, recorded as
	// the author of an admin-made edit.
	AdminEmail func(r *http.Request) string
	// PortalUser resolves the authenticated portal caller, or nil when the
	// request carries no user. Nil leaves the portal routes unmounted, which is
	// what the admin surface passes.
	PortalUser func(r *http.Request) *PortalIdentity
}

// AuditRecorder writes one audit event. It is the write half of audit.Logger:
// this surface records administrative writes and never reads the log back, and
// a narrower dependency is one less thing a deployment has to supply to mount
// these routes.
type AuditRecorder interface {
	Log(ctx context.Context, event audit.Event) error
}

// Handler serves the script routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Shared literals.
const (
	pathID           = "id"
	pathVersion      = "version"
	pathRunID        = "runID"
	errScriptNot     = "script not found"
	errVersionNot    = "version not found"
	errRunNot        = "run not found"
	errListVersions  = "failed to list versions"
	errScheduleNot   = "this script has no schedule"
	errListSchedules = "failed to read schedules"
)

// RegisterAdmin mounts the admin script routes under prefix, each wrapped in
// the admin authentication middleware.
func (h *Handler) RegisterAdmin(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/scripts", wrap(http.HandlerFunc(h.listScripts)))
	// A literal segment outranks the {id} wildcard, so a script whose id is
	// "runs" cannot shadow the operator's cross-script run listing (#1307).
	if h.deps.Runs != nil {
		mux.Handle("GET "+prefix+"/scripts/runs", wrap(http.HandlerFunc(h.listRuns)))
	}
	mux.Handle("GET "+prefix+"/scripts/{id}/versions", wrap(http.HandlerFunc(h.listVersions)))
	mux.Handle("GET "+prefix+"/scripts/{id}/versions/{version}", wrap(http.HandlerFunc(h.getVersion)))
	if h.deps.Schedules == nil {
		return
	}
	mux.Handle("GET "+prefix+"/scripts/schedules", wrap(http.HandlerFunc(h.listSchedules)))
	mux.Handle("GET "+prefix+"/scripts/{id}/schedule", wrap(http.HandlerFunc(h.getSchedule)))
	mux.Handle("PUT "+prefix+"/scripts/{id}/schedule", wrap(http.HandlerFunc(h.setSchedule)))
	// Pausing is its own action rather than a field of the cadence. Sending the
	// whole schedule back to turn it off would re-base its next fire, which is
	// exactly what pausing must not do: a paused schedule resumes on the fire it
	// was parked on.
	mux.Handle("POST "+prefix+"/scripts/{id}/schedule/enable", wrap(http.HandlerFunc(h.enableSchedule)))
	mux.Handle("POST "+prefix+"/scripts/{id}/schedule/disable", wrap(http.HandlerFunc(h.disableSchedule)))
}

// scriptListResponse is the script listing payload.
type scriptListResponse struct {
	Data  []script.Script `json:"data"`
	Total int             `json:"total" example:"3"`
}

// listScripts returns every script, for the admin listing.
//
// @Summary      List managed scripts
// @Description  Returns every managed script with its lifecycle status and current version.
// @Tags         Scripts
// @Produce      json
// @Success      200  {object}  scriptListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts [get]
func (h *Handler) listScripts(w http.ResponseWriter, r *http.Request) {
	// An empty filter carries no VisibleTo predicate, which is the admin view:
	// it covers every script, including the personal ones whose owners are the
	// only other people who can see them.
	scripts, err := h.deps.Scripts.List(r.Context(), script.ListFilter{})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list scripts")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, scriptListResponse{Data: scripts, Total: len(scripts)})
}

// versionListResponse is the version history payload.
type versionListResponse struct {
	Data  []script.Version `json:"data"`
	Total int              `json:"total" example:"4"`
}

// listVersions returns a script's full version history, newest first.
//
// @Summary      List script versions
// @Description  Returns every version of a script with its source, its author, and the roles that author held.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  versionListResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/versions [get]
func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.loadScript(w, r)
	if !ok {
		return
	}
	versions, err := h.deps.Versions.ListVersions(r.Context(), sc.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errListVersions)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, versionListResponse{Data: versions, Total: len(versions)})
}

// versionDetailResponse is one version with what a reader needs to understand
// it: the snapshot, what its code reaches for, and whether anyone has run it.
type versionDetailResponse struct {
	Version script.Version `json:"version"`
	// Referenced is what a static read of the source says the script calls,
	// which connections it names, and where it writes.
	Referenced referenced `json:"referenced"`
	// Findings are the validator's complaints about the source.
	Findings []scriptrun.Finding `json:"findings,omitempty"`
	// DryRun is the account of somebody having executed this exact source, and
	// what it did (#1364). It is absent when nobody has.
	DryRun *script.DryRun `json:"dry_run,omitempty"`
}

// referenced is the static read of a version's source.
type referenced struct {
	Capabilities []string `json:"capabilities"`
	Connections  []string `json:"connections"`
	// Destinations are the destination names the source writes to, with the
	// portal counted for any export that names none, because that is where such
	// an export lands.
	Destinations []string `json:"destinations"`
	// RefreshTargets are the output names platform.publish_data refreshes, so
	// a reader sees which asset's data region this script rewrites.
	RefreshTargets []string `json:"refresh_targets"`
	// DynamicConnections is true when at least one call computes its connection
	// instead of naming one, DynamicDestinations when one computes its
	// destination, and DynamicRefreshTargets when a publish_data call computes
	// the name it refreshes. Any of them makes that list incomplete.
	DynamicConnections    bool `json:"dynamic_connections"`
	DynamicDestinations   bool `json:"dynamic_destinations"`
	DynamicRefreshTargets bool `json:"dynamic_refresh_targets"`
}

// getVersion returns one version with what its source reaches.
//
// @Summary      Get a script version
// @Description  Returns one version's snapshot together with the capabilities, connections, and destinations its source reaches for.
// @Tags         Scripts
// @Produce      json
// @Param        id       path  string  true  "Script ID"
// @Param        version  path  int     true  "Version number"
// @Success      200  {object}  versionDetailResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/versions/{version} [get]
func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	sc, v, ok := h.loadScriptVersion(w, r)
	if !ok {
		return
	}
	report := scriptrun.Validate(v.Source)
	httpjson.WriteJSON(w, http.StatusOK, versionDetailResponse{
		Version: *v,
		Referenced: referenced{
			Capabilities:          report.Capabilities,
			Connections:           report.Connections,
			Destinations:          report.Destinations,
			RefreshTargets:        report.RefreshTargets,
			DynamicConnections:    report.DynamicConnections,
			DynamicDestinations:   report.DynamicDestinations,
			DynamicRefreshTargets: report.DynamicRefreshTargets,
		},
		Findings: report.Findings,
		DryRun:   h.dryRunFor(r, sc.ID, v.Source),
	})
}

// dryRunFor is the account of this exact source having been executed, or nil
// when there is none.
//
// A read that fails yields nil rather than failing the page: the account is a
// decoration, and "we could not check" and "nobody ran it" are close enough in
// consequence that blocking the reader over the difference would be the worse
// trade. The difference is recorded in the log.
func (h *Handler) dryRunFor(r *http.Request, scriptID, source string) *script.DryRun {
	if h.deps.DryRuns == nil {
		return nil
	}
	d, err := h.deps.DryRuns.LatestDryRun(r.Context(), scriptID, script.SourceDigest(source))
	if err != nil {
		slog.Error("failed to read a script dry-run account", "error", err)
		return nil
	}
	return d
}

// loadScript resolves the script named by the path, writing the error response
// when it cannot.
func (h *Handler) loadScript(w http.ResponseWriter, r *http.Request) (*script.Script, bool) {
	sc, err := h.deps.Scripts.GetByID(r.Context(), r.PathValue(pathID))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get script")
		return nil, false
	}
	if sc == nil {
		httpjson.WriteError(w, http.StatusNotFound, errScriptNot)
		return nil, false
	}
	return sc, true
}

// loadScriptVersion resolves both the script and the version named by the path.
// The version alone does not answer what the script is executing today, which
// is what a review is read against.
func (h *Handler) loadScriptVersion(w http.ResponseWriter, r *http.Request) (*script.Script, *script.Version, bool) {
	sc, ok := h.loadScript(w, r)
	if !ok {
		return nil, nil, false
	}
	n, err := strconv.Atoi(r.PathValue(pathVersion))
	if err != nil || n <= 0 {
		httpjson.WriteError(w, http.StatusNotFound, errVersionNot)
		return nil, nil, false
	}
	v, err := h.deps.Versions.GetVersion(r.Context(), sc.ID, n)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errListVersions)
		return nil, nil, false
	}
	if v == nil {
		httpjson.WriteError(w, http.StatusNotFound, errVersionNot)
		return nil, nil, false
	}
	return sc, v, true
}
