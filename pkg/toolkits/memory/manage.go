package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const idLength = 16

// memory_manage command names. Defined as constants so the dispatch
// switch, the help map, and any audit/log statements all reference
// the same literal — and goconst doesn't flag the repeats.
const (
	cmdUpdate           = "update"
	cmdForget           = "forget"
	cmdList             = "list"
	cmdReviewStale      = "review_stale"
	cmdReviewDuplicates = "review_duplicates"
	cmdConsolidate      = "consolidate"
	// fieldMessage is the JSON key used in successful command results.
	fieldMessage = "message"
)

// handleManage dispatches memory_manage commands. Creating memory moved to the
// memory_capture tool (#633); this tool manages the lifecycle of existing
// records.
func (t *Toolkit) handleManage(ctx context.Context, _ *mcp.CallToolRequest, input manageInput) (*mcp.CallToolResult, any, error) {
	switch input.Command {
	case cmdUpdate:
		return t.handleUpdate(ctx, input)
	case cmdForget:
		return t.handleForget(ctx, input)
	case cmdList:
		return t.handleList(ctx, input)
	case cmdReviewStale:
		return t.handleReviewStale(ctx, input)
	case cmdReviewDuplicates:
		return t.handleReviewDuplicates(ctx, input)
	case cmdConsolidate:
		return t.handleConsolidate(ctx, input)
	case "":
		return helpResult(), nil, nil
	default:
		return errorResult(fmt.Sprintf("unknown command %q: use update, forget, list, review_stale, review_duplicates, or consolidate (create with memory_capture)", input.Command)), nil, nil
	}
}

// embeddingBreadcrumbs returns the model identifier and content hash
// that travel with a freshly-computed embedding so a synchronously-
// embedded row carries the same breadcrumbs the indexjobs memory
// consumer writes and dedups on (model match + SHA-256 text hash). A row
// stamped this way is not flagged as a gap by the reconciler and is not
// re-embedded on a later sweep unless its content or the provider model
// changes. Returns zero values for an empty vector (embedder skipped or
// failed), leaving the columns NULL/” so the reconciler backfills them.
func (t *Toolkit) embeddingBreadcrumbs(emb []float32, content string) (model string, hash []byte) {
	if len(emb) == 0 {
		return "", nil
	}
	sum := sha256.Sum256([]byte(content))
	return embedding.ModelName(t.embedder), sum[:]
}

// handleUpdate modifies an existing memory record.
func (t *Toolkit) handleUpdate(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	if input.ID == "" {
		return errorResult("id is required for update"), nil, nil
	}

	if result := verifyOwnership(ctx, t.store, input.ID, cmdUpdate); result != nil {
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

	// Re-embed if content changed. Symmetric with the handleRemember
	// guard: skip the noop placeholder so an update on an unconfigured
	// deployment does not overwrite a previously-real vector with a
	// zero vector (#429).
	if input.Content != "" && embedding.IsConfigured(t.embedder) {
		emb, err := t.embedder.Embed(ctx, input.Content)
		if err != nil {
			slog.Warn("embedding generation failed on update", "error", err)
		} else {
			updates.Embedding = emb
			updates.EmbeddingModel, updates.EmbeddingTextHash = t.embeddingBreadcrumbs(emb, input.Content)
		}
	}

	if err := t.store.Update(ctx, input.ID, updates); err != nil {
		return errorResult("failed to update memory: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"id":         input.ID,
		fieldMessage: "Memory updated successfully.",
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
		"id":         input.ID,
		fieldMessage: "Memory archived successfully.",
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
		"records":    records,
		"total":      total,
		"limit":      filter.EffectiveLimit(),
		"offset":     filter.Offset,
		fieldMessage: fmt.Sprintf("%d stale memories found. Use 'update' to revise or 'forget' to archive.", total),
	}), nil, nil
}

// handleReviewDuplicates lists the caller's high-similarity active record
// pairs for consolidation review, the backstop for near-duplicates the
// capture-time recall gate missed (#762). Memory content is per-user, so the
// listing is scoped to the caller's own records — the same boundary
// consolidate/update/forget enforce, keeping every listed pair actionable.
// The similarity floor is the capture-time suggest threshold: the backstop
// exists precisely for pairs below the auto-supersede bar.
func (t *Toolkit) handleReviewDuplicates(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	finder, ok := t.store.(memstore.DuplicateFinder)
	if !ok {
		return errorResult("review_duplicates requires the database-backed memory store with vector search"), nil, nil
	}
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserEmail == "" {
		return errorResult("a user identity (email) is required to review duplicates"), nil, nil
	}

	pairs, err := finder.SimilarActivePairs(ctx, pc.UserEmail, recallSuggestThreshold, input.Limit)
	if err != nil {
		return errorResult("failed to list duplicate candidates: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"pairs": pairs,
		"total": len(pairs),
		fieldMessage: fmt.Sprintf("%d high-similarity active pair(s) found. To consolidate a pair, use"+
			" command=consolidate with id (the record to keep) and duplicate_id (the record it supersedes).", len(pairs)),
	}), nil, nil
}

// handleConsolidate supersedes a duplicate record by the record being kept,
// completing the review_duplicates loop. Both records must belong to the
// caller (same ownership rule as update/forget) and the kept record must be
// active — otherwise the "duplicate" could be the only live copy of the fact
// and consolidating would silently retire it behind a dead record. The
// supersede preserves the correction chain via metadata.superseded_by rather
// than discarding the duplicate outright.
func (t *Toolkit) handleConsolidate(ctx context.Context, input manageInput) (*mcp.CallToolResult, any, error) {
	if input.ID == "" || input.DuplicateID == "" {
		return errorResult("consolidate requires id (the record to keep) and duplicate_id (the record it supersedes)"), nil, nil
	}
	if input.ID == input.DuplicateID {
		return errorResult("id and duplicate_id must differ"), nil, nil
	}

	keep, result := t.fetchConsolidatePair(ctx, input.ID, input.DuplicateID)
	if result != nil {
		return result, nil, nil
	}
	if keep.Status != memstore.StatusActive {
		return errorResult("the record to keep must be active (status: " + keep.Status + ")"), nil, nil
	}

	if err := t.store.Supersede(ctx, input.DuplicateID, input.ID); err != nil {
		return errorResult("failed to consolidate: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"id":         input.ID,
		"superseded": input.DuplicateID,
		fieldMessage: "Duplicate consolidated: the kept record now supersedes it.",
	}), nil, nil
}

// fetchConsolidatePair loads the two consolidate operands concurrently
// (independent PK lookups) and enforces caller ownership on both. It returns
// the kept record, or a non-nil error result when either record is missing or
// foreign.
func (t *Toolkit) fetchConsolidatePair(ctx context.Context, keepID, duplicateID string) (*memstore.Record, *mcp.CallToolResult) {
	var keep, dup *memstore.Record
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		r, err := t.store.Get(gctx, keepID)
		if err != nil {
			return fmt.Errorf("loading record to keep: %w", err)
		}
		keep = r
		return nil
	})
	g.Go(func() error {
		r, err := t.store.Get(gctx, duplicateID)
		if err != nil {
			return fmt.Errorf("loading duplicate record: %w", err)
		}
		dup = r
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, errorResult("memory not found")
	}

	pc := middleware.GetPlatformContext(ctx)
	for _, r := range []*memstore.Record{keep, dup} {
		if pc != nil && pc.UserEmail != "" && r.CreatedBy != pc.UserEmail {
			return nil, errorResult("you can only " + cmdConsolidate + " your own memories")
		}
	}
	return keep, nil
}

// helpResult returns the list of available commands.
func helpResult() *mcp.CallToolResult {
	return jsonResult(map[string]any{
		"commands": map[string]string{
			cmdUpdate:           "Update an existing memory (requires id)",
			cmdForget:           "Archive a memory (requires id)",
			cmdList:             "List memories with optional filters",
			cmdReviewStale:      "List memories flagged as stale",
			cmdReviewDuplicates: "List high-similarity active memory pairs for consolidation",
			cmdConsolidate:      "Supersede a duplicate record by the one kept (requires id and duplicate_id)",
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
