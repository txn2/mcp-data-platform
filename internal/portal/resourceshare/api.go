//nolint:revive // max-public-structs: one cohesive share API (the request a share is made from, the recipient and the shares a person holds, the service and the dependencies it is built with), not a heap of unrelated types.
package resourceshare

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The routes.
const (
	sharesPath     = "/api/v1/resources/{id}/shares"
	sharedWithPath = "/api/v1/resources/shared-with-me"
)

const (
	maxBody      = 16 << 10
	errNotFound  = "not found"
	errAuth      = "authentication required"
	errForbidden = "only someone who can change this file can share it"
)

// Caller is what the share routes need to know of the person asking, from the
// persona their roles resolve to.
type Caller struct {
	// Persona is the persona they resolve to now: what a share to a persona
	// is matched against.
	Persona string
	// External is a person of an external persona: shares to them are held
	// to viewer, and they share nothing (#2290).
	External bool
	// Sharing is persona.SharingAllowed.
	Sharing bool
}

// Resources reads a resource.
type Resources interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// Shares is the store the routes act through; *Store satisfies it.
type Shares interface {
	Insert(ctx context.Context, share *portaldomain.Share) error
	List(ctx context.Context, resourceID string) ([]portaldomain.Share, error)
	Permission(ctx context.Context, resourceID string, rcpt Recipient) (string, error)
	SharedWith(ctx context.Context, rcpt Recipient) ([]Ref, error)
	Revoke(ctx context.Context, resourceID, shareID string) error
}

// Deps is what the service is built from.
type Deps struct {
	Shares    Shares
	Resources Resources
	// Claims authenticates a request as the resources API does, its level
	// applied and its share lookup bound.
	Claims func(r *http.Request) (*resource.Claims, error)
	// Caller resolves the persona a set of roles resolves to.
	Caller func(roles []string) Caller
	// Notify tells one address a file was shared with them.
	Notify func(ctx context.Context, share *portaldomain.Share, resourceID, title, message string)
	// Members are the addresses of a persona's people.
	Members func(ctx context.Context, persona string) []string
	// PersonaExists reports whether a persona of that name is registered; nil
	// accepts any name.
	PersonaExists func(name string) bool
}

// Service serves resource shares.
type Service struct{ d Deps }

// New returns the service.
func New(d Deps) *Service { return &Service{d: d} }

// Register mounts the routes on mux. They are more specific than the
// resources API's own mount, so they are served here.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET "+sharesPath, s.list)
	mux.HandleFunc("POST "+sharesPath, s.create)
	mux.HandleFunc("DELETE "+sharesPath+"/{shareID}", s.revoke)
	mux.HandleFunc("GET "+sharedWithPath, s.sharedWithMe)
}

// Lookup is resource.Claims.Shared for a caller: the permission a share gives
// them on a resource, held to viewer for a person of an external persona, as
// an asset's is (#2258). A read that fails grants nothing.
func (s *Service) Lookup(ctx context.Context, claims *resource.Claims, c Caller) func(resourceID string) string {
	rcpt := Recipient{UserID: claims.Sub, Email: claims.Email, Persona: c.Persona, Internal: !c.External && c.Persona != ""}
	return func(resourceID string) string {
		perm, err := s.d.Shares.Permission(ctx, resourceID, rcpt)
		if err != nil {
			slog.WarnContext(ctx, "resource share lookup failed", "error", logsan.SanitizeForLog(err.Error()))
			return ""
		}
		if c.External && perm != "" {
			return string(portaldomain.PermissionViewer)
		}
		return perm
	}
}

// ShareRequest is a share to create: a person by address, or a persona.
type ShareRequest struct {
	SharedWithEmail   string `json:"shared_with_email,omitempty" example:"colleague@example.com"`
	SharedWithPersona string `json:"shared_with_persona,omitempty" example:"marketing"`
	// Permission is viewer (the default) or editor. A person of an external
	// persona opens it as viewer whatever it says.
	Permission string `json:"permission,omitempty" example:"viewer"`
	// Message is a plain-text note in the notification.
	Message string `json:"message,omitempty"`
	// Notify is whether the recipient is told; omitted is yes.
	Notify *bool `json:"notify,omitempty"`
}

// ShareList is the shares on a resource.
type ShareList struct {
	Shares []portaldomain.Share `json:"shares"`
}

// SharedResource is one resource shared with the caller.
type SharedResource struct {
	Resource   resource.Resource `json:"resource"`
	ShareID    string            `json:"share_id"`
	SharedBy   string            `json:"shared_by"`
	SharedAt   time.Time         `json:"shared_at"`
	Permission string            `json:"permission"`
	ViaPersona string            `json:"via_persona,omitempty"`
}

// SharedList is the resources shared with the caller.
type SharedList struct {
	Resources []SharedResource `json:"resources"`
}

// request is an authenticated request on one resource.
type request struct {
	claims *resource.Claims
	caller Caller
	res    *resource.Resource
}

// authed authenticates the request and resolves the caller.
func (s *Service) authed(w http.ResponseWriter, r *http.Request) (*resource.Claims, Caller, bool) {
	claims, err := s.d.Claims(r)
	if err != nil || claims == nil {
		status := http.StatusUnauthorized
		if errors.Is(err, resource.ErrForbidden) {
			status = http.StatusForbidden
		}
		httpjson.WriteError(w, status, errAuth)
		return nil, Caller{}, false
	}
	return claims, s.d.Caller(claims.Roles), true
}

// resolve authenticates the request and reads the resource it names, which
// the caller must be able to see: one they cannot is not found.
func (s *Service) resolve(w http.ResponseWriter, r *http.Request) (*request, bool) {
	claims, caller, ok := s.authed(w, r)
	if !ok {
		return nil, false
	}
	res, err := s.d.Resources.Get(r.Context(), r.PathValue("id"))
	if err != nil || res == nil || !resource.CanAccessResource(*claims, res) {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return nil, false
	}
	return &request{claims: claims, caller: caller, res: res}, true
}

// list handles GET /api/v1/resources/{id}/shares.
//
// @Summary      List a file's shares
// @Description  The shares that still grant on a managed resource (#2258): who it is shared with, as viewer or editor. Someone who can change the file.
// @Tags         Resources
// @Produce      json
// @Param        id  path  string  true  "Resource ID"
// @Success      200  {object}  ShareList
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/shares [get]
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	req, ok := s.resolve(w, r)
	if !ok {
		return
	}
	if !resource.CanModifyResource(*req.claims, req.res) {
		httpjson.WriteError(w, http.StatusForbidden, errForbidden)
		return
	}
	shares, err := s.d.Shares.List(r.Context(), req.res.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list shares")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, ShareList{Shares: shares})
}

// create handles POST /api/v1/resources/{id}/shares.
//
// @Summary      Share a file
// @Description  Shares a managed resource with a person by address or with a persona, as viewer or editor (#2258). A viewer reads it, an editor also renames it and writes new versions; neither moves or deletes it. Someone who can change the file and whose persona allows sharing; a person of an external persona shares only their own upload, only with someone who has shared with them. The recipient is told unless notify is false.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        id    path  string        true  "Resource ID"
// @Param        body  body  ShareRequest  true  "Who to share with"
// @Success      201  {object}  portaldomain.Share
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/shares [post]
func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	req, ok := s.resolve(w, r)
	if !ok {
		return
	}
	var in ShareRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.SharedWithPersona != "" && s.d.PersonaExists != nil && !s.d.PersonaExists(in.SharedWithPersona) {
		httpjson.WriteError(w, http.StatusBadRequest, "no persona of that name")
		return
	}
	if status, msg := refusal(req, in); status != 0 {
		httpjson.WriteError(w, status, msg)
		return
	}
	if err := portaldomain.ValidateShareMessage(in.Message); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	share, err := portaldomain.BuildShare(portaldomain.ShareTarget{ResourceID: req.res.ID}, req.claims.Email,
		portaldomain.ShareSpec{RecipientEmail: in.SharedWithEmail, RecipientPersona: in.SharedWithPersona, Permission: in.Permission})
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.d.Shares.Insert(r.Context(), &share); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to create share")
		return
	}
	if in.Notify == nil || *in.Notify {
		s.notify(r.Context(), &share, req.res, in.Message)
	}
	httpjson.WriteJSON(w, http.StatusCreated, share)
}

// LinkShare makes the share a file posted to a channel is opened by: any
// signed-in person of an internal persona, as viewer, recorded as posted to
// where (#2258). It answers the refusal's status and message when the caller
// may not share the file.
func (s *Service) LinkShare(ctx context.Context, claims *resource.Claims, res *resource.Resource, where string) (share *portaldomain.Share, status int, msg string) {
	req := &request{claims: claims, caller: s.d.Caller(claims.Roles), res: res}
	if status, msg = mayShare(req); status != 0 || req.caller.External {
		if status == 0 {
			status, msg = http.StatusForbidden, access.ErrSharingNotAllowed
		}
		return nil, status, msg
	}
	built, err := portaldomain.BuildShare(portaldomain.ShareTarget{ResourceID: res.ID}, claims.Email,
		portaldomain.ShareSpec{AccessMode: "authenticated"})
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	built.Origin, built.PostedTo = portaldomain.OriginChannelPost, where
	if err := s.d.Shares.Insert(ctx, &built); err != nil {
		return nil, http.StatusInternalServerError, "failed to create share"
	}
	return &built, 0, ""
}

// Withdraw revokes a link share whose post did not go out.
func (s *Service) Withdraw(ctx context.Context, share *portaldomain.Share) {
	if err := s.d.Shares.Revoke(ctx, share.ResourceID, share.ID); err != nil {
		slog.WarnContext(ctx, "withdrawing a file's channel share failed", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// mayShare is whether the caller may share the file at all, as a status and
// message, or 0: an administrator, or someone who can change it and whose
// persona allows sharing. An external person is decided by externalRefusal.
func mayShare(req *request) (status int, msg string) {
	switch {
	case req.claims.IsAdmin:
		return 0, ""
	case !resource.CanModifyResource(*req.claims, req.res):
		return http.StatusForbidden, errForbidden
	case !req.caller.Sharing:
		return http.StatusForbidden, access.ErrSharingNotAllowed
	}
	return 0, ""
}

// refusal is why a share may not be made, as a status and message, or 0.
// Anyone who may share names any person or persona (#2290).
func refusal(req *request, in ShareRequest) (status int, msg string) {
	if in.SharedWithEmail == "" && in.SharedWithPersona == "" {
		return http.StatusBadRequest, "name a person or a persona to share with"
	}
	return mayShare(req)
}

// revoke handles DELETE /api/v1/resources/{id}/shares/{shareID}.
//
// @Summary      Stop sharing a file
// @Description  Revokes one share of a managed resource (#2258). Someone who can change the file.
// @Tags         Resources
// @Param        id       path  string  true  "Resource ID"
// @Param        shareID  path  string  true  "Share ID"
// @Success      204
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/shares/{shareID} [delete]
func (s *Service) revoke(w http.ResponseWriter, r *http.Request) {
	req, ok := s.resolve(w, r)
	if !ok {
		return
	}
	if !resource.CanModifyResource(*req.claims, req.res) {
		httpjson.WriteError(w, http.StatusForbidden, errForbidden)
		return
	}
	if err := s.d.Shares.Revoke(r.Context(), req.res.ID, r.PathValue("shareID")); err != nil {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sharedWithMe handles GET /api/v1/resources/shared-with-me.
//
// @Summary      Files shared with me
// @Description  The managed resources shared with the caller, directly or through their persona, each with the share that grants the most (#2258).
// @Tags         Resources
// @Produce      json
// @Success      200  {object}  SharedList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/shared-with-me [get]
func (s *Service) sharedWithMe(w http.ResponseWriter, r *http.Request) {
	claims, caller, ok := s.authed(w, r)
	if !ok {
		return
	}
	refs, err := s.d.Shares.SharedWith(r.Context(), Recipient{UserID: claims.Sub, Email: claims.Email, Persona: caller.Persona})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list shared files")
		return
	}
	out := SharedList{Resources: []SharedResource{}}
	for _, ref := range refs {
		res, err := s.d.Resources.Get(r.Context(), ref.ResourceID)
		if err != nil || res == nil {
			continue
		}
		perm := ref.Permission
		if caller.External {
			perm = string(portaldomain.PermissionViewer)
		}
		out.Resources = append(out.Resources, SharedResource{
			Resource: *res, ShareID: ref.ShareID, SharedBy: ref.SharedBy, SharedAt: ref.SharedAt,
			Permission: perm, ViaPersona: ref.ViaPersona,
		})
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// notify tells a share's recipients: the person it names, or each person of
// the persona it names.
func (s *Service) notify(ctx context.Context, share *portaldomain.Share, res *resource.Resource, message string) {
	if s.d.Notify == nil {
		return
	}
	if share.SharedWithPersona == "" {
		s.d.Notify(ctx, share, res.ID, res.DisplayName, message)
		return
	}
	if s.d.Members == nil {
		return
	}
	for _, email := range s.d.Members(ctx, share.SharedWithPersona) {
		addressed := *share
		addressed.SharedWithEmail = email
		s.d.Notify(ctx, &addressed, res.ID, res.DisplayName, message)
	}
}
