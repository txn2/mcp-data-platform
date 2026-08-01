package settingsapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
)

// registerReviewAlert mounts the knowledge review-queue alert settings routes
// (#803). Like the SMTP routes, reads need only the store and writes need
// database config mode.
func registerReviewAlert(mux *http.ServeMux, h *handler) {
	if h.cfg.ReviewAlert == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/admin/settings/review-queue-alert", h.getReviewAlert)
	if h.cfg.Mutable {
		mux.HandleFunc("PUT /api/v1/admin/settings/review-queue-alert", h.setReviewAlert)
		return
	}
	mux.Handle("PUT /api/v1/admin/settings/review-queue-alert", h.cfg.ReadOnly)
}

// getReviewAlert handles GET /api/v1/admin/settings/review-queue-alert.
//
// @Summary      Get review-queue alert settings
// @Description  Returns the knowledge review-queue staleness alert configuration. Before an operator has written one, the platform defaults are returned with no recipients, and warnings state that nothing will be delivered.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  reviewalert.SettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/review-queue-alert [get]
func (h *handler) getReviewAlert(w http.ResponseWriter, r *http.Request) {
	settings, err := reviewalert.SettingsOf(r.Context(), h.cfg.ReviewAlert)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading review queue alert settings failed")
		return
	}
	writeJSON(w, http.StatusOK, settings.View())
}

// setReviewAlert handles PUT /api/v1/admin/settings/review-queue-alert.
//
// @Summary      Update review-queue alert settings
// @Description  Upserts the knowledge review-queue staleness alert configuration: the pending-count and age thresholds, the re-alert cooldown, and the recipients the digest is delivered to.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  reviewalert.SettingsInput  true  "Review-queue alert settings"
// @Success      200  {object}  reviewalert.SettingsView
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/review-queue-alert [put]
func (h *handler) setReviewAlert(w http.ResponseWriter, r *http.Request) {
	var req reviewalert.SettingsInput
	if err := h.cfg.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errMsg := req.Validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if err := h.cfg.ReviewAlert.Set(r.Context(), req.Settings(), h.cfg.Author(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "storing review queue alert settings failed")
		return
	}
	h.getReviewAlert(w, r)
}
