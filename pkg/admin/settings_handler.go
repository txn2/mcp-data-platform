package admin

import (
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// requestAuthor resolves the acting admin for audit columns: email, else
// user ID, else empty.
func requestAuthor(r *http.Request) string {
	user := GetUser(r.Context())
	if user == nil {
		return ""
	}
	if user.Email != "" {
		return user.Email
	}
	return user.UserID
}

// registerSettingsRoutes registers the platform settings endpoints (#631;
// SMTP first). Reads need only the store; writes need database config mode.
func (h *Handler) registerSettingsRoutes() {
	if h.deps.NotificationSettings == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/settings/smtp", h.getSMTPSettings)
	if h.isMutable() {
		h.mux.HandleFunc("PUT /api/v1/admin/settings/smtp", h.setSMTPSettings)
		if h.deps.SendTestEmail != nil {
			h.mux.HandleFunc("POST /api/v1/admin/settings/smtp/test", h.sendTestEmail)
		}
	} else {
		h.mux.HandleFunc("PUT /api/v1/admin/settings/smtp", h.readOnlyMethod())
		h.mux.HandleFunc("POST /api/v1/admin/settings/smtp/test", h.readOnlyMethod())
	}
}

// getSMTPSettings handles GET /api/v1/admin/settings/smtp.
//
// @Summary      Get SMTP settings
// @Description  Returns the stored SMTP configuration. The password is never returned; password_set reports whether one is stored.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  notification.SMTPSettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/smtp [get]
func (h *Handler) getSMTPSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.deps.NotificationSettings.GetSMTP(r.Context())
	if errors.Is(err, notification.ErrNotFound) {
		writeJSON(w, http.StatusOK, notification.UnconfiguredSMTPView())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading smtp settings failed")
		return
	}
	writeJSON(w, http.StatusOK, settings.View())
}

// setSMTPSettings handles PUT /api/v1/admin/settings/smtp.
//
// @Summary      Update SMTP settings
// @Description  Upserts the SMTP configuration. The password is encrypted at rest; sending an empty password keeps the stored one.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  notification.SMTPSettingsInput  true  "SMTP settings"
// @Success      200  {object}  notification.SMTPSettingsView
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/smtp [put]
func (h *Handler) setSMTPSettings(w http.ResponseWriter, r *http.Request) {
	var req notification.SMTPSettingsInput
	if err := decodeStrict(w, r, &req); err != nil {
		return
	}
	if errMsg := req.Validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if err := h.deps.NotificationSettings.SetSMTP(r.Context(), req.Settings(), requestAuthor(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "storing smtp settings failed")
		return
	}
	h.getSMTPSettings(w, r)
}

// sendTestEmail handles POST /api/v1/admin/settings/smtp/test.
//
// @Summary      Send a test email
// @Description  Delivers a test email through the stored SMTP settings so the configuration can be verified end to end.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  notification.TestEmailRequest  true  "Recipient"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      502  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/smtp/test [post]
func (h *Handler) sendTestEmail(w http.ResponseWriter, r *http.Request) {
	var req notification.TestEmailRequest
	if err := decodeStrict(w, r, &req); err != nil {
		return
	}
	if errMsg := req.Validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if err := h.deps.SendTestEmail(r.Context(), req.To); err != nil {
		if errors.Is(err, notification.ErrSMTPNotConfigured) {
			writeError(w, http.StatusConflict, "SMTP is disabled; enable and save the settings first")
			return
		}
		writeError(w, http.StatusBadGateway, "sending test email failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "to": req.To})
}
