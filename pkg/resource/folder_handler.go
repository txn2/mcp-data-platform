package resource

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// folderMoveRequest names a folder in one library and where it is going.
//
// The library is on the request rather than derived from the folder, because a
// path is only unique inside one: "data/weekly" names a different folder in
// every library that has one, and a rename that guessed which would refile
// somebody else's files.
type folderMoveRequest struct {
	Scope   Scope  `json:"scope" example:"user"`
	ScopeID string `json:"scope_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	From    string `json:"from" example:"data/media-manager"`
	To      string `json:"to" example:"data/shows"`
}

// handleFolderMove handles POST /api/v1/resources/folders/move.
//
// @Summary      Move or rename a folder
// @Description  Rewrites the folder path of every resource beneath one path prefix in one library, in a single transaction. Each resource's previous URI is recorded as an alias, so citations of the old addresses keep resolving. Refused whole on any conflict; nothing is moved.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        body body  resource.folderMoveRequest  true  "The folder and its destination"
// @Success      200  {object}  resource.FolderMove
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      409  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/folders/move [post]
func (h *Handler) handleFolderMove(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var req folderMoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := MoveFolder(r.Context(), h.deps, claims, FolderRename{
		Library: ScopeFilter{Scope: req.Scope, ScopeID: req.ScopeID},
		From:    req.From,
		To:      req.To,
	})
	if err != nil {
		writeFolderMoveError(w, err)
		return
	}

	// The MCP registry is keyed on the URI, so every resource that moved has to
	// be withdrawn from the address it left and registered at the one it took,
	// or a client keeps listing a folder that no longer exists.
	for _, m := range result.Moved {
		h.notifyDelete(m.FromURI)
	}
	h.refreshMoved(r, result)
	writeJSON(w, http.StatusOK, result)
}

// refreshMoved re-registers every resource a folder move rewrote, reading each
// one back so the registration carries the row as stored rather than the plan
// that produced it.
func (h *Handler) refreshMoved(r *http.Request, result *FolderMove) {
	if h.deps.OnCreate == nil {
		return
	}
	for _, m := range result.Moved {
		res, err := h.deps.Store.Get(r.Context(), m.ID)
		if err != nil || res == nil {
			slog.Warn("folder move: re-registering moved resource failed", msgError, err,
				"resource_id", m.ID) // #nosec G706 -- server-generated ID
			continue
		}
		h.notifyCreate(res)
	}
}

// writeFolderMoveError maps a refused folder move onto the status that says why.
//
// Every refusal below left the library exactly as it was, which is what lets
// them share one shape: the person is told what stopped the move and can act on
// it without first establishing whether some of their files already moved.
func writeFolderMoveError(w http.ResponseWriter, err error) {
	switch {
	case IsInvalidScope(err), IsInvalidPath(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrFolderEmpty):
		writeError(w, http.StatusNotFound, err.Error())
	// Checked before the refusal cases below: a subtree holding one file the
	// caller may not change is a question of authority, not of addresses.
	case errors.Is(err, ErrMoveForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case IsFolderMoveRefused(err):
		writeError(w, http.StatusConflict, err.Error())
	default:
		slog.Error("resource folder move failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "moving folder")
	}
}
