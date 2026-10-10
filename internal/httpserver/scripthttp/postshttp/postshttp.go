// Package postshttp serves the posts a managed-script run made to notification
// channels (#1723): the run page's list of what the run sent with
// platform.notify and platform.publish, and what became of each.
//
// The posts are read from the notification queue by the run each post's
// document records, not from the run's outputs. An output is a file the run
// wrote; a post is a message, and what the person reading the run page wants
// to know about it is whether it arrived.
package postshttp

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// PostReader reads one run's posts. notifyqueue.PostgresStore implements it.
type PostReader interface {
	RunPosts(ctx context.Context, runID string) ([]notification.RunPost, error)
}

// Deps are the run lookup the route answers under and where the posts are
// read from.
type Deps struct {
	// Run reads the run in the path for a caller entitled to it, or writes
	// the refusal.
	Run   func(w http.ResponseWriter, r *http.Request) (*script.Run, bool)
	Posts PostReader
}

// Handler serves the route.
type Handler struct {
	deps Deps
}

// ForPortal builds the route over the script routes' own run lookup, so a run
// is found, and refused, the way every other run route finds it: readable by
// the script's owner, an administrator, and whoever requested the run.
func ForPortal(scripts *scripthttp.Handler, deps scripthttp.Deps, posts PostReader) *Handler {
	if posts == nil || deps.Runs == nil || deps.PortalUser == nil {
		return nil
	}
	return &Handler{deps: Deps{
		Run: func(w http.ResponseWriter, r *http.Request) (*script.Run, bool) {
			return scripts.ReadableRun(w, r, deps.PortalUser(r))
		},
		Posts: posts,
	}}
}

// New builds the handler from its dependencies.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// Register mounts the route, wrapped in the portal authentication middleware.
// A nil handler mounts nothing.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h == nil {
		return
	}
	mux.Handle("GET /api/v1/portal/scripts/{id}/runs/{runID}/posts", wrap(http.HandlerFunc(h.list)))
}

// Post is one post as the run page shows it.
type Post struct {
	Channel  string    `json:"channel" example:"ops-alerts"`
	Title    string    `json:"title" example:"Orders feed is 41 minutes behind"`
	QueuedAt time.Time `json:"queued_at"`
	// Status is sent when every delivery went out, failed when any gave up,
	// and queued while any is still waiting.
	Status string `json:"status" example:"sent"`
	// Deliveries counts what the post became: one for a chat or webhook
	// channel, one per recipient for an email channel.
	Deliveries int        `json:"deliveries" example:"1"`
	Sent       int        `json:"sent" example:"1"`
	Failed     int        `json:"failed" example:"0"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	LastError  string     `json:"last_error,omitempty" example:"upstream answered HTTP 403"`
}

// postsResponse is the route's answer.
type postsResponse struct {
	Posts []Post `json:"posts"`
}

// Post statuses.
const (
	statusSent   = "sent"
	statusFailed = "failed"
	statusQueued = "queued"
)

// list handles GET /api/v1/portal/scripts/{id}/runs/{runID}/posts.
//
// @Summary      List what a script run posted to channels
// @Description  Returns the posts a run made with platform.notify and platform.publish, oldest first: the channel, the title, when it was queued, and whether it was delivered. A post held back by its key's repeat window was never queued and is not listed. Readable by the script's owner, an administrator, and whoever requested the run.
// @Tags         Scripts
// @Produce      json
// @Param        id     path  string  true  "Script ID"
// @Param        runID  path  string  true  "Run ID"
// @Success      200  {object}  postsResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/runs/{runID}/posts [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	run, ok := h.deps.Run(w, r)
	if !ok {
		return
	}
	posts, err := h.deps.Posts.RunPosts(r.Context(), run.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the run's posts")
		return
	}
	out := postsResponse{Posts: make([]Post, 0, len(posts))}
	for _, p := range posts {
		out.Posts = append(out.Posts, Post{
			Channel: p.Channel, Title: p.Title, QueuedAt: p.QueuedAt, Status: statusOf(p),
			Deliveries: p.Deliveries, Sent: p.Sent, Failed: p.Failed, SentAt: p.LastSentAt, LastError: p.LastError,
		})
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// statusOf reads a post's deliveries as one status.
func statusOf(p notification.RunPost) string {
	switch {
	case p.Failed > 0:
		return statusFailed
	case p.Sent == p.Deliveries:
		return statusSent
	default:
		return statusQueued
	}
}
