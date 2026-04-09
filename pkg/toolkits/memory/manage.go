package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const idLength = 16

// handleManage dispatches memory_manage commands.
func (t *Toolkit) handleManage(ctx context.Context, _ *mcp.CallToolRequest, input manageInput) (*mcp.CallToolResult, any, error) {
	switch input.Command {
	case "remember":
		return t.handleRemember(ctx, input)
	case "update":
		return t.handleUpdate(ctx, input)
	case "forget":
		return t.handleForget(ctx, input)
	case "list":
		return t.handleList(ctx, input)
	case "review_stale":
		return t.handleReviewStale(ctx, input)
	case "":
		return helpResult(), nil, nil
	default:
		return errorResult(fmt.Sprintf("unknown command %q: use remember, update, forget, list, or review_stale", input.Command)), nil, nil
	}
}

// validateRememberInput checks all required fields for remember command.
func validateRememberInput(input manageInput) error {
	if err := memstore.ValidateContent(input.Content); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	if err := memstore.ValidateDimension(input.Dimension); err != nil {
		return fmt.Errorf("dimension: %w", err)
	}
	if err := memstore.ValidateCategory(input.Category); err != nil {
		return fmt.Errorf("category: %w", err)
	}
	if err := memstore.ValidateConfidence(input.Confidence); err != nil {
		return fmt.Errorf("confidence: %w", err)
	}
	if err := memstore.ValidateSource(input.Source); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if err := memstore.ValidateEntityURNs(input.EntityURNs); err != nil {
		return fmt.Errorf("entity_urns: %w", err)
	}
	return nil
}

// handleRemember creates a new memory record.
func (t *Toolkit) handleRemember(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	if err := validateRememberInput(input); err != nil {
		return errorResult(err.Error()), nil, nil
	}

	pc := middleware.GetPlatformContext(ctx)
	id, err := generateID()
	if err != nil {
		return errorResult("failed to generate ID"), nil, nil //nolint:nilerr // MCP protocol
	}

	// Generate embedding (graceful failure if Ollama unavailable).
	var emb []float32
	emb, err = t.embedder.Embed(ctx, input.Content)
	if err != nil {
		slog.Warn("embedding generation failed, storing without embedding", "error", err)
		emb = nil
	}

	record := memstore.Record{
		ID:         id,
		CreatedBy:  pc.UserEmail,
		Persona:    pc.PersonaName,
		Dimension:  memstore.NormalizeDimension(input.Dimension),
		Content:    input.Content,
		Category:   memstore.NormalizeCategory(input.Category),
		Confidence: memstore.NormalizeConfidence(input.Confidence),
		Source:     memstore.NormalizeSource(input.Source),
		EntityURNs: input.EntityURNs,
		Embedding:  emb,
		Metadata:   input.Metadata,
		Status:     memstore.StatusActive,
	}

	if record.Metadata == nil {
		record.Metadata = make(map[string]any)
	}
	if pc.SessionID != "" {
		record.Metadata["session_id"] = pc.SessionID
	}

	if err := t.store.Insert(ctx, record); err != nil {
		return errorResult("failed to save memory: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"id":      id,
		"status":  "active",
		"message": "Memory recorded successfully.",
	}), nil, nil
}

// handleUpdate modifies an existing memory record.
func (t *Toolkit) handleUpdate(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	if input.ID == "" {
		return errorResult("id is required for update"), nil, nil
	}

	if result := verifyOwnership(ctx, t.store, input.ID, "update"); result != nil {
		return result, nil, nil
	}

	if input.Content != "" {
		if err := memstore.ValidateContent(input.Content); err != nil {
			return errorResult(err.Error()), nil, nil
		}
	}
	if err := memstore.ValidateCategory(input.Category); err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if err := memstore.ValidateConfidence(input.Confidence); err != nil {
		return errorResult(err.Error()), nil, nil
	}

	updates := memstore.RecordUpdate{
		Content:    input.Content,
		Category:   input.Category,
		Confidence: input.Confidence,
		Dimension:  input.Dimension,
		Metadata:   input.Metadata,
	}

	// Re-embed if content changed.
	if input.Content != "" {
		emb, err := t.embedder.Embed(ctx, input.Content)
		if err != nil {
			slog.Warn("embedding generation failed on update", "error", err)
		} else {
			updates.Embedding = emb
		}
	}

	if err := t.store.Update(ctx, input.ID, updates); err != nil {
		return errorResult("failed to update memory: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"id":      input.ID,
		"message": "Memory updated successfully.",
	}), nil, nil
}

// handleForget soft-deletes a memory record.
func (t *Toolkit) handleForget(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	if input.ID == "" {
		return errorResult("id is required for forget"), nil, nil
	}

	if result := verifyOwnership(ctx, t.store, input.ID, "archive"); result != nil {
		return result, nil, nil
	}

	if err := t.store.Delete(ctx, input.ID); err != nil {
		return errorResult("failed to archive memory: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"id":      input.ID,
		"message": "Memory archived successfully.",
	}), nil, nil
}

// verifyOwnership fetches a record and checks that the caller owns it.
// Returns an error result if the record is not found or the caller lacks ownership;
// returns nil when ownership is verified.
func verifyOwnership(ctx context.Context, store memstore.Store, id, action string) *mcp.CallToolResult {
	pc := middleware.GetPlatformContext(ctx)
	record, err := store.Get(ctx, id)
	if err != nil {
		return errorResult("memory not found")
	}
	if pc.UserEmail != "" && record.CreatedBy != pc.UserEmail {
		return errorResult("you can only " + action + " your own memories")
	}
	return nil
}

// handleList returns memory records matching filters.
func (t *Toolkit) handleList(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	pc := middleware.GetPlatformContext(ctx)

	filter := memstore.Filter{
		Persona:   pc.PersonaName,
		Dimension: input.FilterDimension,
		Category:  input.FilterCategory,
		Status:    input.FilterStatus,
		EntityURN: input.FilterEntityURN,
		Limit:     input.Limit,
		Offset:    input.Offset,
	}

	// Default to active status.
	if filter.Status == "" {
		filter.Status = memstore.StatusActive
	}

	records, total, err := t.store.List(ctx, filter)
	if err != nil {
		return errorResult("failed to list memories: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"records": records,
		"total":   total,
		"limit":   filter.EffectiveLimit(),
		"offset":  filter.Offset,
	}), nil, nil
}

// handleReviewStale returns stale memory records for admin review.
// Access is gated by persona tool visibility (opt-in per persona config),
// so no additional authorization check is needed here.
func (t *Toolkit) handleReviewStale(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	filter := memstore.Filter{
		Status: memstore.StatusStale,
		Limit:  input.Limit,
		Offset: input.Offset,
	}

	records, total, err := t.store.List(ctx, filter)
	if err != nil {
		return errorResult("failed to list stale memories: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"records": records,
		"total":   total,
		"limit":   filter.EffectiveLimit(),
		"offset":  filter.Offset,
		"message": fmt.Sprintf("%d stale memories found. Use 'update' to revise or 'forget' to archive.", total),
	}), nil, nil
}

// helpResult returns the list of available commands.
func helpResult() *mcp.CallToolResult {
	return jsonResult(map[string]any{
		"commands": map[string]string{
			"remember":     "Create a new memory (requires content)",
			"update":       "Update an existing memory (requires id)",
			"forget":       "Archive a memory (requires id)",
			"list":         "List memories with optional filters",
			"review_stale": "List memories flagged as stale",
		},
	})
}

// generateID generates a random hex ID.
func generateID() (string, error) {
	b := make([]byte, idLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// jsonResult creates a successful MCP result with JSON content.
func jsonResult(data any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return errorResult("internal error: " + err.Error())
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}

// errorResult creates an error MCP result.
func errorResult(msg string) *mcp.CallToolResult {
	b, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		// Fallback: plain text if marshal fails (should never happen for a string).
		b = []byte(`{"error": "internal error"}`)
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}
