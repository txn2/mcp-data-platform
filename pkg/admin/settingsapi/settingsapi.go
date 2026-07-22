// Package settingsapi serves the /api/v1/admin/settings/smtp surface: the
// stored SMTP configuration (#631), the send-test action, and the test
// recipient's notification opt-out status (#1022). It is a decomposition seam
// of pkg/admin (which sits at the package size budget): the parent registers
// it on the admin mux and injects the request-scoped helpers it shares with
// the other admin routes.
package settingsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Config carries the stores and parent-owned helpers the routes need.
type Config struct {
	// Settings persists the admin SMTP configuration. nil disables every
	// route in this package.
	Settings notification.SettingsStore
	// SendTest delivers a test email through the stored SMTP settings. nil
	// disables the test route.
	SendTest func(ctx context.Context, to string) error
	// Prefs reads per-address notification preferences so the test-send UI
	// can surface a target's opt-out state (#1022). nil disables the
	// recipient-status route.
	Prefs notification.PrefsStore
	// Mutable reports database config mode; false swaps the write routes for
	// ReadOnly.
	Mutable bool
	// Author resolves the acting admin for audit columns.
	Author func(*http.Request) string
	// Decode is the parent's strict JSON body decoder (unknown fields
	// rejected, size-capped); its error text is safe to return as the
	// problem detail.
	Decode func(w http.ResponseWriter, r *http.Request, dst any) error
	// ReadOnly is the parent's 405 not-available-in-file-mode responder.
	ReadOnly http.HandlerFunc
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the settings routes on mux. Reads need only the store;
// writes need database config mode (405 otherwise, matching the other admin
// configuration surfaces).
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Settings == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/settings/smtp", h.getSMTP)
	if cfg.Prefs != nil {
		mux.HandleFunc("GET /api/v1/admin/settings/smtp/recipient-status", h.getRecipientStatus)
	}
	if cfg.Mutable {
		mux.HandleFunc("PUT /api/v1/admin/settings/smtp", h.setSMTP)
		if cfg.SendTest != nil {
			mux.HandleFunc("POST /api/v1/admin/settings/smtp/test", h.sendTest)
		}
	} else {
		mux.Handle("PUT /api/v1/admin/settings/smtp", cfg.ReadOnly)
		mux.Handle("POST /api/v1/admin/settings/smtp/test", cfg.ReadOnly)
	}
}

// getSMTP handles GET /api/v1/admin/settings/smtp.
//
// @Summary      Get SMTP settings
// @Description  Returns the stored SMTP configuration. The password is never returned; password_set reports whether one is stored.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  notification.SMTPSettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/smtp [get]
func (h *handler) getSMTP(w http.ResponseWriter, r *http.Request) {
	settings, err := h.cfg.Settings.GetSMTP(r.Context())
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

// setSMTP handles PUT /api/v1/admin/settings/smtp.
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
func (h *handler) setSMTP(w http.ResponseWriter, r *http.Request) {
	var req notification.SMTPSettingsInput
	if err := h.cfg.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errMsg := req.Validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if err := h.cfg.Settings.SetSMTP(r.Context(), req.Settings(), h.cfg.Author(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "storing smtp settings failed")
		return
	}
	h.getSMTP(w, r)
}

// sendTest handles POST /api/v1/admin/settings/smtp/test.
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
func (h *handler) sendTest(w http.ResponseWriter, r *http.Request) {
	var req notification.TestEmailRequest
	if err := h.cfg.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errMsg := req.Validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if err := h.cfg.SendTest(r.Context(), req.To); err != nil {
		if errors.Is(err, notification.ErrSMTPNotConfigured) {
			writeError(w, http.StatusConflict, "SMTP is disabled; enable and save the settings first")
			return
		}
		writeError(w, http.StatusBadGateway, "sending test email failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "to": req.To})
}

// getRecipientStatus handles GET /api/v1/admin/settings/smtp/recipient-status.
// It backs the test-send UI's informational opt-out notice (#1022): the test
// deliberately bypasses preference gating, so without this signal an address
// can receive test mail yet never receive notifications with nothing
// explaining why.
//
// @Summary      Notification opt-out state of an address
// @Description  Reports whether the given address has opted out of notification emails. Informational only; a test send is never blocked by it.
// @Tags         Settings
// @Produce      json
// @Param        to  query  string  true  "Recipient email address"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/smtp/recipient-status [get]
func (h *handler) getRecipientStatus(w http.ResponseWriter, r *http.Request) {
	addr, err := mail.ParseAddress(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "to must be a valid email address")
		return
	}
	// Canonicalize as the prefs writers do, so mixed-case input finds the row.
	email := strings.ToLower(strings.TrimSpace(addr.Address))
	prefs, err := h.cfg.Prefs.Get(r.Context(), email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading notification preferences failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"to":        email,
		"opted_out": prefs.Mode == notification.ModeOff,
	})
}

// problemDetail mirrors the parent admin package's RFC 9457 Problem Details
// response byte for byte, so this seam's errors are indistinguishable from
// every other admin route's.
type problemDetail struct {
	Type   string `json:"type" example:"about:blank"`
	Title  string `json:"title" example:"Not Found"`
	Status int    `json:"status" example:"404"`
	Detail string `json:"detail,omitempty" example:"resource not found"`
}

// writeJSON writes a JSON response, keeping a content type the caller
// already set (writeError's problem+json).
func writeJSON(w http.ResponseWriter, status int, v any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response using RFC 9457 Problem Details.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSON(w, status, problemDetail{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}
