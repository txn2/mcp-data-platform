package resource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// FolderDescriber keeps the description a folder's search entry carries
// (#2111): what the files in it are, written once for the folder rather than
// on every file. The Postgres store implements it.
type FolderDescriber interface {
	FolderDescription(ctx context.Context, lib ScopeFilter, path string) (string, error)
	DescribeFolder(ctx context.Context, lib ScopeFilter, path, description string) error
}

// DescribeFolder sets the description of a folder the caller may write into.
// An empty description clears it. A folder the library does not hold is
// ErrFolderEmpty.
func DescribeFolder(ctx context.Context, deps Deps, claims *Claims, f NewFolder, description string) error {
	describer, ok := deps.Folders.(FolderDescriber)
	if !ok {
		return ErrFoldersUnsupported
	}
	if err := validateFolderTarget(f.Library, f.Path); err != nil {
		return err
	}
	description = strings.TrimSpace(description)
	if len(description) > MaxDescriptionLen {
		return &invalidPathError{msg: fmt.Sprintf("description exceeds %d characters", MaxDescriptionLen)}
	}
	if !CanWriteScope(*claims, f.Library.Scope, f.Library.ScopeID) {
		return ErrMoveForbidden
	}
	if err := describer.DescribeFolder(ctx, f.Library, f.Path, description); err != nil {
		return fmt.Errorf("describing folder: %w", err)
	}
	return nil
}

// folderDescription reads a folder's description, for a caller who may read
// the library.
func folderDescription(ctx context.Context, deps Deps, claims *Claims, f NewFolder) (string, error) {
	describer, ok := deps.Folders.(FolderDescriber)
	if !ok {
		return "", ErrFoldersUnsupported
	}
	if err := validateFolderTarget(f.Library, f.Path); err != nil {
		return "", err
	}
	if !CanSeeLibrary(*claims, f.Library) {
		return "", ErrFolderEmpty
	}
	desc, err := describer.FolderDescription(ctx, f.Library, f.Path)
	if err != nil {
		return "", fmt.Errorf("reading folder description: %w", err)
	}
	return desc, nil
}

// FolderDescription reads the description stored on a folder's row.
func (s *postgresStore) FolderDescription(ctx context.Context, lib ScopeFilter, path string) (string, error) { //nolint:revive // interface impl
	var desc string
	err := s.db.QueryRowContext(ctx, `SELECT description FROM resource_folders
		WHERE scope = $1 AND COALESCE(scope_id, '') = $2 AND path = $3`,
		string(lib.Scope), lib.ScopeID, path).Scan(&desc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrFolderEmpty
	}
	if err != nil {
		return "", fmt.Errorf("reading folder description: %w", err)
	}
	return desc, nil
}

// DescribeFolder writes a folder's description and hands its search entry to
// the index worker, which re-embeds it when the summary changed. The summary
// the entry was embedded from is emptied with it: a job already running for
// the folder read the old description, the queue keeps no second job for a
// folder while one runs, and an empty summary is what the reconciler reads as
// an entry still owed.
func (s *postgresStore) DescribeFolder(ctx context.Context, lib ScopeFilter, path, description string) error { //nolint:revive // interface impl
	res, err := s.db.ExecContext(ctx, `UPDATE resource_folders SET description = $4, summary_text = ''
		WHERE scope = $1 AND COALESCE(scope_id, '') = $2 AND path = $3`,
		string(lib.Scope), lib.ScopeID, path, description)
	if err != nil {
		return fmt.Errorf("describing folder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFolderEmpty
	}
	s.folderIndex.NotifyWrite(ctx, FolderKey(lib, path))
	return nil
}

// folderDescriptionRequest names a folder and what it holds.
type folderDescriptionRequest struct {
	Scope       Scope  `json:"scope" example:"global"`
	ScopeID     string `json:"scope_id,omitempty" example:""`
	Path        string `json:"path" example:"pipelines/nltv"`
	Description string `json:"description" example:"NLTV station daily reach and frequency archive, one CSV per station day since 2019"`
}

// handleFolderDescribe handles PUT /api/v1/resources/folders/description.
//
// @Summary      Describe a folder
// @Description  Sets what a folder holds, in a sentence. A folder holding files that are not indexed one by one is found by search as one entry, and the description is part of what that entry is matched on, with the folder's path, how many files it holds and of which types, and a few of their names. An empty description clears it. Requires the authority to add files to the library.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        body body  resource.folderDescriptionRequest  true  "The folder and its description"
// @Success      200  {object}  resource.folderDescriptionRequest
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/folders/description [put]
func (h *Handler) handleFolderDescribe(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var req folderDescriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	f := NewFolder{Library: ScopeFilter{Scope: req.Scope, ScopeID: req.ScopeID}, Path: req.Path}
	if err := DescribeFolder(r.Context(), h.deps, claims, f, req.Description); err != nil {
		writeFolderError(w, err, "describing folder")
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	writeJSON(w, http.StatusOK, req)
}

// handleFolderDescription handles GET /api/v1/resources/folders/description.
//
// @Summary      Read a folder's description
// @Description  Returns what a folder holds, as its description says. Empty when none was written.
// @Tags         Resources
// @Produce      json
// @Param        scope     query  string  true   "Library scope"  Enums(global, persona, user)
// @Param        scope_id  query  string  false  "Persona name or user for a persona or user library"
// @Param        path      query  string  true   "Folder path"
// @Success      200  {object}  resource.folderDescriptionRequest
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/folders/description [get]
func (h *Handler) handleFolderDescription(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := NewFolder{Library: ScopeFilter{Scope: Scope(q.Get("scope")), ScopeID: q.Get("scope_id")}, Path: q.Get("path")}
	desc, err := folderDescription(r.Context(), h.deps, claims, f)
	if err != nil {
		writeFolderError(w, err, "reading folder description")
		return
	}
	writeJSON(w, http.StatusOK, folderDescriptionRequest{
		Scope: f.Library.Scope, ScopeID: f.Library.ScopeID, Path: f.Path, Description: desc,
	})
}
