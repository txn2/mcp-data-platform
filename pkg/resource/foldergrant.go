package resource

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/grantsource"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Write grants on a Global folder (#2684): an administrator grants a person,
// an address or a persona write on one folder of the Global library, and the
// grant covers that folder and everything under it (PathUnder), at any depth,
// including folders made later. A grantee uploads, makes folders, writes
// versions, renames, moves within what they can write, deletes and shares
// inside the subtree; the granted folder itself is renamed, moved and deleted
// by administrators only, since that changes what the grant covers.
//
// Grants are stored as folder shares (portal_shares.folder_path) and read once
// per request through the source internal/grantsource holds, so a revoked
// grant or a persona a person no longer resolves to is refused on their next
// request, on every replica.

// parentFolder is the folder holding path, "" for a top-level folder.
func parentFolder(path string) string {
	before, _, found := strings.CutLast(path, "/")
	if !found {
		return ""
	}
	return before
}

// WithFolderGrants returns the claims with the caller's folder grants read
// lazily, once, the first time a check asks, from the installed source. A
// caller acting for a person (a managed-script run) holds that person's
// grants, never the principal's. A read that fails grants nothing.
func (c Claims) WithFolderGrants(ctx context.Context) Claims {
	src := grantsource.Installed()
	if src == nil {
		return c
	}
	g := grantsource.Grantee{Subject: c.Sub, Email: c.Email, Personas: c.Personas}
	if c.OnBehalfOf != "" {
		g.Subject, g.Email = c.OnBehalfOfSub, c.OnBehalfOf
	}
	var (
		once    sync.Once
		folders []string
	)
	read := func() []string {
		once.Do(func() {
			got, err := src.WritableFolders(ctx, g)
			if err != nil {
				slog.WarnContext(ctx, "resources: reading folder grants failed", "error", logsan.SanitizeForLog(err.Error()))
				return
			}
			folders = got
		})
		return folders
	}
	c.grantedFolders = read
	c.folderWrite = func(path string) bool {
		for _, f := range read() {
			if f != "" && PathUnder(path, f) {
				return true
			}
		}
		return false
	}
	return c
}

// granted reports whether a folder grant covers path in the library: only the
// Global library takes grants, and a person kept from Global reaches none.
func granted(c Claims, scope Scope, path string) bool {
	return scope == ScopeGlobal && !c.NoGlobal && c.folderWrite != nil && c.folderWrite(path)
}

// CanWriteFolder reports whether the caller may add to the folder at path in
// the library: everything CanWriteScope allows, and a folder a write grant
// covers (#2684). The upload route, a new folder's parent, manage_resource and
// a script's export all ask it.
func (c Claims) CanWriteFolder(scope Scope, scopeID, path string) bool {
	return CanWriteScope(c, scope, scopeID) || granted(c, scope, path)
}

// GrantedFolders is the Global folders the caller holds a write grant on,
// [] for none or a person kept from Global.
func (c Claims) GrantedFolders() []string {
	if c.NoGlobal || c.grantedFolders == nil {
		return []string{}
	}
	return append([]string{}, c.grantedFolders()...)
}

// myGrants is the Global folders the caller can write through a grant.
type myGrants struct {
	Folders []string `json:"folders" example:"reports"`
}

// handleMyGrants handles GET /api/v1/resources/folder-grants/mine: the Global
// folders a write grant lets the caller write, which the portal shows Upload,
// New folder, Rename, Move and Delete inside and marks "You can edit". It
// names no other grantee.
//
// @Summary      List the Global folders you can write
// @Description  Returns the Global folders a write grant lets the caller write (#2684), [] for none. Names no other grantee.
// @Tags         Resources
// @Produce      json
// @Success      200  {object}  myGrants
// @Failure      401  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/folder-grants/mine [get]
func (h *Handler) handleMyGrants(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, myGrants{Folders: claims.GrantedFolders()})
}
