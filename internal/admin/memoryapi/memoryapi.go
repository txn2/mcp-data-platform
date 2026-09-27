// Package memoryapi serves /api/v1/admin/memory/records: every memory
// record on the deployment, whoever wrote it (#1926).
//
// The portal's /api/v1/portal/memory/records lists only the caller's own
// records, so without this route an administrator could not see, count or
// audit the memory on their deployment, while insights and changesets each
// had an admin list. It takes the portal route's filters and its
// limit/offset paging, plus created_by to narrow to one author.
//
// It is a decomposition seam of pkg/admin (which sits at its package size
// budget); the parent registers it on the admin mux, so every route here is
// already behind the admin persona gate.
package memoryapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// Lister reads memory records by filter. Satisfied by memory.Store.
type Lister interface {
	List(ctx context.Context, filter memory.Filter) ([]memory.Record, int, error)
}

// Config carries what the route needs. A nil Records leaves it
// unregistered: a deployment without a memory store has nothing to list.
type Config struct {
	Records Lister
}

// recordListResponse is one page of memory records, paged the way the
// portal's own list is: by limit and offset.
type recordListResponse struct {
	Data   []memory.Record `json:"data"`
	Total  int             `json:"total" example:"212"`
	Limit  int             `json:"limit" example:"20"`
	Offset int             `json:"offset" example:"0"`
}

type handler struct {
	cfg Config
}

// Register mounts the admin memory route on mux. It is read-only.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Records == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/memory/records", h.listRecords)
}

// parseFilter reads the portal list's vocabulary plus created_by. An
// unparseable limit or offset is treated as absent, as the portal route
// treats it.
func parseFilter(r *http.Request) memory.Filter {
	q := r.URL.Query()
	return memory.Filter{
		CreatedBy: q.Get("created_by"),
		Dimension: q.Get("dimension"),
		SinkClass: q.Get("sink_class"),
		Category:  q.Get("category"),
		Status:    q.Get("status"),
		Source:    q.Get("source"),
		Limit:     intParam(q.Get("limit"), memory.DefaultLimit),
		Offset:    max(intParam(q.Get("offset"), 0), 0),
	}
}

func intParam(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

// listRecords handles GET /api/v1/admin/memory/records.
//
// @Summary      List every memory record
// @Description  Returns paginated memory records written by any user, newest first, with the portal memory list's filters plus created_by to narrow to one author.
// @Tags         Memory
// @Produce      json
// @Param        created_by  query  string   false  "Filter by author email"
// @Param        dimension   query  string   false  "Filter by dimension"
// @Param        sink_class  query  string   false  "Filter by sink class"
// @Param        category    query  string   false  "Filter by category"
// @Param        status      query  string   false  "Filter by status"
// @Param        source      query  string   false  "Filter by source"
// @Param        limit       query  integer  false  "Results per page (default: 20, max: 100)"
// @Param        offset      query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  recordListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/memory/records [get]
func (h *handler) listRecords(w http.ResponseWriter, r *http.Request) {
	filter := parseFilter(r)
	records, total, err := h.cfg.Records.List(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list memory records")
		return
	}
	if records == nil {
		records = []memory.Record{}
	}
	httpjson.WriteJSON(w, http.StatusOK, recordListResponse{
		Data:   records,
		Total:  total,
		Limit:  filter.EffectiveLimit(),
		Offset: filter.Offset,
	})
}
