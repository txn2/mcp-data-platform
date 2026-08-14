// Package scripthttp exposes managed-script review over the admin REST API:
// the script list, a script's version history, one version with the
// capabilities its code reaches for, and the approval action that makes a
// version executable.
//
// Approval is the load-bearing control of the whole feature — nothing runs
// unattended except a version somebody approved, and approving binds the
// capability grant that run is confined to — so it is a first-class REST action
// from the moment execution exists rather than something a later UI introduces.
// The human review surface is built on these routes.
//
// It lives beside the other version-review seam (internal/httpserver/
// versionhttp) rather than inside pkg/admin so that package stays within its
// size budget; the composition root mounts it under the admin path prefix
// wrapped in the admin authentication middleware and injects the identity
// accessor, so this package never imports the admin surface.
package scripthttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Deps carries the collaborators the review handlers need. Every store except
// Schedules is required; a deployment without them does not mount these routes.
type Deps struct {
	Scripts    script.Store
	Versions   script.VersionStore
	Approvals  script.ApprovalStore
	Reviews    script.ReviewStore
	Rejections script.RejectionStore
	// Schedules is the cadence store. Nil leaves the schedule routes unmounted,
	// which is the honest shape for a deployment that cannot keep a schedule.
	Schedules script.ScheduleStore

	// AdminEmail returns the authenticated administrator's email, which is
	// stamped on the approval.
	AdminEmail func(r *http.Request) string
}

// Handler serves the script review routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Shared literals.
const (
	pathID           = "id"
	pathVersion      = "version"
	errScriptNot     = "script not found"
	errVersionNot    = "version not found"
	errListVersions  = "failed to list versions"
	errScheduleNot   = "this script has no schedule"
	errListSchedules = "failed to read schedules"
)

// RegisterAdmin mounts the review routes under prefix, each wrapped in the
// admin authentication middleware.
func (h *Handler) RegisterAdmin(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/scripts", wrap(http.HandlerFunc(h.listScripts)))
	// A literal segment outranks the {id} wildcard in the same position, so the
	// queue route cannot be shadowed by a script whose id is "reviews".
	mux.Handle("GET "+prefix+"/scripts/reviews", wrap(http.HandlerFunc(h.listPendingReviews)))
	mux.Handle("GET "+prefix+"/scripts/{id}/versions", wrap(http.HandlerFunc(h.listVersions)))
	mux.Handle("GET "+prefix+"/scripts/{id}/versions/{version}", wrap(http.HandlerFunc(h.getVersion)))
	mux.Handle("POST "+prefix+"/scripts/{id}/versions/{version}/approve", wrap(http.HandlerFunc(h.approveVersion)))
	mux.Handle("POST "+prefix+"/scripts/{id}/versions/{version}/reject", wrap(http.HandlerFunc(h.rejectVersion)))
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

// listScripts returns every script, for the review queue.
//
// @Summary      List managed scripts
// @Description  Returns every managed script with its lifecycle status and the id of the version the platform is allowed to execute (empty when nothing is approved).
// @Tags         Scripts
// @Produce      json
// @Success      200  {object}  scriptListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts [get]
func (h *Handler) listScripts(w http.ResponseWriter, r *http.Request) {
	// An empty filter carries no VisibleTo predicate, which is the admin view:
	// review covers every script, including the personal ones whose owners are
	// the only other people who can see them.
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
// @Description  Returns every version of a script with its source, its author and the roles that author held, and the approval stamp and capability grant bound to it.
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

// versionReviewResponse is one version with everything a reviewer needs to
// decide on it: the snapshot, what its code reaches for, and how that compares
// with the grant it already carries.
type versionReviewResponse struct {
	Version script.Version `json:"version"`
	// Referenced is what a static read of the source says the script calls and
	// which connections it names.
	Referenced referenced `json:"referenced"`
	// MissingCapabilities and MissingConnections are what the code reaches for
	// that the current grant does not cover. On an unapproved version that is
	// everything, which is the point: it is the grant a reviewer would have to
	// bind for this code to work.
	MissingCapabilities []string `json:"missing_capabilities,omitempty"`
	MissingConnections  []string `json:"missing_connections,omitempty"`
	// Findings are the validator's complaints about the source.
	Findings []scriptrun.Finding `json:"findings,omitempty"`
	// Approved is the version the script executes today, with the grant it
	// holds and the diff from its source to this one. It is absent when the
	// script has never had a version approved, which is what makes this a first
	// approval rather than a change to something already running.
	Approved *approvedBaseline `json:"approved,omitempty"`
}

// referenced is the static read of a version's source.
type referenced struct {
	Capabilities []string `json:"capabilities"`
	Connections  []string `json:"connections"`
	// DynamicConnections is true when at least one call computes its connection
	// instead of naming one, which makes the connection list incomplete — and
	// means the grant cannot be checked against the code by reading it.
	DynamicConnections bool `json:"dynamic_connections"`
}

// getVersion returns one version with its capability diff.
//
// @Summary      Get a script version for review
// @Description  Returns one version's snapshot together with the capabilities and connections its source reaches for, and which of those the version's current grant does not cover.
// @Tags         Scripts
// @Produce      json
// @Param        id       path  string  true  "Script ID"
// @Param        version  path  int     true  "Version number"
// @Success      200  {object}  versionReviewResponse
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
	missingCapabilities, missingConnections := v.Grants.MissingFor(report.Capabilities, report.Connections)
	httpjson.WriteJSON(w, http.StatusOK, versionReviewResponse{
		Version:  *v,
		Approved: h.baselineFor(r, sc, v),
		Referenced: referenced{
			Capabilities:       report.Capabilities,
			Connections:        report.Connections,
			DynamicConnections: report.DynamicConnections,
		},
		MissingCapabilities: missingCapabilities,
		MissingConnections:  missingConnections,
		Findings:            report.Findings,
	})
}

// approveRequest is the grant a reviewer binds to a version.
//
// Roles are deliberately absent. The approval copies the roles the version's
// author held, so a reviewer decides what a script may REACH and never what
// authority it holds; a request that could name roles would turn approval into
// a way to hand a script more access than the person who wrote it.
type approveRequest struct {
	Connections  []string `json:"connections"`
	Capabilities []string `json:"capabilities"`
	Destinations []string `json:"destinations"`
}

// approveVersion approves a version and binds its capability grant.
//
// @Summary      Approve a script version
// @Description  Stamps the version approved, binds the capability grant it executes under, and points the script's execution gate at it. The granted roles are the roles the version's author held and cannot be set by the request. Approving a pending draft also applies its snapshot to the live script.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id       path  string          true  "Script ID"
// @Param        version  path  int             true  "Version number"
// @Param        grant    body  approveRequest  true  "Capability grant"
// @Success      200  {object}  script.Version
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/versions/{version}/approve [post]
func (h *Handler) approveVersion(w http.ResponseWriter, r *http.Request) {
	v, ok := h.loadVersion(w, r)
	if !ok {
		return
	}
	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	grants := script.Grants{
		Connections:  req.Connections,
		Capabilities: req.Capabilities,
		Destinations: req.Destinations,
	}
	if detail := refuseUnreachableGrant(v, grants); detail != "" {
		httpjson.WriteError(w, http.StatusBadRequest, detail)
		return
	}
	approved, err := h.deps.Approvals.ApproveVersion(r.Context(), v.ScriptID, v.Version, h.deps.AdminEmail(r), grants)
	if err != nil {
		writeDecisionError(w, err, "failed to approve version")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, approved)
}

// refuseUnreachableGrant rejects an approval whose grant does not cover what
// the code plainly reaches for, naming what is missing.
//
// Approving a script that will refuse itself on its first query is not a
// governance decision anybody meant to make, and the reviewer is the last
// person who can catch it cheaply. The check reads the source statically, so a
// script that computes its connection name cannot be checked this way; that
// case is reported to the reviewer by the review endpoint rather than guessed
// at here.
func refuseUnreachableGrant(v *script.Version, grants script.Grants) string {
	report := scriptrun.Validate(v.Source)
	missingCapabilities, missingConnections := grants.MissingFor(report.Capabilities, report.Connections)
	switch {
	case len(missingCapabilities) > 0:
		return "the grant does not cover capabilities this version calls: " + join(missingCapabilities)
	case len(missingConnections) > 0:
		return "the grant does not cover connections this version queries: " + join(missingConnections)
	}
	return ""
}

// join renders a list for an error detail.
func join(values []string) string { return strings.Join(values, ", ") }

// writeDecisionError maps a review decision's failure to a status, naming the
// decision that failed in the fallback message.
//
// Both caller-correctable outcomes are matched by SENTINEL: a conflict means
// the version moved under the reviewer, and an invalid grant means the
// capability set cannot be bound. Anything else is the platform's own failure,
// and its detail — possibly driver text — must not reach the client, so it is
// answered with a fixed message. Classifying by anything other than a sentinel
// (an error's shape, its message) would eventually route a wrapped store error
// into the branch that echoes it back.
func writeDecisionError(w http.ResponseWriter, err error, public string) {
	switch {
	case errors.Is(err, script.ErrVersionConflict):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, script.ErrInvalidGrant), errors.Is(err, script.ErrNoGrants):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, public)
	}
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

// loadVersion resolves the version named by the path.
func (h *Handler) loadVersion(w http.ResponseWriter, r *http.Request) (*script.Version, bool) {
	_, v, ok := h.loadScriptVersion(w, r)
	return v, ok
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
