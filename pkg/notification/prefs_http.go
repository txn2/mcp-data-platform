package notification

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// PrefsAPI serves the self-scoped notification preference REST endpoints.
// It registers onto the portal's authenticated mux via a registrar hook (the
// datahubapi pattern) so the feature lives in this package; the composition
// root supplies UserEmail to resolve the authenticated caller. Server-side
// self-scope: the caller's email is the only key ever read or written.
type PrefsAPI struct {
	Store PrefsStore
	// Settings backs the delivery_available signal. Optional: when unset the
	// field stays true, so a wiring gap never tells users a working feature
	// is unavailable. Only the derived boolean is exposed -- no SMTP host,
	// credential, or sender value reaches this non-admin response.
	Settings SettingsStore
	// UserEmail resolves the authenticated user's email from the request,
	// returning "" when unauthenticated.
	UserEmail func(*http.Request) string
}

// PrefsResponse is the user-facing preferences shape.
type PrefsResponse struct {
	Mode            string `json:"mode"`
	SharesEnabled   bool   `json:"shares_enabled"`
	CommentsEnabled bool   `json:"comments_enabled"`
	MentionsEnabled bool   `json:"mentions_enabled"`
	// DeliveryAvailable reports whether the platform currently has an SMTP
	// path that could deliver these notifications. False means stored
	// preferences describe an intent nothing can act on: triggers keep
	// queueing rows and those rows expire undelivered.
	DeliveryAvailable bool `json:"delivery_available"`
}

// PrefsRequest is the body for updating the caller's preferences. Omitted
// fields are left unchanged.
type PrefsRequest struct {
	Mode            *string `json:"mode,omitempty"`
	SharesEnabled   *bool   `json:"shares_enabled,omitempty"`
	CommentsEnabled *bool   `json:"comments_enabled,omitempty"`
	MentionsEnabled *bool   `json:"mentions_enabled,omitempty"`
}

// Register mounts the preference endpoints on mux.
func (a *PrefsAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/notification-prefs", a.getPrefs)
	mux.HandleFunc("PUT /api/v1/portal/notification-prefs", a.putPrefs)
}

// getPrefs handles GET /api/v1/portal/notification-prefs.
//
// @Summary      Get my notification preferences
// @Description  Returns the calling user's email notification preferences. Users with no stored preferences get the defaults (immediate delivery, all categories on).
// @Tags         Notifications
// @Produce      json
// @Success      200  {object}  PrefsResponse
// @Failure      401  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/notification-prefs [get]
func (a *PrefsAPI) getPrefs(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	prefs, err := a.Store.Get(r.Context(), email)
	if err != nil {
		writePrefsError(w, http.StatusInternalServerError, "reading notification preferences failed")
		return
	}
	writePrefsJSON(w, prefsResponse(prefs, a.deliveryAvailable(r.Context())))
}

// putPrefs handles PUT /api/v1/portal/notification-prefs.
//
// @Summary      Update my notification preferences
// @Description  Updates the calling user's email notification preferences. Omitted fields are left unchanged. Server-side self-scope: only the caller's own preferences are ever written.
// @Tags         Notifications
// @Accept       json
// @Produce      json
// @Param        request  body  PrefsRequest  true  "Preference changes"
// @Success      200  {object}  PrefsResponse
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/notification-prefs [put]
func (a *PrefsAPI) putPrefs(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	var req PrefsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePrefsError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Mode != nil && !ValidMode(*req.Mode) {
		writePrefsError(w, http.StatusBadRequest, "mode must be off, immediate, or daily")
		return
	}
	prefs, err := a.Store.Set(r.Context(), email, PrefsUpdate(req))
	if err != nil {
		writePrefsError(w, http.StatusInternalServerError, "storing notification preferences failed")
		return
	}
	writePrefsJSON(w, prefsResponse(prefs, a.deliveryAvailable(r.Context())))
}

// deliveryAvailable reports whether a queued notification currently has a
// path to a mailbox: SMTP configured, enabled, and pointed at a host. A read
// failure reports available -- a transient store error must not tell users a
// configured feature is off.
func (a *PrefsAPI) deliveryAvailable(ctx context.Context) bool {
	if a.Settings == nil {
		return true
	}
	settings, err := a.Settings.GetSMTP(ctx)
	if errors.Is(err, ErrNotFound) {
		return false
	}
	if err != nil {
		slog.Warn("notification: reading smtp settings for delivery signal failed", logKeyError, err)
		return true
	}
	return settings != nil && settings.Enabled && settings.Host != ""
}

// callerEmail resolves the authenticated caller, writing a 401 when absent.
func (a *PrefsAPI) callerEmail(w http.ResponseWriter, r *http.Request) string {
	email := ""
	if a.UserEmail != nil {
		// Same normalization the queue keys rows by, so a caller whose
		// identity carries a display name reads and writes the row their
		// notifications are addressed to.
		email = NormalizeAddress(a.UserEmail(r))
	}
	if email == "" {
		writePrefsError(w, http.StatusUnauthorized, "authentication required")
	}
	return email
}

// prefsResponse maps store preferences to the API shape.
func prefsResponse(p Prefs, deliveryAvailable bool) PrefsResponse {
	return PrefsResponse{
		Mode:              p.Mode,
		SharesEnabled:     p.SharesEnabled,
		CommentsEnabled:   p.CommentsEnabled,
		MentionsEnabled:   p.MentionsEnabled,
		DeliveryAvailable: deliveryAvailable,
	}
}

func writePrefsJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writePrefsError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
