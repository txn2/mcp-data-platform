package settingsapi

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptalert"
)

// scriptRunAlertPath is the settings surface for the alert a failed scheduled
// run raises (#1724).
const scriptRunAlertPath = "/api/v1/admin/settings/script-run-alert"

// registerScriptRunAlert mounts the scheduled-run failure alert routes.
func registerScriptRunAlert(mux *http.ServeMux, h *handler) {
	registerAlertSettings(mux, h, scriptRunAlertPath, h.cfg.ScriptRunAlert != nil, alertHandlers{get: h.getScriptRunAlert, set: h.setScriptRunAlert})
}

// setScriptRunAlert handles PUT /api/v1/admin/settings/script-run-alert.
//
// @Summary      Update scheduled-run failure alert settings
// @Description  Sets the notification channels a failed scheduled run is posted to. An absent channels field keeps the stored list and an empty one clears it; a name no channel is stored under is refused.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  scriptalert.SettingsInput  true  "Scheduled-run failure alert settings"
// @Success      200  {object}  scriptalert.SettingsView
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/script-run-alert [put]
func (h *handler) setScriptRunAlert(w http.ResponseWriter, r *http.Request) {
	var req scriptalert.SettingsInput
	if err := h.cfg.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if msg := req.Validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	stored, err := scriptalert.SettingsOf(r.Context(), h.cfg.ScriptRunAlert)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading scheduled-run failure alert settings failed")
		return
	}
	next := req.Settings()
	if req.Channels == nil {
		next.Channels = stored.Channels
	}
	if !h.channelsExist(w, r, next.Channels) {
		return
	}
	author := h.cfg.Author(r)
	if err := h.cfg.ScriptRunAlert.Set(r.Context(), next, author); err != nil {
		writeError(w, http.StatusInternalServerError, "storing scheduled-run failure alert settings failed")
		return
	}
	next.UpdatedBy, next.UpdatedAt = author, time.Now().UTC()
	writeJSON(w, http.StatusOK, next.View())
}

// getScriptRunAlert handles GET /api/v1/admin/settings/script-run-alert.
//
// @Summary      Get scheduled-run failure alert settings
// @Description  Returns the notification channels a failed scheduled run of any managed script is posted to, beside the email its owner receives. Empty before an operator has named any.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  scriptalert.SettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/script-run-alert [get]
func (h *handler) getScriptRunAlert(w http.ResponseWriter, r *http.Request) {
	settings, err := scriptalert.SettingsOf(r.Context(), h.cfg.ScriptRunAlert)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading scheduled-run failure alert settings failed")
		return
	}
	writeJSON(w, http.StatusOK, settings.View())
}
