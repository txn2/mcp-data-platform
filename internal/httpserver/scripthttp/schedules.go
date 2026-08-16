package scripthttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxScheduleBodyBytes bounds a set-schedule request. A cadence, a zone, and a
// binding per declared parameter fit inside it many times over.
const maxScheduleBodyBytes = 64 << 10

// scheduleListResponse is the schedule listing payload.
type scheduleListResponse struct {
	Data  []script.Schedule `json:"data"`
	Total int               `json:"total" example:"2"`
}

// listSchedules returns every schedule, for the operator view of what the
// platform is running unattended.
//
// @Summary      List script schedules
// @Description  Returns every managed-script schedule with its cadence, its bound parameters, its next fire, and how many fires it has missed.
// @Tags         Scripts
// @Produce      json
// @Success      200  {object}  scheduleListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/schedules [get]
func (h *Handler) listSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := h.deps.Schedules.ListSchedules(r.Context(), script.ScheduleFilter{})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errListSchedules)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, scheduleListResponse{Data: schedules, Total: len(schedules)})
}

// getSchedule returns one script's schedule.
//
// @Summary      Get a script's schedule
// @Description  Returns the cadence a script runs on, the parameters every fire binds, when it fires next, and how many fires it has missed.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  script.Schedule
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/schedule [get]
func (h *Handler) getSchedule(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.loadScript(w, r)
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

// scheduleRequest is the cadence an operator sets.
//
// It carries no roles, connections, or capabilities, and could not usefully:
// what a scheduled run executes and what it may reach were bound when the
// version was approved. A schedule adds cadence to that grant and nothing else.
type scheduleRequest struct {
	// Cron is a standard five-field expression or a descriptor (@daily).
	Cron string `json:"cron" example:"0 7 * * 1-5"`
	// Timezone is the IANA zone the expression is read in; empty means UTC.
	Timezone string `json:"timezone" example:"America/Los_Angeles"`
	// Params are the values every fire binds, with ${fire_date} left as
	// written: it expands at the fire, so the run records the date it computed.
	Params map[string]any `json:"params,omitempty"`
	// Enabled turns the schedule on or off; omitted leaves an existing
	// schedule's state alone and starts a new one enabled.
	Enabled *bool `json:"enabled,omitempty"`
}

// setSchedule creates or replaces a script's schedule.
//
// @Summary      Set a script's schedule
// @Description  Creates or replaces the cadence a script runs on. The parameters are validated against the approved version's contract, or the live record's when nothing is approved yet. A schedule grants no authority: the version that runs and the capabilities it holds were both bound at approval.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id        path  string           true  "Script ID"
// @Param        schedule  body  scheduleRequest  true  "Cadence"
// @Success      200  {object}  script.Schedule
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/schedule [put]
func (h *Handler) setSchedule(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.loadScript(w, r)
	if !ok {
		return
	}
	h.writeSchedule(w, r, sc, h.deps.AdminEmail(r))
}

// writeSchedule applies a set request to sc, recording actor as the change's
// author. It is the whole of the operation, and both surfaces call it: the
// admin route resolves its script by id alone, the portal route resolves it
// through ownership, and from there setting a cadence is one behavior rather
// than two implementations to keep in step.
func (h *Handler) writeSchedule(w http.ResponseWriter, r *http.Request, sc *script.Script, actor string) {
	var req scheduleRequest
	// The bindings are scalars against a declared parameter contract, so the
	// bound is generous for anything a schedule legitimately carries. It is
	// applied here rather than at the caller because the portal route (#1307)
	// is reachable by every authenticated user, not only by administrators,
	// and the decoder would otherwise materialize whatever it was sent.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScheduleBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	approved, ok := h.approvedVersion(w, r, sc)
	if !ok {
		return
	}
	prev, ok := h.currentSchedule(w, r, sc.ID)
	if !ok {
		return
	}
	sched, err := script.BuildSchedule(sc, approved, prev, script.ScheduleRequest{
		CronSpec: req.Cron, Timezone: req.Timezone, Params: req.Params,
		Enabled: req.Enabled, Actor: actor,
	}, time.Now())
	if err != nil {
		// Every failure here is the request's: an unparseable expression, an
		// unknown zone, a binding the contract refuses. Each names what to fix.
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.deps.Schedules.SetSchedule(r.Context(), sched); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to set the schedule")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, sched)
}

// enableSchedule resumes a paused schedule.
//
// @Summary      Enable a script's schedule
// @Description  Resumes a paused schedule without touching its cadence. The next fire is the one it was parked on, which the misfire policy then collapses to a single run — the same treatment downtime gets.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  script.Schedule
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/schedule/enable [post]
func (h *Handler) enableSchedule(w http.ResponseWriter, r *http.Request) {
	h.setScheduleEnabled(w, r, true)
}

// disableSchedule pauses a schedule.
//
// @Summary      Disable a script's schedule
// @Description  Stops a schedule firing, keeping the row that explains the runs it produced. There is deliberately no way to delete a schedule.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  script.Schedule
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/schedule/disable [post]
func (h *Handler) disableSchedule(w http.ResponseWriter, r *http.Request) {
	h.setScheduleEnabled(w, r, false)
}

// setScheduleEnabled applies the pause state and answers with the schedule.
func (h *Handler) setScheduleEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	sc, ok := h.loadScript(w, r)
	if !ok {
		return
	}
	h.writeScheduleEnabled(w, r, sc, h.deps.AdminEmail(r), enabled)
}

// writeScheduleEnabled pauses or resumes sc's schedule, recording actor. Like
// writeSchedule it is surface-independent: only how the script was resolved
// differs between the admin and the portal route.
func (h *Handler) writeScheduleEnabled(w http.ResponseWriter, r *http.Request, sc *script.Script, actor string, enabled bool) {
	err := h.deps.Schedules.SetScheduleEnabled(r.Context(), sc.ID, enabled, actor)
	if errors.Is(err, script.ErrScheduleNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, errScheduleNot)
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to change the schedule")
		return
	}
	sched, ok := h.currentSchedule(w, r, sc.ID)
	if !ok {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, sched)
}

// approvedVersion loads the version a schedule's fires would execute, or nil
// when the script has none.
func (h *Handler) approvedVersion(w http.ResponseWriter, r *http.Request, sc *script.Script) (*script.Version, bool) {
	if !sc.Executable() {
		return nil, true
	}
	v, err := h.deps.Versions.GetVersionByID(r.Context(), sc.ApprovedVersionID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the approved version")
		return nil, false
	}
	return v, true
}

// currentSchedule loads the schedule being replaced, treating its absence as
// the normal first-time case.
func (h *Handler) currentSchedule(w http.ResponseWriter, r *http.Request, scriptID string) (*script.Schedule, bool) {
	sched, err := h.deps.Schedules.GetSchedule(r.Context(), scriptID)
	if errors.Is(err, script.ErrScheduleNotFound) {
		return nil, true
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errListSchedules)
		return nil, false
	}
	return sched, true
}
