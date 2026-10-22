package accessreq

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
)

// Target is the item a request is about, as its owner holds it.
type Target struct {
	Name       string
	OwnerID    string
	OwnerEmail string
}

// Requests is the store the routes read and write; *Store implements it.
type Requests interface {
	Open(ctx context.Context, r Request) (*Request, bool, error)
	Pending(ctx context.Context, targetType, targetID, requesterID string) (*Request, error)
	Get(ctx context.Context, id string) (*Request, error)
	ListForOwner(ctx context.Context, ownerEmail, targetType, targetID string, all bool) ([]Request, error)
	Decide(ctx context.Context, id string, d Decision) error
}

// Deps is what the routes are built from.
type Deps struct {
	Store Requests
	// Resolve reads an item; an error is an item that does not exist.
	Resolve func(ctx context.Context, targetType, id string) (Target, error)
	// CanOpen reports whether the user already opens the item.
	CanOpen func(ctx context.Context, user *access.User, targetType, id string) bool
	// IsAdmin is the platform's administrator rule.
	IsAdmin func(user *access.User) bool
	// Share creates the person share an approval grants and returns its id.
	Share func(ctx context.Context, targetType, id, createdBy, recipientEmail, permission string) (string, error)
	// NotifyOwner tells the owner a request is waiting; NotifyRequester tells
	// the requester how it was answered. Either may be nil.
	NotifyOwner     func(ctx context.Context, req *Request, itemName string)
	NotifyRequester func(ctx context.Context, req *Request, itemName string)
	// Settings reads the stored configuration; nil uses Defaults.
	Settings func(ctx context.Context) Settings
}

// plural maps the route segment to the target type.
var plural = map[string]string{"assets": TargetAsset, "collections": TargetCollection, "prompts": TargetPrompt}

const (
	maxBody     = 8 << 10
	errNotFound = "not found"
	errAuth     = "authentication required"
)

type handler struct{ d Deps }

// Register mounts the routes, each behind wrap, the portal's authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, d Deps) {
	h := &handler{d: d}
	mux.Handle("GET /api/v1/portal/assets/{id}/access-request", wrap(http.HandlerFunc(h.page)))
	mux.Handle("GET /api/v1/portal/collections/{id}/access-request", wrap(http.HandlerFunc(h.page)))
	mux.Handle("GET /api/v1/portal/prompts/{id}/access-request", wrap(http.HandlerFunc(h.page)))
	mux.Handle("POST /api/v1/portal/assets/{id}/access-requests", wrap(http.HandlerFunc(h.open)))
	mux.Handle("POST /api/v1/portal/collections/{id}/access-requests", wrap(http.HandlerFunc(h.open)))
	mux.Handle("POST /api/v1/portal/prompts/{id}/access-requests", wrap(http.HandlerFunc(h.open)))
	mux.Handle("GET /api/v1/portal/access-requests", wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/portal/access-requests/{id}/approve", wrap(http.HandlerFunc(h.approve)))
	mux.Handle("POST /api/v1/portal/access-requests/{id}/decline", wrap(http.HandlerFunc(h.decline)))
}

func (h *handler) settings(ctx context.Context) Settings {
	if h.d.Settings == nil {
		return Defaults()
	}
	return h.d.Settings(ctx)
}

// item is the item a route names.
type item struct {
	targetType, id string
	Target
}

// target reads the item a route names, writing 404 and returning nil when
// there is none.
func (h *handler) target(w http.ResponseWriter, r *http.Request) *item {
	seg, _, _ := strings.Cut(r.URL.Path[len("/api/v1/portal/"):], "/")
	targetType, id := plural[seg], r.PathValue("id")
	t, err := h.d.Resolve(r.Context(), targetType, id)
	if targetType == "" || err != nil {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return nil
	}
	return &item{targetType: targetType, id: id, Target: t}
}

// page handles GET /api/v1/portal/{assets|collections|prompts}/{id}/access-request.
//
// @Summary      Read the request-access page
// @Description  What the page shown to a signed-in person who cannot open an item says (#2090): whether they already open it, the item's owner, the item's name when the access_requests setting reveals names, and their pending request for it if they have one.
// @Tags         Access requests
// @Produce      json
// @Param        id  path  string  true  "Item id"
// @Success      200  {object}  Page
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/access-request [get]
// @Router       /portal/collections/{id}/access-request [get]
// @Router       /portal/prompts/{id}/access-request [get]
func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuth)
		return
	}
	it := h.target(w, r)
	if it == nil {
		return
	}
	page := Page{HasAccess: h.d.CanOpen(r.Context(), user, it.targetType, it.id), OwnerEmail: it.OwnerEmail}
	if h.settings(r.Context()).RevealNames || page.HasAccess {
		page.Name = it.Name
	}
	if pending, err := h.d.Store.Pending(r.Context(), it.targetType, it.id, user.UserID); err == nil {
		page.Pending = pending
	}
	httpjson.WriteJSON(w, http.StatusOK, page)
}

// open handles POST /api/v1/portal/{assets|collections|prompts}/{id}/access-requests.
//
// @Summary      Request access to an item
// @Description  Asks the owner of an asset, collection or prompt for access (#2090), with an optional message. The owner is notified by their notification preferences with Approve as viewer, Approve as editor and Decline. A second request while one is pending answers 200 with that request and notifies nobody; a person who already opens the item is refused with 409, and one who has asked three times for it in a day with 429.
// @Tags         Access requests
// @Accept       json
// @Produce      json
// @Param        id    path  string       true  "Item id"
// @Param        body  body  OpenRequest  false "The message"
// @Success      201  {object}  Request
// @Success      200  {object}  Request
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      429  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/access-requests [post]
// @Router       /portal/collections/{id}/access-requests [post]
// @Router       /portal/prompts/{id}/access-requests [post]
func (h *handler) open(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuth)
		return
	}
	it := h.target(w, r)
	if it == nil {
		return
	}
	msg, ok := readMessage(w, r)
	if !ok {
		return
	}
	if h.d.CanOpen(r.Context(), user, it.targetType, it.id) {
		httpjson.WriteError(w, http.StatusConflict, "you already have access to this item")
		return
	}
	req, created, err := h.d.Store.Open(r.Context(), Request{
		ID: uuid.NewString(), TargetType: it.targetType, TargetID: it.id,
		RequesterID: user.UserID, RequesterEmail: user.Email, OwnerEmail: it.OwnerEmail, Message: msg,
		ExpiresAt: time.Now().Add(time.Duration(h.settings(r.Context()).ExpireDays) * 24 * time.Hour),
	})
	switch {
	case errors.Is(err, ErrTooMany):
		httpjson.WriteError(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		fail(w, r, err)
		return
	}
	if !created {
		httpjson.WriteJSON(w, http.StatusOK, req)
		return
	}
	if h.d.NotifyOwner != nil {
		h.d.NotifyOwner(r.Context(), req, it.Name)
	}
	httpjson.WriteJSON(w, http.StatusCreated, req)
}

// list handles GET /api/v1/portal/access-requests.
//
// @Summary      List access requests waiting on me
// @Description  Returns the pending requests for items the caller owns (#2090), every pending request for an administrator, or one item's with target_type and target_id.
// @Tags         Access requests
// @Produce      json
// @Param        target_type  query  string  false  "asset, collection or prompt"
// @Param        target_id    query  string  false  "Item id"
// @Success      200  {object}  RequestList
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/access-requests [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuth)
		return
	}
	q := r.URL.Query()
	reqs, err := h.d.Store.ListForOwner(r.Context(), user.Email, q.Get("target_type"), q.Get("target_id"), h.d.IsAdmin(user))
	if err != nil {
		fail(w, r, err)
		return
	}
	for i := range reqs {
		if t, err := h.d.Resolve(r.Context(), reqs[i].TargetType, reqs[i].TargetID); err == nil {
			reqs[i].TargetName = t.Name
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, RequestList{Requests: reqs})
}

// answering is a pending request the caller may answer, with the caller and
// the item.
type answering struct {
	user *access.User
	req  *Request
	item Target
}

// decided reads a request the caller may answer: its item's owner or an
// administrator. It writes the refusal and returns nil otherwise.
func (h *handler) decided(w http.ResponseWriter, r *http.Request) *answering {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuth)
		return nil
	}
	req, err := h.d.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return nil
	}
	t, err := h.d.Resolve(r.Context(), req.TargetType, req.TargetID)
	owner := err == nil && (t.OwnerID == user.UserID || (t.OwnerEmail != "" && strings.EqualFold(t.OwnerEmail, user.Email)))
	if !owner && !h.d.IsAdmin(user) {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return nil
	}
	if req.Status != StatusPending {
		httpjson.WriteError(w, http.StatusConflict, "the request is "+req.Status)
		return nil
	}
	return &answering{user: user, req: req, item: t}
}

// approve handles POST /api/v1/portal/access-requests/{id}/approve.
//
// @Summary      Approve an access request
// @Description  Creates a person share of the item for the requester with the permission given, viewer or editor, recorded with origin access_request (#2090), and tells the requester. The item's owner or an administrator.
// @Tags         Access requests
// @Accept       json
// @Produce      json
// @Param        id    path  string         true  "Request id"
// @Param        body  body  DecideRequest  true  "The permission"
// @Success      200  {object}  Request
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/access-requests/{id}/approve [post]
func (h *handler) approve(w http.ResponseWriter, r *http.Request) {
	var in DecideRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Permission != "viewer" && in.Permission != "editor" {
		httpjson.WriteError(w, http.StatusBadRequest, "permission must be viewer or editor")
		return
	}
	a := h.decided(w, r)
	if a == nil {
		return
	}
	shareID, err := h.d.Share(r.Context(), a.req.TargetType, a.req.TargetID, a.user.Email, a.req.RequesterEmail, in.Permission)
	if err != nil {
		fail(w, r, err)
		return
	}
	h.finish(w, r, a, Decision{Status: StatusApproved, Permission: in.Permission, DecidedBy: a.user.Email, ShareID: shareID})
}

// decline handles POST /api/v1/portal/access-requests/{id}/decline.
//
// @Summary      Decline an access request
// @Description  Closes the request without a share and tells the requester (#2090). The item's owner or an administrator.
// @Tags         Access requests
// @Produce      json
// @Param        id  path  string  true  "Request id"
// @Success      200  {object}  Request
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/access-requests/{id}/decline [post]
func (h *handler) decline(w http.ResponseWriter, r *http.Request) {
	a := h.decided(w, r)
	if a == nil {
		return
	}
	h.finish(w, r, a, Decision{Status: StatusDeclined, DecidedBy: a.user.Email})
}

// finish records the answer, tells the requester and writes the request.
func (h *handler) finish(w http.ResponseWriter, r *http.Request, a *answering, d Decision) {
	req := a.req
	if err := h.d.Store.Decide(r.Context(), req.ID, d); err != nil {
		if errors.Is(err, ErrDecided) {
			httpjson.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		fail(w, r, err)
		return
	}
	req.Status, req.Permission, req.DecidedBy, req.ShareID = d.Status, d.Permission, d.DecidedBy, d.ShareID
	if h.d.NotifyRequester != nil {
		h.d.NotifyRequester(r.Context(), req, a.item.Name)
	}
	httpjson.WriteJSON(w, http.StatusOK, req)
}

// readMessage reads the optional body of a request for access, writing 400
// for one that is malformed or too long.
func readMessage(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in OpenRequest
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return "", false
	}
	msg, err := CleanMessage(in.Message)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return msg, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "access requests", "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, "access requests are unavailable")
}
