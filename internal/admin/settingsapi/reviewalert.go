package settingsapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
)

// reviewAlertRoute is one review queue's settings surface: the store holding
// its configuration and the target naming its defaults. Both queues are served
// by one implementation, so a threshold rule or a validation fix cannot apply
// to one of them and not the other.
type reviewAlertRoute struct {
	store  reviewalert.SettingsStore
	target reviewalert.Target
}

// knowledgeRoute is the knowledge insight queue's settings surface (#803).
func (h *handler) knowledgeRoute() reviewAlertRoute {
	return reviewAlertRoute{store: h.cfg.ReviewAlert, target: reviewalert.KnowledgeTarget()}
}

// scriptRoute is the managed-script review queue's settings surface (#1287).
func (h *handler) scriptRoute() reviewAlertRoute {
	return reviewAlertRoute{store: h.cfg.ScriptReviewAlert, target: reviewalert.ScriptTarget()}
}

// alertRoutes is one queue's pair of settings routes: where they live, the
// store that decides whether they exist at all, and the handlers behind them.
type alertRoutes struct {
	path  string
	store reviewalert.SettingsStore
	get   http.HandlerFunc
	set   http.HandlerFunc
}

// registerReviewAlert mounts the alert settings routes for every review queue
// the deployment has. Like the SMTP routes, reads need only the store and
// writes need database config mode.
func registerReviewAlert(mux *http.ServeMux, h *handler) {
	for _, route := range []alertRoutes{
		{
			path:  "/api/v1/admin/settings/review-queue-alert",
			store: h.cfg.ReviewAlert, get: h.getReviewAlert, set: h.setReviewAlert,
		},
		{
			path:  "/api/v1/admin/settings/script-review-alert",
			store: h.cfg.ScriptReviewAlert, get: h.getScriptReviewAlert, set: h.setScriptReviewAlert,
		},
	} {
		registerAlertRoutes(mux, h, route)
	}
}

// registerAlertRoutes mounts one queue's pair of routes, leaving both unmounted
// when the queue has no store: an operator must not be able to configure an
// alert nothing will ever send.
func registerAlertRoutes(mux *http.ServeMux, h *handler, route alertRoutes) {
	if route.store == nil {
		return
	}
	mux.HandleFunc("GET "+route.path, route.get)
	if h.cfg.Mutable {
		mux.HandleFunc("PUT "+route.path, route.set)
		return
	}
	mux.Handle("PUT "+route.path, h.cfg.ReadOnly)
}

// getReviewAlert handles GET /api/v1/admin/settings/review-queue-alert.
//
// @Summary      Get knowledge review-queue alert settings
// @Description  Returns the knowledge review-queue staleness alert configuration. Before an operator has written one, the platform defaults are returned with no recipients, and warnings state that nothing will be delivered.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  reviewalert.SettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/review-queue-alert [get]
func (h *handler) getReviewAlert(w http.ResponseWriter, r *http.Request) {
	writeReviewAlert(w, r, h.knowledgeRoute())
}

// setReviewAlert handles PUT /api/v1/admin/settings/review-queue-alert.
//
// @Summary      Update knowledge review-queue alert settings
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
	h.storeReviewAlert(w, r, h.knowledgeRoute())
}

// getScriptReviewAlert handles GET /api/v1/admin/settings/script-review-alert.
//
// @Summary      Get script review-queue alert settings
// @Description  Returns the managed-script review-queue alert configuration: when to alert that script versions are waiting for approval, and who hears about it. Before an operator has written one, the platform defaults are returned with no recipients.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  reviewalert.SettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/script-review-alert [get]
func (h *handler) getScriptReviewAlert(w http.ResponseWriter, r *http.Request) {
	writeReviewAlert(w, r, h.scriptRoute())
}

// setScriptReviewAlert handles PUT /api/v1/admin/settings/script-review-alert.
//
// @Summary      Update script review-queue alert settings
// @Description  Upserts the managed-script review-queue alert configuration: the pending-count and age thresholds, the re-alert cooldown, and the recipients the alert is delivered to.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  reviewalert.SettingsInput  true  "Script review alert settings"
// @Success      200  {object}  reviewalert.SettingsView
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/script-review-alert [put]
func (h *handler) setScriptReviewAlert(w http.ResponseWriter, r *http.Request) {
	h.storeReviewAlert(w, r, h.scriptRoute())
}

// writeReviewAlert answers with one queue's stored configuration, or the
// platform defaults when an operator has never written one.
func writeReviewAlert(w http.ResponseWriter, r *http.Request, route reviewAlertRoute) {
	settings, err := reviewalert.SettingsOf(r.Context(), route.store, route.target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading review queue alert settings failed")
		return
	}
	writeJSON(w, http.StatusOK, settings.View())
}

// storeReviewAlert upserts one queue's configuration and answers with the
// stored result, so the caller sees normalized recipients and re-evaluated
// warnings rather than an echo of what it sent.
func (h *handler) storeReviewAlert(w http.ResponseWriter, r *http.Request, route reviewAlertRoute) {
	var req reviewalert.SettingsInput
	if err := h.cfg.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errMsg := req.Validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if err := route.store.Set(r.Context(), req.Settings(), h.cfg.Author(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "storing review queue alert settings failed")
		return
	}
	writeReviewAlert(w, r, route)
}
