package flowhttp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttiles"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// TileReader reads one variant of a script's tile (#1909), or
// scripttiles.ErrNoTile, and whether it was drawn from the version the script
// is at.
type TileReader interface {
	Tile(ctx context.Context, scriptID, variant string) (data []byte, current bool, err error)
}

// tileMaxAge is how long a browser may keep a tile drawn from the version the
// script is at. The listing addresses a tile by that version, so a new version
// is a new address. Until the worker draws it, the address answers the older
// version's tile, which is revalidated rather than kept, so the new diagram
// shows once it is drawn.
const tileMaxAge = time.Hour

// scriptTile serves a script's tile: its flow diagram, drawn by the tile
// worker.
//
// @Summary      Get a script's tile
// @Description  Returns a script's tile as a PNG: its flow diagram, drawn from the latest version that parses, light or dark. 404 when the script has no tile yet, or its latest version does not parse, which the listing shows as a placeholder. Readable by everyone signed in, as the script is.
// @Tags         Scripts
// @Produce      png
// @Param        id       path   string  true   "Script ID"
// @Param        variant  query  string  false  "light (default) or dark"
// @Success      200
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/thumbnail [get]
func (h *Handler) scriptTile(w http.ResponseWriter, r *http.Request) {
	if h.deps.SignedIn == nil || !h.deps.SignedIn(r) {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	variant := scripttiles.VariantLight
	if r.URL.Query().Get("variant") == scripttiles.VariantDark {
		variant = scripttiles.VariantDark
	}
	id := r.PathValue("id")
	data, current, err := h.deps.Tiles.Tile(r.Context(), id, variant)
	if errors.Is(err, scripttiles.ErrNoTile) {
		httpjson.WriteError(w, http.StatusNotFound, "this script has no tile")
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the script's tile")
		return
	}
	if current {
		blobserve.CachePrivate(w, tileMaxAge)
	}
	blobserve.Serve(w, r, blobserve.Options{Name: id + ".png", ContentType: "image/png", Data: data, Revalidate: !current})
}
