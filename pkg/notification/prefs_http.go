package notification

import (
	"encoding/json"
	"net/http"
	"strings"
)

// PrefsAPI serves the self-scoped notification preference REST endpoints.
// It registers onto the portal's authenticated mux via a registrar hook (the
// datahubapi pattern) so the feature lives in this package; the composition
// root supplies UserEmail to resolve the authenticated caller. Server-side
// self-scope: the caller's email is the only key ever read or written.
type PrefsAPI struct {
	Store PrefsStore
	// UserEmail resolves the authenticated user's email from the request,
	// returning "" when unauthenticated.
	UserEmail func(*http.Request) string
}

// PrefsResponse is the user-facing preferences shape.
type PrefsResponse struct {
	Mode            string `json:"mode"`
	SharesEnabled   bool   `json:"shares_enabled"`
	CommentsEnabled bool   `json:"comments_enabled"`
}

// PrefsRequest is the body for updating the caller's preferences. Omitted
// fields are left unchanged.
type PrefsRequest struct {
	Mode            *string `json:"mode,omitempty"`
	SharesEnabled   *bool   `json:"shares_enabled,omitempty"`
	CommentsEnabled *bool   `json:"comments_enabled,omitempty"`
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
	writePrefsJSON(w, prefsResponse(prefs))
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
	writePrefsJSON(w, prefsResponse(prefs))
}

// callerEmail resolves the authenticated caller, writing a 401 when absent.
func (a *PrefsAPI) callerEmail(w http.ResponseWriter, r *http.Request) string {
	email := ""
	if a.UserEmail != nil {
		email = strings.ToLower(strings.TrimSpace(a.UserEmail(r)))
	}
	if email == "" {
		writePrefsError(w, http.StatusUnauthorized, "authentication required")
	}
	return email
}

// prefsResponse maps store preferences to the API shape.
func prefsResponse(p Prefs) PrefsResponse {
	return PrefsResponse{Mode: p.Mode, SharesEnabled: p.SharesEnabled, CommentsEnabled: p.CommentsEnabled}
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
