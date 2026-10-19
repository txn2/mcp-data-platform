package settingsapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/storagecap"
)

// storagePath is the storage capacity settings (#2148).
const storagePath = "/api/v1/admin/settings/storage"

// registerStorage mounts the storage settings routes.
func registerStorage(mux *http.ServeMux, h *handler) {
	registerAlertSettings(mux, h, storagePath, h.cfg.Storage != nil, alertHandlers{get: h.getStorage, set: h.setStorage})
}

// getStorage handles GET /api/v1/admin/settings/storage.
//
// @Summary      Get storage capacity settings
// @Description  Returns the storage capacity settings: the database limit, the byte budget of each bucket (which overrides MCP_PLATFORM_STORAGE_BUDGETS for the buckets it names), the SeaweedFS cluster disk capacity is read from, how many days of history are kept, the warning and critical thresholds on percent used and on days until a store fills, and the notification channels the alerts are posted to. The defaults before anything is written are 180 days, 75%/90% and 30/7 days.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  storagecap.View
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/storage [get]
func (h *handler) getStorage(w http.ResponseWriter, r *http.Request) {
	getAlertSettings(w, r, "storage", h.readStorage)
}

// setStorage handles PUT /api/v1/admin/settings/storage.
//
// @Summary      Update storage capacity settings
// @Description  Writes the storage capacity settings. A field left out keeps its stored value. A budget is a positive number of bytes per bucket; the SeaweedFS addresses are http or https; a used_percent threshold is 0-100 with the warning below the critical; a days_left warning is more days than its critical; 0 turns a threshold off. A channel name no channel is stored under is refused.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  storagecap.Input  true  "Storage capacity settings"
// @Success      200  {object}  storagecap.View
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/storage [put]
func (h *handler) setStorage(w http.ResponseWriter, r *http.Request) {
	putAlertSettings[storagecap.Settings, storagecap.View, storagecap.Input](h, w, r, alertSection[storagecap.Settings, storagecap.View]{
		what: "storage", read: h.readStorage, set: h.cfg.Storage.Set,
		settings: func(v storagecap.View) storagecap.Settings { return v.Settings },
		channels: func(s storagecap.Settings) []string { return s.Channels },
		view: func(s storagecap.Settings, author, at string) storagecap.View {
			return storagecap.View{Settings: s, UpdatedBy: author, UpdatedAt: at}
		},
	})
}

// readStorage is the stored storage settings, or the defaults.
func (h *handler) readStorage(ctx context.Context) (storagecap.View, error) {
	v, err := storagecap.Read(ctx, h.cfg.Storage)
	if err != nil {
		return v, fmt.Errorf("storage settings: %w", err)
	}
	return v, nil
}

// alertSection is how the routes of one leveled alert's settings read, write
// and answer with them: S is the stored settings, V the view a route answers
// with.
type alertSection[S, V any] struct {
	// what names the settings in a failure's text.
	what string
	// read is the stored settings, or the defaults before any are written.
	read func(ctx context.Context) (V, error)
	set  func(ctx context.Context, v S, author string) error
	// settings is the settings a view carries.
	settings func(v V) S
	// channels is the notification channels the settings post to.
	channels func(s S) []string
	// view is the settings as written by author at at.
	view func(s S, author, at string) V
}

// getAlertSettings answers with the settings read reads, naming them what in
// a failure.
func getAlertSettings[V any](w http.ResponseWriter, r *http.Request, what string, read func(ctx context.Context) (V, error)) {
	v, err := read(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading "+what+" settings failed")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// putAlertSettings decodes an input I and writes it over the stored settings,
// once every channel they name exists, and answers with what was stored.
func putAlertSettings[S, V any, I interface{ Apply(stored S) (S, error) }](h *handler, w http.ResponseWriter, r *http.Request, sec alertSection[S, V]) {
	var in I
	if err := h.cfg.Decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stored, err := sec.read(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading "+sec.what+" settings failed")
		return
	}
	next, err := in.Apply(sec.settings(stored))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.channelsExist(w, r, sec.channels(next)) {
		return
	}
	author := h.cfg.Author(r)
	if err := sec.set(r.Context(), next, author); err != nil {
		writeError(w, http.StatusInternalServerError, "storing "+sec.what+" settings failed")
		return
	}
	writeJSON(w, http.StatusOK, sec.view(next, author, time.Now().UTC().Format(time.RFC3339)))
}
