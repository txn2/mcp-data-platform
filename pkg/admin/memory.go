package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// MemoryHandler provides admin REST endpoints for memory record management.
type MemoryHandler struct {
	store memory.Store
}

// NewMemoryHandler creates a new memory admin handler.
func NewMemoryHandler(store memory.Store) *MemoryHandler {
	return &MemoryHandler{store: store}
}

// memoryListResponse wraps a paginated list of memory records.
type memoryListResponse struct {
	Data    []memory.Record `json:"data"`
	Total   int             `json:"total" example:"20"`
	Page    int             `json:"page" example:"1"`
	PerPage int             `json:"per_page" example:"20"`
}

// memoryStatsResponse contains aggregated statistics for memory records.
type memoryStatsResponse struct {
	ByDimension map[string]int `json:"by_dimension"`
	ByCategory  map[string]int `json:"by_category"`
	ByStatus    map[string]int `json:"by_status"`
	Total       int            `json:"total" example:"20"`
}

// memoryUpdateRequest represents the body of PUT /memory/records/{id}.
type memoryUpdateRequest struct {
	Content    string         `json:"content,omitempty" example:"The daily_sales table in the retail schema is partitioned by date."`
	Category   string         `json:"category,omitempty" example:"business_context"`
	Confidence string         `json:"confidence,omitempty" example:"high"`
	Dimension  string         `json:"dimension,omitempty" example:"knowledge"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// ListRecords handles GET /api/v1/admin/memory/records.
//
// @Summary      List memory records
// @Description  Returns paginated memory records with optional filtering.
// @Tags         Memory
// @Produce      json
// @Param        persona      query  string  false  "Filter by persona"
// @Param        dimension    query  string  false  "Filter by dimension"
// @Param        category     query  string  false  "Filter by category"
// @Param        status       query  string  false  "Filter by status"
// @Param        source       query  string  false  "Filter by source"
// @Param        entity_urn   query  string  false  "Filter by entity URN"
// @Param        created_by   query  string  false  "Filter by creator"
// @Param        since        query  string  false  "Records after this time (RFC 3339)"
// @Param        until        query  string  false  "Records before this time (RFC 3339)"
// @Param        page         query  integer false  "Page number, 1-based (default: 1)"
// @Param        per_page     query  integer false  "Results per page (default: 20)"
// @Success      200  {object}  memoryListResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/memory/records [get]
func (h *MemoryHandler) ListRecords(w http.ResponseWriter, r *http.Request) {
	filter := parseMemoryFilter(r)
	records, total, err := h.store.List(r.Context(), filter)
	if err != nil {
		slog.Error("failed to list memory records", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list memory records")
		return
	}

	if records == nil {
		records = []memory.Record{}
	}

	writeJSON(w, http.StatusOK, memoryListResponse{
		Data:    records,
		Total:   total,
		Page:    filter.Offset/filter.EffectiveLimit() + 1,
		PerPage: filter.EffectiveLimit(),
	})
}

// GetStats handles GET /api/v1/admin/memory/records/stats.
//
// @Summary      Get memory record stats
// @Description  Returns aggregated memory record statistics by dimension, category, and status.
// @Tags         Memory
// @Produce      json
// @Param        persona      query  string  false  "Filter by persona"
// @Param        dimension    query  string  false  "Filter by dimension"
// @Param        category     query  string  false  "Filter by category"
// @Param        status       query  string  false  "Filter by status"
// @Param        source       query  string  false  "Filter by source"
// @Param        entity_urn   query  string  false  "Filter by entity URN"
// @Param        created_by   query  string  false  "Filter by creator"
// @Param        since        query  string  false  "Records after this time (RFC 3339)"
// @Param        until        query  string  false  "Records before this time (RFC 3339)"
// @Success      200  {object}  memoryStatsResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/memory/records/stats [get]
func (h *MemoryHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	// Fetch all matching records across all pages to compute accurate stats.
	filter := parseMemoryFilter(r)
	filter.Limit = memory.MaxLimit
	filter.Offset = 0

	stats := memoryStatsResponse{
		ByDimension: map[string]int{},
		ByCategory:  map[string]int{},
		ByStatus:    map[string]int{},
	}

	for {
		records, total, err := h.store.List(r.Context(), filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to compute memory stats")
			return
		}
		if stats.Total == 0 {
			stats.Total = total
		}
		for i := range records {
			stats.ByDimension[records[i].Dimension]++
			stats.ByCategory[records[i].Category]++
			stats.ByStatus[records[i].Status]++
		}
		if len(records) < memory.MaxLimit {
			break
		}
		filter.Offset += memory.MaxLimit
	}

	writeJSON(w, http.StatusOK, stats)
}

// GetRecord handles GET /api/v1/admin/memory/records/{id}.
//
// @Summary      Get memory record
// @Description  Returns a single memory record by ID.
// @Tags         Memory
// @Produce      json
// @Param        id  path  string  true  "Record ID"
// @Success      200  {object}  memory.Record
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/memory/records/{id} [get]
func (h *MemoryHandler) GetRecord(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)
	record, err := h.store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "record not found")
		return
	}
	writeJSON(w, http.StatusOK, record)
}

// UpdateRecord handles PUT /api/v1/admin/memory/records/{id}.
//
// @Summary      Update memory record
// @Description  Update content, category, confidence, dimension, or metadata on a record.
// @Tags         Memory
// @Accept       json
// @Produce      json
// @Param        id    path  string               true  "Record ID"
// @Param        body  body  memoryUpdateRequest   true  "Fields to update"
// @Success      200  {object}  statusResponse
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/memory/records/{id} [put]
func (h *MemoryHandler) UpdateRecord(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)

	var req memoryUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Verify record exists and is not archived.
	record, err := h.store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "record not found")
		return
	}
	if record.Status == memory.StatusArchived {
		writeError(w, http.StatusConflict, "cannot update an archived record")
		return
	}

	updates := memory.RecordUpdate{
		Content:    req.Content,
		Category:   req.Category,
		Confidence: req.Confidence,
		Dimension:  req.Dimension,
		Metadata:   req.Metadata,
	}
	if err := h.store.Update(r.Context(), id, updates); err != nil {
		slog.Error("failed to update memory record", "id", id, "error", err) // #nosec G706 -- structured log with validated id, not user-facing
		writeError(w, http.StatusInternalServerError, "failed to update memory record")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// DeleteRecord handles DELETE /api/v1/admin/memory/records/{id}.
//
// @Summary      Archive memory record
// @Description  Soft-deletes a memory record by setting its status to archived.
// @Tags         Memory
// @Produce      json
// @Param        id  path  string  true  "Record ID"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/memory/records/{id} [delete]
func (h *MemoryHandler) DeleteRecord(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)

	// Verify record exists.
	if _, err := h.store.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "record not found")
		return
	}

	if err := h.store.Delete(r.Context(), id); err != nil {
		slog.Error("failed to archive memory record", "id", id, "error", err) // #nosec G706 -- structured log with validated id, not user-facing
		writeError(w, http.StatusInternalServerError, "failed to archive memory record")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// parseMemoryFilter parses query parameters into a memory.Filter.
func parseMemoryFilter(r *http.Request) memory.Filter {
	q := r.URL.Query()
	filter := memory.Filter{
		Persona:   q.Get("persona"),
		Dimension: q.Get("dimension"),
		Category:  q.Get("category"),
		Status:    q.Get("status"),
		Source:    q.Get("source"),
		EntityURN: q.Get("entity_urn"),
		CreatedBy: q.Get("created_by"),
		Since:     parseTimeParam(q, "since"),
		Until:     parseTimeParam(q, "until"),
		Limit:     parseLimit(q),
	}
	filter.Offset = parsePageOffset(q, filter.EffectiveLimit())
	return filter
}
