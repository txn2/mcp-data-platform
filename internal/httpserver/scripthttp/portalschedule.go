package scripthttp

import (
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The portal schedule routes are the first mutations on this surface, and they
// are deliberately the only ones (#1307).
//
// A cadence carries no authority. It is a row of expression, timezone, and
// bound parameters; the run gate and the persona filter are re-read at every
// fire, so re-timing a script cannot make it reach anything it could not
// already reach. Nothing here can widen what a run reaches, and nothing can
// change what a script does.
//
// Who may set one is therefore the script's owner and an administrator — the
// same rule the tool applies (internal/platform/scriptlayer, schedulable), and
// the same rule reading it answers to.
//
// The routes run under the portal's own authentication and CSRF handling,
// because they are mounted inside the same wrapper the read routes are, and
// they answer "not yours" exactly as they answer "no such script" — the
// difference would tell a caller that a script they may not read exists.

// registerPortalSchedules mounts the owner's cadence controls. It is called
// from RegisterPortal only where the deployment can keep a schedule.
func (h *Handler) registerPortalSchedules(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/schedule", wrap(h.portalHandler(h.portalGetSchedule)))
	mux.Handle("PUT /api/v1/portal/scripts/{id}/schedule", wrap(h.portalHandler(h.portalSetSchedule)))
	// Pausing is its own action rather than a field of the cadence, for the
	// reason the admin surface states: sending the whole schedule back to turn
	// it off would re-base its next fire, and a paused schedule must resume on
	// the fire it was parked on.
	mux.Handle("POST /api/v1/portal/scripts/{id}/schedule/enable", wrap(h.portalHandler(h.portalEnableSchedule)))
	mux.Handle("POST /api/v1/portal/scripts/{id}/schedule/disable", wrap(h.portalHandler(h.portalDisableSchedule)))
}

// portalGetSchedule returns an owned script's schedule in full.
//
// The listing and the contract both report a cadence already, but neither
// carries the parameter bindings every fire passes: the contract is the
// document a reference resolves to, and adding an owner's bindings to it would
// widen every surface that renders one. This route is what the owner's editor
// reads, so it is owner-and-admin like the other editing reads here.
//
// @Summary      Get a script's schedule
// @Description  Returns the cadence a script the caller owns runs on, the parameters every fire binds, when it fires next, and how many fires it has missed. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  script.Schedule
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/schedule [get]
func (h *Handler) portalGetSchedule(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	sched, err := h.deps.Schedules.GetSchedule(r.Context(), sc.ID)
	if errors.Is(err, script.ErrScheduleNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, errScheduleNot)
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errListSchedules)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, sched)
}

// portalSetSchedule creates or replaces an owned script's cadence.
//
// @Summary      Set a script's schedule
// @Description  Creates or replaces the cadence a script the caller owns runs on, with the parameters every fire binds. A schedule grants no authority: every fire executes the latest saved version, authorized against the roles captured at that save. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id        path  string           true  "Script ID"
// @Param        schedule  body  scheduleRequest  true  "Cadence"
// @Success      200  {object}  script.Schedule
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/schedule [put]
func (h *Handler) portalSetSchedule(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	h.writeSchedule(w, r, sc, user.owner())
}

// portalEnableSchedule resumes an owned script's paused schedule.
//
// @Summary      Enable a script's schedule
// @Description  Resumes a paused schedule on a script the caller owns, without touching its cadence. The next fire is the one it was parked on. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  script.Schedule
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/schedule/enable [post]
func (h *Handler) portalEnableSchedule(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	h.portalScheduleEnabled(w, r, user, true)
}

// portalDisableSchedule pauses an owned script's schedule.
//
// @Summary      Disable a script's schedule
// @Description  Stops a schedule on a script the caller owns from firing, keeping the row that explains the runs it produced. There is deliberately no way to delete a schedule. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  script.Schedule
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/schedule/disable [post]
func (h *Handler) portalDisableSchedule(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	h.portalScheduleEnabled(w, r, user, false)
}

// portalScheduleEnabled resolves the owned script and applies the pause state.
func (h *Handler) portalScheduleEnabled(w http.ResponseWriter, r *http.Request, user *PortalIdentity, enabled bool) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	h.writeScheduleEnabled(w, r, sc, user.owner(), enabled)
}
