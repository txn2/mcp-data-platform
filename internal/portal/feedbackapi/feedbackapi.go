// Package feedbackapi holds the portal's feedback surface: threads and their
// events, the activity feed, the practitioner and SME worklists, asset and
// collection sign-off, validation responses, and capturing a thread as an
// insight.
//
// These routes are one family. They share a target model (a thread hangs off an
// asset, a collection, a prompt, a knowledge page, or nothing), one set of
// visibility rules over those targets, and one target-gathering policy that the
// worklist and the activity feed both read. Splitting them apart would fork
// that policy; leaving them in pkg/portal kept that package at its size ceiling.
//
// The seam owns no policy of its own: every permission decision goes through
// the shared authorization core in internal/portal/access, which pkg/portal
// builds once and hands over, so a check cannot mean one thing here and another
// in the parent.
package feedbackapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// Common error messages, path value keys, and query parameter names. They are
// spelled here rather than imported from the parent because pkg/portal imports
// this package to register its routes and so cannot be imported back; the
// wording is what a client sees and must stay identical on both sides.
const (
	errAuthRequired             = "authentication required"
	errAccessDenied             = "access denied"
	errAssetNotFound            = "asset not found"
	errCollectionNotFound       = "collection not found"
	errKnowledgePageNotFoundMsg = "knowledge page not found"
	errInvalidRequestBody       = "invalid request body"

	pathKeyID = "id"
	// logKeyError is the structured-logging key for an error value.
	logKeyError = "error"
	paramLimit  = "limit"
	paramOffset = "offset"

	statusDeleted = "deleted"
)

// ChangesetReader provides read access to knowledge changesets, used to surface
// the thread -> insight -> changeset chain on a feedback thread.
type ChangesetReader interface {
	ListChangesets(ctx context.Context, filter knowledge.ChangesetFilter) ([]knowledge.Changeset, int, error)
}

// MemoryWriter inserts memory records. It backs the "capture feedback as an
// insight" path: a reviewer turns a feedback thread into a pending,
// knowledge-dimension memory record that enters the apply_knowledge review
// queue.
type MemoryWriter interface {
	Insert(ctx context.Context, record memory.Record) error
}

// MentionResolver returns the addresses a comment body delivers an @-mention
// to on a thread target: the names written in the body, minus the author's own
// address, filtered to the people who can open that target. A nil resolver
// disables mentions (no database), leaving every token ordinary text.
type MentionResolver interface {
	ResolveMentions(ctx context.Context, targetType, targetID, body, author string) []string
}

// ThreadNotifier receives the thread half of the portal's notification
// triggers. It is the narrow view of the platform notifier this surface needs;
// the share half stays with the routes that create shares. A nil notifier
// disables thread notifications.
type ThreadNotifier interface {
	// NotifyThreadEvent fires after a successful thread create or event
	// append. thread carries the target reference; body is the comment text;
	// mentioned carries the @-mentions the body delivers, already filtered to
	// people who can open the target.
	NotifyThreadEvent(ctx context.Context, thread *threads.Thread, actorEmail, body string, mentioned []string)
}

// Config carries everything the feedback routes need. A nil store disables the
// routes that require it rather than failing them at request time, except where
// the handler reports the capability as unconfigured (503).
type Config struct {
	Threads        threads.ThreadStore
	Assets         portaldomain.AssetStore
	Collections    portaldomain.CollectionStore
	Shares         portaldomain.ShareStore
	Prompts        prompt.Store
	KnowledgePages knowledgepage.Store
	Changesets     ChangesetReader
	MemoryWriter   MemoryWriter
	Mentions       MentionResolver
	Notifier       ThreadNotifier
	// Access is the portal's authorization core, built by the parent so this
	// surface and the routes that stayed behind answer permission questions
	// the same way.
	Access *access.Checker
	// PersonaName resolves a caller's roles to their persona name, the value
	// stamped on a captured insight. nil yields no persona.
	PersonaName func(roles []string) string
}

// Handler serves the portal's feedback routes.
type Handler struct {
	cfg    Config
	access *access.Checker
}

// New returns a Handler over cfg.
func New(cfg Config) *Handler {
	return &Handler{cfg: cfg, access: cfg.Access}
}

// Register wires the feedback routes onto mux. Threads require a thread store;
// with none configured the surface registers nothing, which is what a portal
// running without a database expects.
func (h *Handler) Register(mux *http.ServeMux) {
	if h.cfg.Threads == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/portal/threads", h.listThreads)
	mux.HandleFunc("POST /api/v1/portal/threads", h.createThread)
	mux.HandleFunc("GET /api/v1/portal/threads/counts", h.threadCounts)
	mux.HandleFunc("GET /api/v1/portal/threads/{id}", h.getThread)
	mux.HandleFunc("PATCH /api/v1/portal/threads/{id}", h.updateThread)
	mux.HandleFunc("DELETE /api/v1/portal/threads/{id}", h.deleteThread)
	mux.HandleFunc("GET /api/v1/portal/threads/{id}/events", h.listThreadEvents)
	mux.HandleFunc("GET /api/v1/portal/threads/{id}/chain", h.getThreadChain)
	mux.HandleFunc("POST /api/v1/portal/threads/{id}/events", h.appendThreadEvent)
	mux.HandleFunc("GET /api/v1/portal/feedback/activity", h.feedbackActivity)
	mux.HandleFunc("GET /api/v1/portal/worklist/practitioner", h.practitionerWorklist)
	mux.HandleFunc("GET /api/v1/portal/worklist/sme", h.smeWorklist)
	mux.HandleFunc("GET /api/v1/portal/assets/{id}/signoff", h.assetSignoff)
	mux.HandleFunc("GET /api/v1/portal/collections/{id}/signoff", h.collectionSignoff)
	mux.HandleFunc("POST /api/v1/portal/threads/{id}/validation", h.respondValidation)
}

// RegisterInsightCapture wires the "capture feedback as an insight" route. It
// is separate from Register because it needs a memory writer as well as a
// thread store, the same pair the route required before the split.
func (h *Handler) RegisterInsightCapture(mux *http.ServeMux) {
	if h.cfg.Threads == nil || h.cfg.MemoryWriter == nil {
		return
	}
	mux.HandleFunc("POST /api/v1/portal/threads/{id}/insight", h.captureThreadInsight)
}

// pagedResponse is the list envelope the feedback lists return. It matches the
// parent's paginated envelope field for field, so a client cannot tell which
// side of the split served the request.
type pagedResponse struct {
	Data   any `json:"data"`
	Total  int `json:"total" example:"42"`
	Limit  int `json:"limit" example:"20"`
	Offset int `json:"offset" example:"0"`
}

// intParam reads a non-negative integer query parameter, falling back on an
// absent, unparseable, or negative value.
func intParam(r *http.Request, name string, fallback int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// assetViewable reports whether the caller may view the asset, writing the
// denial itself. It is the one place this surface turns the shared view check
// into an HTTP status, so a lookup failure is a 500 and a genuine denial a 403.
func (h *Handler) assetViewable(w http.ResponseWriter, r *http.Request, assetID string, asset *portaldomain.Asset, user *access.User) bool {
	granted, err := h.access.AssetViewGrant(r.Context(), assetID, asset, user)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to check share access")
		return false
	}
	if !granted {
		httpjson.WriteError(w, http.StatusForbidden, errAccessDenied)
	}
	return granted
}

// notifyThreadEvent fires the thread notification trigger when a notifier is
// wired. It never fails the originating request.
func (h *Handler) notifyThreadEvent(ctx context.Context, thread *threads.Thread, actorEmail, body string, mentioned []string) {
	if h.cfg.Notifier != nil {
		h.cfg.Notifier.NotifyThreadEvent(ctx, thread, actorEmail, body, mentioned)
	}
}
