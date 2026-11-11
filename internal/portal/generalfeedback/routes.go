package generalfeedback

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

const (
	settingsPath = "/api/v1/admin/settings/general-feedback"
	reachPath    = "/api/v1/portal/feedback/general"
	maxBodyBytes = 64 << 10
)

// RegisterSettings mounts the admin routes behind wrap.
func RegisterSettings(mux *http.ServeMux, wrap func(http.Handler) http.Handler, svc *Service, author func(*http.Request) string) {
	h := &handler{svc: svc, author: author}
	mux.Handle("GET "+settingsPath, wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+settingsPath, wrap(http.HandlerFunc(h.put)))
}

// RegisterReach mounts the portal route the composer reads behind wrap.
func RegisterReach(mux *http.ServeMux, wrap func(http.Handler) http.Handler, svc *Service) {
	h := &handler{svc: svc}
	mux.Handle("GET "+reachPath, wrap(http.HandlerFunc(h.reach)))
}

type handler struct {
	svc    *Service
	author func(*http.Request) string
}

// get handles GET /api/v1/admin/settings/general-feedback.
//
// @Summary      Get who hears about General feedback
// @Description  Returns who is told when someone posts to the General feedback channel (#2603): the personas whose people are told, the addresses told as well, the channels a new post is also sent to, and whether the author is sent an acknowledgement. people is everyone a new post reaches now; persona_options lists the personas with how many people each reaches; warnings carries reaches_nobody when the list reaches no person and no channel. Before a list is saved it is portal.feedback.standalone_recipients, or the administrator persona.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  View
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/general-feedback [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	httpjson.WriteJSON(w, http.StatusOK, h.svc.View(r.Context()))
}

// put handles PUT /api/v1/admin/settings/general-feedback.
//
// @Summary      Set who hears about General feedback
// @Description  Saves the list. personas must name existing personas (at most 20), recipients must be email addresses (at most 50), and channels must name configured notification channels. Answers the saved list as GET does.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  Settings  true  "The list"
// @Success      200  {object}  View
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/general-feedback [put]
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	var in Settings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not a general feedback list")
		return
	}
	view, err := h.svc.Save(r.Context(), in, h.author(r))
	var invalid *InvalidError
	switch {
	case errors.As(err, &invalid):
		httpjson.WriteError(w, http.StatusBadRequest, invalid.Msg)
	case errors.Is(err, ErrNoStore):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case err != nil:
		slog.ErrorContext(r.Context(), "general feedback: saving the list failed", "error", logsan.SanitizeForLog(err.Error())) // #nosec G706 -- sanitized
		httpjson.WriteError(w, http.StatusInternalServerError, "the list could not be saved")
	default:
		httpjson.WriteJSON(w, http.StatusOK, view)
	}
}

// Reach is whether a new General post is told to anyone, for the line the
// composer shows. It never names who.
type Reach struct {
	ReachesSomeone bool `json:"reaches_someone" example:"true"`
}

// reach handles GET /api/v1/portal/feedback/general.
//
// @Summary      Whether General feedback reaches anyone
// @Description  Answers whether a new post to the General feedback channel is told to anyone (#2603), so the composer can say so before the author posts. It never names the recipients.
// @Tags         Feedback
// @Produce      json
// @Success      200  {object}  Reach
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/feedback/general [get]
func (h *handler) reach(w http.ResponseWriter, r *http.Request) {
	httpjson.WriteJSON(w, http.StatusOK, Reach{ReachesSomeone: h.svc.ReachesSomeone(r.Context())})
}
