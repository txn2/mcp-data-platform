// Package admin provides REST API endpoints for administrative operations.
package admin

import (
	"encoding/json"
	"net/http"
)

// Handler provides admin REST API endpoints.
type Handler struct {
	mux        *http.ServeMux
	knowledge  *KnowledgeHandler
	authMiddle func(http.Handler) http.Handler
}

// NewHandler creates a new admin API handler.
func NewHandler(kh *KnowledgeHandler, authMiddle func(http.Handler) http.Handler) *Handler {
	h := &Handler{
		mux:        http.NewServeMux(),
		knowledge:  kh,
		authMiddle: authMiddle,
	}
	h.registerRoutes()
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authMiddle != nil {
		h.authMiddle(h.mux).ServeHTTP(w, r)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// registerRoutes registers all admin API routes.
func (h *Handler) registerRoutes() {
	if h.knowledge != nil {
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/insights", h.knowledge.ListInsights)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/insights/stats", h.knowledge.GetStats)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/insights/{id}", h.knowledge.GetInsight)
		h.mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}/status", h.knowledge.UpdateInsightStatus)
		h.mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}", h.knowledge.UpdateInsight)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/changesets", h.knowledge.ListChangesets)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/changesets/{id}", h.knowledge.GetChangeset)
		h.mux.HandleFunc("POST /api/v1/admin/knowledge/changesets/{id}/rollback", h.knowledge.RollbackChangeset)
	}
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
