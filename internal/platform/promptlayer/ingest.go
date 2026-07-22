package promptlayer

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// ingestStaticPrompts mirrors the statically-registered prompts (operator config
// + built-in workflow + toolkit prompts) into the prompt store as read-only,
// system-sourced rows so the existing embedding indexer and manage_prompt search
// cover them, exactly like database-authored prompts.
//
// Static prompts are otherwise served only as MCP protocol prompts (AddPrompt)
// and never reach the store, so on a deployment whose prompts are all static the
// prompt index and semantic search are empty (#593). The ingested rows are
// scope=global, status=approved (operator-trusted, no review), source=system.
// They are excluded from the prompts/list runtime path (already served via
// AddPrompt) and from registerDatabasePrompts, and are read-only via
// manage_prompt.
//
// Must run after all static prompts are registered into promptInfos and before
// registerDatabasePrompts (which would otherwise add database prompts to
// promptInfos and cause them to be re-ingested as system rows).
func (h *Handle) ingestStaticPrompts(ctx context.Context) {
	if h.store == nil {
		return
	}

	infos := h.staticPromptInfos()
	wanted := make(map[string]bool, len(infos))

	for _, info := range infos {
		if info.Name == "" {
			continue
		}
		wanted[info.Name] = true
		h.upsertSystemPrompt(ctx, info)
	}

	h.pruneStaleSystemPrompts(ctx, wanted)

	if len(wanted) > 0 {
		slog.Info("ingested static prompts for indexing and search", "count", len(wanted))
	}
}

// upsertSystemPrompt creates or refreshes the system row for one static prompt.
// A name already owned by a non-system prompt is left untouched so ingestion
// never clobbers user/admin-authored prompts.
func (h *Handle) upsertSystemPrompt(ctx context.Context, info registry.PromptInfo) {
	desired := systemPromptFromInfo(info)

	existing, err := h.store.Get(ctx, info.Name)
	if err != nil {
		slog.Warn("ingest static prompt: lookup failed", promptLogKey, info.Name, logKeyError, err)
		return
	}
	switch {
	case existing == nil:
		if err := h.store.Create(ctx, desired); err != nil {
			slog.Warn("ingest static prompt: create failed", promptLogKey, info.Name, logKeyError, err)
		}
	case existing.Source == prompt.SourceSystem:
		desired.ID = existing.ID
		if err := h.store.Update(ctx, desired); err != nil {
			slog.Warn("ingest static prompt: update failed", promptLogKey, info.Name, logKeyError, err)
		}
	default:
		slog.Warn("ingest static prompt: name already used by a non-system prompt; skipping",
			promptLogKey, info.Name, "source", existing.Source)
	}
}

// staticPromptInfos is the set of statically-registered prompts (operator config
// + workflow, held in promptInfos) plus toolkit-described prompts. It must be
// read before registerDatabasePrompts pollutes promptInfos with database prompts.
func (h *Handle) staticPromptInfos() []registry.PromptInfo {
	toolkit := h.collectToolkitPromptInfos()
	h.promptInfosMu.RLock()
	infos := make([]registry.PromptInfo, 0, len(h.promptInfos)+len(toolkit))
	infos = append(infos, h.promptInfos...)
	h.promptInfosMu.RUnlock()
	return append(infos, toolkit...)
}

// systemPromptFromInfo maps a registered prompt's metadata to a read-only,
// approved, global system prompt row suitable for indexing and search.
func systemPromptFromInfo(info registry.PromptInfo) *prompt.Prompt {
	args := make([]prompt.Argument, 0, len(info.Arguments))
	for _, a := range info.Arguments {
		args = append(args, prompt.Argument{Name: a.Name, Description: a.Description, Required: a.Required})
	}
	return &prompt.Prompt{
		Name:        info.Name,
		DisplayName: displayOrName(info.DisplayName, info.Name),
		Description: info.Description,
		Content:     info.Content,
		Arguments:   args,
		Category:    info.Category,
		Scope:       prompt.ScopeGlobal,
		Source:      prompt.SourceSystem,
		Status:      prompt.StatusApproved,
		Enabled:     true,
	}
}

// pruneStaleSystemPrompts deletes system-sourced prompt rows whose name is no
// longer in the registered static set, so removing a prompt from config or the
// build reconciles the store on the next startup.
func (h *Handle) pruneStaleSystemPrompts(ctx context.Context, wanted map[string]bool) {
	rows, err := h.store.List(ctx, prompt.ListFilter{Source: prompt.SourceSystem})
	if err != nil {
		slog.Warn("ingest static prompt: list system prompts failed", logKeyError, err)
		return
	}
	for i := range rows {
		if wanted[rows[i].Name] {
			continue
		}
		if err := h.store.DeleteByID(ctx, rows[i].ID); err != nil {
			slog.Warn("ingest static prompt: prune failed", "name", rows[i].Name, logKeyError, err)
		}
	}
}
