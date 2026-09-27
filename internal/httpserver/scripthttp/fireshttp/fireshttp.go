// Package fireshttp serves when script schedules fire, for the Schedules tab
// of the scripts page (#1891). It lays the fires out on the three time axes
// the page draws them on: a day for the schedules that fire more than once a
// day, a week for those that fire at least once a week, and three months for
// the rest.
//
// Every fire comes from script.Cron, the parse the materializer advances a
// schedule with. The browser draws what this returns and computes no fire of
// its own, so the page and the scheduler cannot disagree about a DST
// transition, an @every descriptor or a day-of-month-or-day-of-week
// expression.
//
// It is mounted by scripthttp, which resolves the caller. Visibility is the
// run listing's: a caller sees the schedules of the scripts they own and an
// administrator sees every schedule.
package fireshttp

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/scriptlist"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Route is where the layout is served. A literal segment outranks the {id}
// wildcard, so a script whose id is "fires" cannot shadow it.
const Route = "GET /api/v1/portal/scripts/fires"

// ScriptLister reads the scripts a caller may see.
type ScriptLister interface {
	List(ctx context.Context, filter script.ListFilter) ([]script.Script, error)
}

// ScheduleLister reads the schedules of a set of scripts.
type ScheduleLister interface {
	ListSchedules(ctx context.Context, filter script.ScheduleFilter) ([]script.Schedule, error)
}

// Deps carries the two stores and the caller.
type Deps struct {
	Scripts   ScriptLister
	Schedules ScheduleLister
	// Caller is who is asking, as the owner address a script records, or
	// ok=false after writing the refusal.
	Caller func(w http.ResponseWriter, r *http.Request) (owner string, isAdmin, ok bool)
}

// Handler serves the layout.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the route, wrapped in the portal authentication middleware.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle(Route, wrap(http.HandlerFunc(h.list)))
}

// list lays out when the caller's scheduled scripts fire.
//
// @Summary      Lay out the fire times of the caller's script schedules
// @Description  Returns every schedule the caller may see, each placed on one of three axes by how often it fires: intraday (the viewer's day, for schedules firing more than once a day), multi_day (the viewer's Monday-to-Monday week, for at least once a week) and long_term (three calendar months from the first of the viewer's month). Each row lists its fires in the window, expanded in the schedule's own timezone with the same parse the scheduler uses, capped at 500 per row; fire_count is every fire in the window and truncated says when fires holds fewer. rhythm classifies the typical gap between fires. A paused schedule is listed with enabled false. A schedule that cannot be parsed is named under unreadable. Owners see their own schedules; administrators see every schedule.
// @Tags         Scripts
// @Produce      json
// @Param        tz  query  string  false  "IANA zone the windows are cut in, the viewer's own; empty means UTC"
// @Success      200  {object}  fireshttp.Timeline
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/fires [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	owner, isAdmin, ok := h.deps.Caller(w, r)
	if !ok {
		return
	}
	viewer, ok := viewerZone(w, r)
	if !ok {
		return
	}
	scripts, err := h.deps.Scripts.List(r.Context(), scriptlist.Filter(owner, isAdmin, nil))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list scripts")
		return
	}
	ids := make([]string, 0, len(scripts))
	names := make(map[string]string, len(scripts))
	for i := range scripts {
		ids = append(ids, scripts[i].ID)
		names[scripts[i].ID] = displayName(&scripts[i])
	}
	schedules := []script.Schedule{}
	// An empty id set matches nothing in the store anyway; skipping the read
	// says the same thing without the round trip.
	if len(ids) > 0 {
		schedules, err = h.deps.Schedules.ListSchedules(r.Context(), script.ScheduleFilter{ScriptIDs: ids, Limit: len(ids)})
		if err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, "failed to read schedules")
			return
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, Build(schedules, names, viewer, time.Now()))
}

// displayName is what a person calls a script.
func displayName(sc *script.Script) string {
	if sc.DisplayName != "" {
		return sc.DisplayName
	}
	return sc.Name
}

// viewerZone reads the zone the caller asked the windows be cut in, answering
// 400 for one the runtime does not know rather than silently drawing the day
// in UTC, which would put every mark hours from where the viewer expects it.
func viewerZone(w http.ResponseWriter, r *http.Request) (*time.Location, bool) {
	name := strings.TrimSpace(r.URL.Query().Get("tz"))
	if name == "" {
		return time.UTC, true
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "tz is not a known timezone; use an IANA name such as America/Los_Angeles or UTC")
		return nil, false
	}
	return loc, true
}
