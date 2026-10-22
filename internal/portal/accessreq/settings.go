package accessreq

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
)

const settingsPath = "/api/v1/admin/settings/access-requests"

// SettingsStore reads and writes the section.
type SettingsStore interface {
	Read(ctx context.Context) Settings
	Write(ctx context.Context, s Settings, author string) error
}

// RegisterSettings mounts the admin settings routes behind wrap.
func RegisterSettings(mux *http.ServeMux, wrap func(http.Handler) http.Handler, store SettingsStore, author func(*http.Request) string) {
	h := &settingsHandler{store: store, author: author}
	mux.Handle("GET "+settingsPath, wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+settingsPath, wrap(http.HandlerFunc(h.put)))
}

type settingsHandler struct {
	store  SettingsStore
	author func(*http.Request) string
}

// get handles GET /api/v1/admin/settings/access-requests.
//
// @Summary      Get the access request settings
// @Description  Returns whether the page a person who cannot open an item sees names the item (reveal_names, default false: a generic title), and how many days a request for access waits for an answer (expire_days, default 14) (#2090).
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  Settings
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/access-requests [get]
func (h *settingsHandler) get(w http.ResponseWriter, r *http.Request) {
	httpjson.WriteJSON(w, http.StatusOK, h.store.Read(r.Context()))
}

// put handles PUT /api/v1/admin/settings/access-requests.
//
// @Summary      Update the access request settings
// @Description  Sets reveal_names and expire_days (1 to 365). A request already made keeps the expiry it was made with.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  Settings  true  "The settings"
// @Success      200  {object}  Settings
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/access-requests [put]
func (h *settingsHandler) put(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if !decode(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.Write(r.Context(), in, h.author(r)); err != nil {
		fail(w, r, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, in)
}
