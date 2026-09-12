package settingsapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/connalert"
)

// connAlertPath is the settings surface for connection-revocation alerts
// (#1694).
const connAlertPath = "/api/v1/admin/settings/connection-alert"

// registerConnAlert mounts the connection-revocation alert settings routes.
// Like the review-queue routes, reads need only the store and writes need
// database config mode; with no store both stay unmounted, since an operator
// must not be able to name recipients for an alert nothing will ever send.
func registerConnAlert(mux *http.ServeMux, h *handler) {
	if h.cfg.ConnectionAlert == nil {
		return
	}
	mux.HandleFunc("GET "+connAlertPath, h.getConnAlert)
	if h.cfg.Mutable {
		mux.HandleFunc("PUT "+connAlertPath, h.setConnAlert)
		return
	}
	mux.Handle("PUT "+connAlertPath, h.cfg.ReadOnly)
}

// getConnAlert handles GET /api/v1/admin/settings/connection-alert.
//
// @Summary      Get connection-revocation alert settings
// @Description  Returns the configuration for the alert raised when an upstream rejects a connection's refresh and the platform discards the credential. Before an operator has written one, the platform defaults are returned with no escalation recipients, and a warning states that only the person who authorized a connection is told.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  connalert.SettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/connection-alert [get]
func (h *handler) getConnAlert(w http.ResponseWriter, r *http.Request) {
	h.writeConnAlert(w, r)
}

// setConnAlert handles PUT /api/v1/admin/settings/connection-alert.
//
// @Summary      Update connection-revocation alert settings
// @Description  Upserts the connection-revocation alert configuration: whether the alerts are sent at all, how long a revoked connection goes unauthorized before the escalation is raised, and the addresses it is raised with.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  connalert.SettingsInput  true  "Connection alert settings"
// @Success      200  {object}  connalert.SettingsView
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/connection-alert [put]
func (h *handler) setConnAlert(w http.ResponseWriter, r *http.Request) {
	var req connalert.SettingsInput
	if err := h.cfg.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errMsg := req.Validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if err := h.cfg.ConnectionAlert.Set(r.Context(), req.Settings(), h.cfg.Author(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "storing connection alert settings failed")
		return
	}
	h.writeConnAlert(w, r)
}

// writeConnAlert answers with the stored configuration, or the platform
// defaults when an operator has never written one. A write answers through it
// too, so the caller sees normalized recipients and re-evaluated warnings
// rather than an echo of what it sent.
func (h *handler) writeConnAlert(w http.ResponseWriter, r *http.Request) {
	settings, err := connalert.SettingsOf(r.Context(), h.cfg.ConnectionAlert)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading connection alert settings failed")
		return
	}
	writeJSON(w, http.StatusOK, settings.View())
}
