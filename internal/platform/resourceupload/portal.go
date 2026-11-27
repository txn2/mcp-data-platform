package resourceupload

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The portal's upload links (#2844): a person makes a link for a folder on the
// Resources page to give an external service, sees the links they made that
// can still take a file, and revokes one. The links are the ones
// manage_resource action=upload_link makes, through the same Issue.

// Portal routes.
const (
	PortalRoute  = "/api/v1/resources/upload-links"
	revokeSuffix = "/revoke"
)

// maxIssueBody bounds a link request's body.
const maxIssueBody = 16 << 10

// issueBody is a link as the portal asks for it.
type issueBody struct {
	Scope       string `json:"scope" example:"user"`
	ScopeID     string `json:"scope_id,omitempty"`
	Path        string `json:"path" example:"renders"`
	Filename    string `json:"filename,omitempty" example:"report.pdf"`
	ContentType string `json:"content_type" example:"image/png"`
	SizeBytes   int64  `json:"size_bytes" example:"20971520"`
	MaxFiles    int    `json:"max_files,omitempty" example:"4"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty" example:"7200"`
	IfExists    string `json:"if_exists,omitempty" example:"fail"`
}

// openList is the caller's open links.
type openList struct {
	Links []Open `json:"links"`
}

// MountPortal registers the portal's link routes on mux, each reading the
// caller's resource claims with extract, as the resource routes do.
func (s *Service) MountPortal(mux *http.ServeMux, extract resource.ClaimsExtractor) {
	if s == nil || extract == nil {
		return
	}
	p := portal{s: s, extract: extract}
	mux.HandleFunc("GET "+PortalRoute, p.list)
	mux.HandleFunc("POST "+PortalRoute, p.issue)
	mux.HandleFunc("POST "+PortalRoute+"/{id}"+revokeSuffix, p.revoke)
}

// MountPortalFrom registers the portal's link routes on mux from the toolkit
// among kits that issues links.
func MountPortalFrom[T any](mux *http.ServeMux, kits []T, extract resource.ClaimsExtractor) {
	for _, k := range kits {
		if m, ok := any(k).(interface {
			MountUploadLinkPortal(mux *http.ServeMux, extract resource.ClaimsExtractor)
		}); ok {
			m.MountUploadLinkPortal(mux, extract)
		}
	}
}

type portal struct {
	s       *Service
	extract resource.ClaimsExtractor
}

// claims reads the caller, answering the refusal when it cannot.
func (p portal) claims(w http.ResponseWriter, r *http.Request) (resource.Claims, bool) {
	c, err := p.extract(r)
	if err == nil && c != nil {
		return *c, true
	}
	var refused interface{ Refusal() string }
	switch {
	case errors.As(err, &refused):
		httpjson.WriteError(w, http.StatusForbidden, refused.Refusal())
	case errors.Is(err, resource.ErrForbidden):
		httpjson.WriteError(w, http.StatusForbidden, "invalid or missing CSRF token")
	default:
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
	}
	return resource.Claims{}, false
}

// list handles GET /api/v1/resources/upload-links.
//
// @Summary      List my open upload links
// @Description  The upload links the caller made (from the portal or manage_resource action=upload_link, #2844) that can still take a file: not expired, not revoked and not full. A link's token is never listed.
// @Tags         Resources
// @Produce      json
// @Success      200  {object}  resourceupload.openList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/upload-links [get]
func (p portal) list(w http.ResponseWriter, r *http.Request) {
	claims, ok := p.claims(w, r)
	if !ok {
		return
	}
	links, err := p.s.Opened(r.Context(), claims)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "the upload links could not be read")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, openList{Links: links})
}

// issue handles POST /api/v1/resources/upload-links.
//
// @Summary      Make an upload link
// @Description  Makes a link an external service PUTs files to (#2844), filed in the folder named as the caller, with the checks a portal upload gets. Without a filename each file is PUT to upload_url/<filename>; max_files (1 to 100) is how many files it takes, size_bytes the most each may be, ttl_seconds how long it lives (default 900, at most 86400). The link is shown once.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        body  body      resourceupload.issueBody  true  "The link"
// @Success      201   {object}  resourceupload.Issued
// @Failure      400   {object}  httpjson.ProblemDetail
// @Failure      401   {object}  httpjson.ProblemDetail
// @Failure      403   {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/upload-links [post]
func (p portal) issue(w http.ResponseWriter, r *http.Request) {
	claims, ok := p.claims(w, r)
	if !ok {
		return
	}
	var in issueBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIssueBody)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body must be a JSON object naming the link")
		return
	}
	scope, scopeID := resource.ResolveScopeFor(resource.Scope(strings.TrimSpace(in.Scope)), strings.TrimSpace(in.ScopeID), claims)
	issued, err := p.s.Issue(r.Context(), IssueRequest{
		Scope: scope, ScopeID: scopeID, Path: in.Path, Filename: strings.TrimSpace(in.Filename),
		ContentType: strings.TrimSpace(in.ContentType), IfExists: strings.TrimSpace(in.IfExists),
		MaxBytes: in.SizeBytes, MaxFiles: in.MaxFiles, TTL: time.Duration(in.TTLSeconds) * time.Second,
	}, claims, claims.Sub)
	if errors.Is(err, ErrRefused) {
		httpjson.WriteError(w, http.StatusBadRequest, strings.TrimSuffix(err.Error(), ": "+ErrRefused.Error()))
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "the upload link could not be made")
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, issued)
}

// revoke handles POST /api/v1/resources/upload-links/{id}/revoke.
//
// @Summary      Revoke an upload link
// @Description  Ends an upload link before it expires (#2844): a PUT to it answers 404 from then on. The person who made it, or an administrator, may.
// @Tags         Resources
// @Param        id  path  string  true  "The link's id"
// @Success      204
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/upload-links/{id}/revoke [post]
func (p portal) revoke(w http.ResponseWriter, r *http.Request) {
	claims, ok := p.claims(w, r)
	if !ok {
		return
	}
	err := p.s.Revoke(r.Context(), r.PathValue("id"), claims)
	switch {
	case errors.Is(err, ErrNoLink):
		httpjson.WriteError(w, http.StatusNotFound, "no open upload link of yours has that id")
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "the upload link could not be revoked")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
