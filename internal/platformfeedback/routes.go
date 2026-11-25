package platformfeedback

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/reqlog"
)

const (
	settingsPath = "/api/v1/admin/settings/platform-feedback"
	postsPath    = "/api/v1/admin/platform-feedback"
	maxBodyBytes = 64 << 10
)

// Register mounts the admin routes behind wrap: the settings, the list, one
// post, its status, and a resend.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, svc *Service, author func(*http.Request) string) {
	h := &handler{svc: svc, author: author}
	mux.Handle("GET "+settingsPath, wrap(http.HandlerFunc(h.getSettings)))
	mux.Handle("PUT "+settingsPath, wrap(http.HandlerFunc(h.putSettings)))
	mux.Handle("GET "+postsPath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("GET "+postsPath+"/{id}", wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+postsPath+"/{id}/status", wrap(http.HandlerFunc(h.status)))
	mux.Handle("POST "+postsPath+"/{id}/resend", wrap(http.HandlerFunc(h.resend)))
}

type handler struct {
	svc    *Service
	author func(*http.Request) string
}

// SettingsView is the settings with who saved them.
type SettingsView struct {
	Settings
	UpdatedBy string     `json:"updated_by,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func (h *handler) settingsView(r *http.Request) SettingsView {
	return viewOf(h.svc.Settings(r.Context()))
}

// viewOf is stored settings as the routes answer them.
func viewOf(got settingsjson.Stored[Settings]) SettingsView {
	v := SettingsView{Settings: got.Value, UpdatedBy: got.UpdatedBy}
	if !got.UpdatedAt.IsZero() {
		at := got.UpdatedAt
		v.UpdatedAt = &at
	}
	return v
}

// getSettings handles GET /api/v1/admin/settings/platform-feedback.
//
// @Summary      Get the platform feedback settings
// @Description  Returns whether agents may post platform feedback (#2820), the notification channels a new post is sent to, the personas that may post (empty is every persona except service accounts), and the per-session and per-persona daily limits. Off by default.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  SettingsView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/platform-feedback [get]
func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	httpjson.WriteJSON(w, http.StatusOK, h.settingsView(r))
}

// putSettings handles PUT /api/v1/admin/settings/platform-feedback.
//
// @Summary      Set the platform feedback settings
// @Description  Saves the settings. channels must name configured notification channels and personas existing personas; session_limit is 1 to 100 and persona_daily_limit 1 to 10000 (0 keeps the default of 5 and 50). Answers the saved settings as GET does.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  Settings  true  "The settings"
// @Success      200  {object}  SettingsView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/platform-feedback [put]
func (h *handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not platform feedback settings")
		return
	}
	saved, err := h.svc.SaveSettings(r.Context(), in, h.author(r))
	var invalid *InvalidError
	switch {
	case errors.As(err, &invalid):
		httpjson.WriteError(w, http.StatusBadRequest, invalid.Msg)
	case err != nil:
		reqlog.Failed(r, "platform feedback: saving the settings failed", err)
		httpjson.WriteError(w, http.StatusInternalServerError, "the settings could not be saved")
	default:
		httpjson.WriteJSON(w, http.StatusOK, viewOf(saved))
	}
}

// ListResponse is a page of posts and the page's counts.
type ListResponse struct {
	Posts []Item `json:"posts"`
	Total int    `json:"total"`
	Stats Stats  `json:"stats"`
}

// list handles GET /api/v1/admin/platform-feedback.
//
// @Summary      List platform feedback
// @Description  Lists what agents reported the platform could not do for them (#2820), newest report first, with how many reports each post has and the counts the page opens on: open posts, reports and refusals this week, and failed deliveries.
// @Tags         Platform feedback
// @Produce      json
// @Param        status  query  string  false  "open, planned, done or declined"
// @Param        tool    query  string  false  "The tool a post names"
// @Param        q       query  string  false  "Text in what it was trying to do, what blocked it, or its suggestion"
// @Param        limit   query  int     false  "At most 200 (default 50)"
// @Param        offset  query  int     false  "Posts to skip"
// @Success      200  {object}  ListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/platform-feedback [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	page, err := h.svc.List(r.Context(), ListFilter{
		Status: q.Get("status"), Tool: q.Get("tool"), Query: q.Get("q"), Limit: limit, Offset: offset,
	})
	if err != nil {
		reqlog.Failed(r, "platform feedback: listing failed", err)
		httpjson.WriteError(w, http.StatusInternalServerError, "platform feedback could not be listed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, page)
}

// get handles GET /api/v1/admin/platform-feedback/{id}.
//
// @Summary      Get one platform feedback post
// @Description  Returns one post with its reports (the persona, the calls named, when) and each channel's delivery as the notification queue holds it.
// @Tags         Platform feedback
// @Produce      json
// @Param        id  path  string  true  "Post id"
// @Success      200  {object}  Detail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/platform-feedback/{id} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Get(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "no such platform feedback post")
	case err != nil:
		reqlog.Failed(r, "platform feedback: reading a post failed", err)
		httpjson.WriteError(w, http.StatusInternalServerError, "the post could not be read")
	default:
		httpjson.WriteJSON(w, http.StatusOK, d)
	}
}

// StatusRequest moves a post to a status.
type StatusRequest struct {
	Status string `json:"status" example:"planned"`
}

// status handles PUT /api/v1/admin/platform-feedback/{id}/status.
//
// @Summary      Set a platform feedback post's status
// @Description  Moves a post to open, planned, done or declined. A status change is not sent to the channels.
// @Tags         Platform feedback
// @Accept       json
// @Param        id    path  string         true  "Post id"
// @Param        body  body  StatusRequest  true  "The status"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/platform-feedback/{id}/status [put]
func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	var in StatusRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not a status")
		return
	}
	h.done(w, r, h.svc.SetStatus(r.Context(), r.PathValue("id"), in.Status, h.author(r)))
}

// ResendRequest names the channel a post is sent to again.
type ResendRequest struct {
	Channel string `json:"channel" example:"ops-webhook"`
}

// resend handles POST /api/v1/admin/platform-feedback/{id}/resend.
//
// @Summary      Send a platform feedback post again
// @Description  Queues the post again for one of the channels platform feedback is sent to, as a failed delivery is retried by hand.
// @Tags         Platform feedback
// @Accept       json
// @Param        id    path  string         true  "Post id"
// @Param        body  body  ResendRequest  true  "The channel"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/platform-feedback/{id}/resend [post]
func (h *handler) resend(w http.ResponseWriter, r *http.Request) {
	var in ResendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not a channel")
		return
	}
	h.done(w, r, h.svc.Resend(r.Context(), r.PathValue("id"), in.Channel))
}

// done answers a write: 204, or the refusal.
func (*handler) done(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *InvalidError
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.As(err, &invalid):
		httpjson.WriteError(w, http.StatusBadRequest, invalid.Msg)
	case errors.Is(err, ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "no such platform feedback post")
	default:
		reqlog.Failed(r, "platform feedback: a write failed", err)
		httpjson.WriteError(w, http.StatusInternalServerError, "the change could not be made")
	}
}
