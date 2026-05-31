package admin

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// embeddingProviderStatusResponse is returned by GET /admin/embedding/status.
// Reports which embedding provider is wired and whether it can produce
// real vectors. The portal renders the unconfigured state as a banner
// on the Catalogs and Memory panels so operators see the degraded
// state instead of "N/N indexed" badges built from zero vectors (#429).
type embeddingProviderStatusResponse struct {
	// Kind is the provider implementation identifier: "ollama", "noop",
	// or a future kind. Empty string when no provider was wired.
	Kind string `json:"kind" example:"ollama"`
	// Model identifies the underlying embedding model when the provider
	// exposes it (e.g., "nomic-embed-text" for Ollama). Empty for
	// providers whose backend has no model concept (noop).
	Model string `json:"model" example:"nomic-embed-text"`
	// Dimension is the embedding vector length.
	Dimension int `json:"dimension" example:"768"`
	// Status is the operator-visible health enum:
	//   - "ok": a real provider is configured; semantic features active.
	//   - "unconfigured": no provider configured (or noop placeholder).
	//     Semantic ranking falls back to lexical; memory writes persist
	//     with Embedding: nil.
	Status string `json:"status" example:"ok"`
}

const (
	embeddingStatusOK           = "ok"
	embeddingStatusUnconfigured = "unconfigured"
)

// modelNamed mirrors the optional interface declared in
// pkg/toolkits/apigateway/embed_spec.go. Repeated here so admin does
// not import the apigateway package to read the model name.
type modelNamed interface {
	Model() string
}

// getEmbeddingStatus handles GET /api/v1/admin/embedding/status.
//
// @Summary      Get embedding provider status
// @Description  Returns the configured embedding provider's kind, model, dimension, and a status enum (ok / unconfigured). Used by the portal to surface a banner when semantic features are disabled.
// @Tags         System
// @Produce      json
// @Success      200  {object}  embeddingProviderStatusResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/embedding/status [get]
func (h *Handler) getEmbeddingStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.embeddingProviderStatus())
}

// embeddingProviderStatus builds the embedding-provider health view
// from the wired provider. Shared by GET /admin/embedding/status and
// the Indexing dashboard summary banner so both report the same state
// (a noop or nil provider makes the whole index meaningless, so the
// dashboard surfaces it prominently).
func (h *Handler) embeddingProviderStatus() embeddingProviderStatusResponse {
	prov := h.deps.Embedder
	if prov == nil {
		return embeddingProviderStatusResponse{Status: embeddingStatusUnconfigured}
	}
	resp := embeddingProviderStatusResponse{
		Kind:      prov.Kind(),
		Dimension: prov.Dimension(),
	}
	if m, ok := prov.(modelNamed); ok {
		resp.Model = m.Model()
	}
	if embedding.IsConfigured(prov) {
		resp.Status = embeddingStatusOK
	} else {
		resp.Status = embeddingStatusUnconfigured
	}
	return resp
}
