package sessionapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// getEvent handles GET /api/v1/portal/events/{id}.
//
// @Summary      Get one of my own calls
// @Description  Returns one audit event of the calling user's own, by event id. It is the drill-down behind a row of the session timeline: the parameters the call carried, the reason stated for it, and the error text when it failed, none of which the timeline entry itself holds. The read is scoped to the caller the same way the session read is — an event belonging to someone else is answered as not-found rather than as a refusal, which is the same answer an id that was never issued gets. It is not the call catalog: GET /portal/calls/{id} answers only for the sql, api and graphql kinds, so most of a session's rows have no record there.
// @Tags         Sessions
// @Produce      json
// @Param        id  path  string  true  "Audit event ID"
// @Success      200  {object}  audit.Event
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/events/{id} [get]
func (h *handler) getEvent(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	// UserID is assigned here, after the id is taken from the path, so the
	// scope is not something a caller can spell. An event of somebody else's
	// simply does not match, which is why the miss below is a 404 and never a
	// 403: telling one caller that another caller's event exists is the
	// disclosure this route is scoped to prevent.
	filter := audit.QueryFilter{
		ID:     r.PathValue("id"),
		UserID: user.UserID,
		Limit:  1,
	}
	events, err := h.cfg.Events.Query(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the call")
		return
	}
	if len(events) == 0 {
		httpjson.WriteError(w, http.StatusNotFound, "no such call")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, events[0])
}
