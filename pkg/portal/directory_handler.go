package portal

import (
	"net/http"

	userdir "github.com/txn2/mcp-data-platform/pkg/user"
)

// directoryUser is the minimal known-user representation exposed to the share
// picker. It deliberately omits bookkeeping fields (source, added_by, seen-at)
// — the picker only needs to show and resolve a name.
type directoryUser struct {
	Email     string `json:"email" example:"marcus.johnson@example.com"`
	FirstName string `json:"first_name,omitempty" example:"Marcus"`
	LastName  string `json:"last_name,omitempty" example:"Johnson"`
	Confirmed bool   `json:"confirmed" example:"true"`
}

// directoryUsersResponse wraps a page of directory users for the picker.
type directoryUsersResponse struct {
	Users []directoryUser `json:"users"`
	Total int             `json:"total" example:"42"`
}

// listDirectoryUsers handles GET /api/v1/portal/users.
//
// @Summary      List known users for the share picker
// @Description  Returns the known-users directory so a user can pick a teammate to share with. Includes admin-added people who have not logged in yet (confirmed=false). Any authenticated user may call this.
// @Tags         User
// @Produce      json
// @Param        q       query  string   false  "Case-insensitive match on email or name"
// @Param        limit   query  integer  false  "Results per page (default: 50, max: 100)"
// @Param        offset  query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  directoryUsersResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/users [get]
func (h *Handler) listDirectoryUsers(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	users, total, err := h.deps.UserDirectory.List(r.Context(), userdir.Filter{
		Query:  r.URL.Query().Get("q"),
		Limit:  intParam(r, paramLimit, defaultLimit),
		Offset: intParam(r, paramOffset, 0),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	out := make([]directoryUser, 0, len(users))
	for i := range users {
		out = append(out, directoryUser{
			Email:     users[i].Email,
			FirstName: users[i].FirstName,
			LastName:  users[i].LastName,
			Confirmed: users[i].Confirmed,
		})
	}
	writeJSON(w, http.StatusOK, directoryUsersResponse{Users: out, Total: total})
}
