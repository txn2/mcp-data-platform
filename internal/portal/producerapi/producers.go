package producerapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// producerView is one producer of one file, as a viewer lists it.
type producerView struct {
	// Kind is "script", "session" or "person"; ID is the stable identity the
	// surface links to.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Label is what to call the producer: a script's current name, falling back
	// to the name it had when it wrote if it has since been deleted, and a
	// person's address. Empty for a session, whose id is its own label.
	Label string `json:"label,omitempty"`
	// Exists reports whether the producer can still be opened. It is false only
	// where the platform positively determined the producer is gone -- a script
	// id that resolves to nothing -- so a surface can say "a script that no
	// longer exists" rather than link to a page that 404s.
	Exists bool `json:"exists"`
	// Created marks the producer that brought the file into existence, as
	// against one that has only changed it since.
	Created      bool      `json:"created"`
	FirstWriteAt time.Time `json:"first_write_at"`
	LastWriteAt  time.Time `json:"last_write_at"`
	WriteCount   int       `json:"write_count"`
	// LastVersion is the file version this producer last wrote, or zero for a
	// file whose kind does not number its writes.
	LastVersion int `json:"last_version"`
}

// producersResponse lists what has written one file, most recent writer first.
type producersResponse struct {
	Data  []producerView `json:"data"`
	Total int            `json:"total"`
}

// assetProducers lists what has written this asset.
//
// It answers a different question from the asset's provenance, which stays
// exactly as it was: provenance records the data calls the CONTENT was built
// from, and this records who did the building.
//
// @Summary      List what produced an asset
// @Description  Returns the scripts, sessions and people that created or modified this asset, most recent writer first, marking which one created it and whether each producer still exists.
// @Tags         Portal
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  producersResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      410  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/producers [get]
func (h *handler) assetProducers(w http.ResponseWriter, r *http.Request) {
	user := caller(w, r)
	if user == nil {
		return
	}
	asset, ok := h.viewableAsset(w, r, user)
	if !ok {
		return
	}
	h.writeProducers(w, r, producedby.TargetAsset, asset.ID)
}

// resourceProducers lists what has written this managed resource.
//
// @Summary      List what produced a managed resource
// @Description  Returns the scripts, sessions and people that created or modified this resource, most recent writer first, marking which one created it and whether each producer still exists.
// @Tags         Resources
// @Produce      json
// @Param        id  path  string  true  "Resource ID"
// @Success      200  {object}  producersResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/resources/{id}/producers [get]
func (h *handler) resourceProducers(w http.ResponseWriter, r *http.Request) {
	user := caller(w, r)
	if user == nil {
		return
	}
	res, ok := h.readableResource(w, r, user)
	if !ok {
		return
	}
	h.writeProducers(w, r, producedby.TargetResource, res.ID)
}

// writeProducers answers both routes. The caller has already established that
// this reader may open the file; what wrote it carries no further permission of
// its own.
func (h *handler) writeProducers(w http.ResponseWriter, r *http.Request, targetKind, targetID string) {
	rows, err := h.cfg.Producers.ListByTarget(r.Context(), targetKind, targetID)
	if err != nil {
		slog.Error("producers: listing what wrote this file failed",
			logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list what produced this")
		return
	}
	views := h.views(r.Context(), rows)
	httpjson.WriteJSON(w, http.StatusOK, producersResponse{Data: views, Total: len(views)})
}

// views renders the rows, resolving each script producer against the scripts
// that exist now.
func (h *handler) views(ctx context.Context, rows []producedby.Row) []producerView {
	live := h.liveScripts(ctx, rows)
	views := make([]producerView, 0, len(rows))
	for _, row := range rows {
		v := producerView{
			Kind: row.Producer.Kind, ID: row.Producer.ID, Label: row.Producer.Label,
			Exists: true, Created: row.Created,
			FirstWriteAt: row.FirstWriteAt, LastWriteAt: row.LastWriteAt,
			WriteCount: row.WriteCount, LastVersion: row.LastVersion,
		}
		if row.Producer.Kind == producedby.KindScript && live != nil {
			name, still := live[row.Producer.ID]
			v.Exists = still
			// The recorded label is kept when the script is gone: it is the
			// only remaining answer to which script that was.
			if still && name != "" {
				v.Label = name
			}
		}
		views = append(views, v)
	}
	return views
}

// liveScripts resolves the script producers among rows to the names they carry
// now, or nil when this deployment cannot resolve them -- in which case every
// producer is reported as existing, which is what a surface with no lookup can
// honestly say.
func (h *handler) liveScripts(ctx context.Context, rows []producedby.Row) map[string]string {
	if h.cfg.Scripts == nil {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Producer.Kind == producedby.KindScript {
			ids = append(ids, row.Producer.ID)
		}
	}
	if len(ids) == 0 {
		return map[string]string{}
	}
	names, err := h.cfg.Scripts.Names(ctx, ids)
	if err != nil {
		slog.Warn("producers: resolving script producers failed; reporting them as still existing",
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return names
}
