package promptlayer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// promptRefPrefix is the universal fetch-reference scheme for prompts, the same
// mcp:prompt:<id> form the search federation emits, accepted by `use` as a
// stable handle.
const promptRefPrefix = "mcp:prompt:"

// useConfidenceMargin is the minimum score gap between the top two ranked
// candidates for the top result to count as the single confident match; a
// smaller gap returns the candidate list for the agent to disambiguate.
const useConfidenceMargin = 0.15

// useCandidateLimit caps the candidate list returned for an ambiguous handle.
const useCandidateLimit = 5

// handlePromptUse resolves any prompt handle to the caller's visible prompt and
// returns it ready to run: an mcp:prompt:<id> reference, an exact bare name, an
// exact display name (case-insensitive), or free text ranked against the
// library. A single confident match returns the rendered content with argument
// specs and provenance; an ambiguous handle returns ranked candidates.
func (h *Handle) handlePromptUse(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	handle := strings.TrimSpace(input.Name)
	if handle == "" {
		return promptErrorResult("name is required: pass a prompt name, display name, mcp:prompt:<id> reference, or free text"), nil, nil
	}
	if id, ok := strings.CutPrefix(handle, promptRefPrefix); ok {
		return h.useByID(ctx, id, input.Args)
	}
	if pr := h.useExactName(ctx, strings.ToLower(handle)); pr != nil {
		return promptUseResult(pr, input.Args)
	}
	if res, done := h.useExactDisplayName(ctx, handle, input.Args); done {
		return res, nil, nil
	}
	return h.useRanked(ctx, handle, input.Args)
}

// useByID resolves an mcp:prompt:<id> reference within the caller's visibility.
func (h *Handle) useByID(ctx context.Context, id string, args map[string]string) (*mcp.CallToolResult, any, error) {
	pr, err := h.store.GetByID(ctx, id)
	if err != nil {
		slog.Error(promptErrGet, promptLogKey, id, promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, promptErrGet, err), nil, nil
	}
	if pr == nil || !pr.Enabled || !h.canViewPrompt(ctx, pr) {
		return promptErrorResult(fmt.Sprintf("prompt %q not found", promptRefPrefix+id)), nil, nil
	}
	return promptUseResult(pr, args)
}

// canViewPrompt applies the same visibility rule as manage_prompt get: global
// and persona prompts are readable by everyone; a personal prompt only by its
// owner, someone it is shared with, or an admin. Share access is matched by
// prompt ID: a same-named prompt shared by someone else grants nothing.
func (h *Handle) canViewPrompt(ctx context.Context, pr *prompt.Prompt) bool {
	if pr.Scope != prompt.ScopePersonal || h.isAdminPersona(ctx) {
		return true
	}
	email := resolveEmail(ctx)
	if pr.OwnerEmail == email {
		return true
	}
	return h.isSharedWithCaller(ctx, email, pr.ID)
}

// isSharedWithCaller reports whether the prompt with the given ID is shared
// directly with the caller.
func (h *Handle) isSharedWithCaller(ctx context.Context, email, id string) bool {
	if email == "" || id == "" || h.shareStore == nil {
		return false
	}
	refs, err := h.shareStore.ListSharedPromptsWithUser(ctx, "", email)
	if err != nil {
		return false
	}
	for _, ref := range refs {
		if ref.PromptID == id {
			return true
		}
	}
	return false
}

// useExactName resolves an exact bare stored name: the caller's own personal
// prompt, then one shared with them, then the globally-unique shared
// (global/persona) prompt. System rows resolve here too, so built-ins are
// runnable by name via `use`.
func (h *Handle) useExactName(ctx context.Context, name string) *prompt.Prompt {
	if prompt.ValidateName(name) != nil {
		return nil
	}
	email := resolveEmail(ctx)
	if pr, err := h.store.GetPersonal(ctx, email, name); err == nil && pr != nil && pr.Enabled {
		return pr
	}
	if pr := h.sharedPromptByName(ctx, email, name); pr != nil {
		return pr
	}
	if pr, err := h.store.Get(ctx, name); err == nil && pr != nil && pr.Enabled {
		return pr
	}
	return nil
}

// useExactDisplayName resolves an exact display-name match (case-insensitive)
// over the caller's visible prompts. A unique match, or a unique
// highest-precedence match among several, resolves; a precedence tie returns
// the candidates. done is false when nothing matched.
func (h *Handle) useExactDisplayName(ctx context.Context, handle string, args map[string]string) (*mcp.CallToolResult, bool) {
	var matches []prompt.Prompt
	for _, pr := range h.visiblePrompts(ctx, handle) {
		if strings.EqualFold(pr.DisplayName, handle) {
			matches = append(matches, pr)
		}
	}
	switch {
	case len(matches) == 0:
		return nil, false
	case len(matches) == 1:
		res, _, _ := promptUseResult(&matches[0], args)
		return res, true
	}
	if winner := precedenceWinner(matches); winner != nil {
		res, _, _ := promptUseResult(winner, args)
		return res, true
	}
	res, _, _ := promptCandidatesResult(unscored(matches))
	return res, true
}

// visiblePrompts returns the enabled prompts the caller can see, narrowed by
// the store's substring search where the backend supports it. Admins see all
// scopes; non-admins see their own personal prompts plus global and their
// persona's prompts (prompts shared user-to-user resolve by exact name only,
// matching the search visibility rule).
func (h *Handle) visiblePrompts(ctx context.Context, search string) []prompt.Prompt {
	enabled := true
	if h.isAdminPersona(ctx) {
		out, err := h.store.List(ctx, prompt.ListFilter{Search: search, Enabled: &enabled})
		if err != nil {
			slog.Warn("failed to list prompts", logKeyError, err)
			return nil
		}
		return out
	}
	out, err := h.store.List(ctx, prompt.ListFilter{
		Scope: prompt.ScopePersonal, OwnerEmail: resolveEmail(ctx), Search: search, Enabled: &enabled,
	})
	if err != nil {
		slog.Warn("failed to list personal prompts", logKeyError, err)
		out = nil
	}
	return h.mergeExtraScopes(ctx, out, &enabled)
}

// useRanked resolves free text by relevance ranking within the caller's
// visibility: the single result, or a top result clearing the confidence
// margin, resolves; otherwise the ranked candidates are returned. A store
// without ranking degrades to a substring match over the visible prompts.
func (h *Handle) useRanked(ctx context.Context, handle string, args map[string]string) (*mcp.CallToolResult, any, error) {
	searcher, ok := h.store.(prompt.Searcher)
	if !ok {
		return h.useSubstring(ctx, handle, args)
	}
	persona := ""
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		persona = pc.PersonaName
	}
	scored, err := searcher.Search(ctx, prompt.SearchQuery{
		Embedding:  embedding.EmbedForSearch(ctx, h.embedder, handle),
		QueryText:  handle,
		OwnerEmail: resolveEmail(ctx),
		Persona:    persona,
		IsAdmin:    h.isAdminPersona(ctx),
		Limit:      useCandidateLimit,
	})
	if err != nil {
		slog.Error("failed to resolve prompt", promptLogKey, handle, promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, "failed to resolve prompt", err), nil, nil
	}
	switch {
	case len(scored) == 0:
		return promptErrorResult(fmt.Sprintf("no prompt matched %q; use the list command to browse", handle)), nil, nil
	case len(scored) == 1 || scored[0].Score-scored[1].Score >= useConfidenceMargin:
		return promptUseResult(&scored[0].Prompt, args)
	}
	return promptCandidatesResult(scored)
}

// useSubstring is the ranking fallback for stores without search: a unique
// substring match over the visible prompts resolves; several return candidates.
// Spoken handles use spaces where machine names use hyphens, so the
// hyphenated form is matched against the name too.
func (h *Handle) useSubstring(ctx context.Context, handle string, args map[string]string) (*mcp.CallToolResult, any, error) {
	hyphenated := strings.ReplaceAll(handle, " ", "-")
	var matches []prompt.Prompt
	for _, pr := range h.visiblePrompts(ctx, handle) {
		if containsFold(pr.Name, handle) || containsFold(pr.Name, hyphenated) ||
			containsFold(pr.DisplayName, handle) || containsFold(pr.Description, handle) {
			matches = append(matches, pr)
		}
	}
	switch {
	case len(matches) == 0:
		return promptErrorResult(fmt.Sprintf("no prompt matched %q; use the list command to browse", handle)), nil, nil
	case len(matches) == 1:
		return promptUseResult(&matches[0], args)
	}
	return promptCandidatesResult(unscored(matches))
}

// containsFold reports whether s contains substr case-insensitively.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// Display-name collision precedence for `use`: when several visible prompts
// share a display name, the caller's own personal prompt outranks their
// persona's, which outranks a global. This orders only `use` resolution; the
// native prompts surface is collision-free by construction (presented names
// carry scope prefixes).
const (
	scopeRankGlobal = iota
	scopeRankPersona
	scopeRankPersonal
)

// scopeRank maps a prompt scope to its display-name collision precedence.
func scopeRank(scope string) int {
	switch scope {
	case prompt.ScopePersonal:
		return scopeRankPersonal
	case prompt.ScopePersona:
		return scopeRankPersona
	default:
		return scopeRankGlobal
	}
}

// precedenceWinner returns the unique highest-precedence prompt among the
// matches, or nil on a tie.
func precedenceWinner(matches []prompt.Prompt) *prompt.Prompt {
	best, bestRank, tied := -1, -1, false
	for i := range matches {
		switch r := scopeRank(matches[i].Scope); {
		case r > bestRank:
			best, bestRank, tied = i, r, false
		case r == bestRank:
			tied = true
		}
	}
	if tied {
		return nil
	}
	return &matches[best]
}

// unscored wraps plain prompts as zero-score candidates for the shared
// candidate presentation.
func unscored(matches []prompt.Prompt) []prompt.ScoredPrompt {
	scored := make([]prompt.ScoredPrompt, 0, len(matches))
	for _, m := range matches {
		scored = append(scored, prompt.ScoredPrompt{Prompt: m})
	}
	return scored
}

// missingRequiredArgs lists the required argument names absent from args.
func missingRequiredArgs(specs []prompt.Argument, args map[string]string) []string {
	var missing []string
	for _, a := range specs {
		if a.Required && args[a.Name] == "" {
			missing = append(missing, a.Name)
		}
	}
	return missing
}

// promptProvenance builds the provenance block of a resolved prompt so the
// agent can confirm what it is about to run.
func promptProvenance(pr *prompt.Prompt) map[string]any {
	prov := map[string]any{
		fieldName:      pr.Name,
		"display_name": displayOrName(pr.DisplayName, pr.Name),
		"description":  pr.Description,
		"scope":        pr.Scope,
		"source":       pr.Source,
		fieldStatus:    pr.Status,
	}
	if pr.ID != "" {
		prov["reference"] = promptRefPrefix + pr.ID
	}
	if pr.OwnerEmail != "" {
		prov["owner_email"] = pr.OwnerEmail
	}
	if pr.ApprovedBy != "" {
		prov["approved_by"] = pr.ApprovedBy
	}
	if pr.ApprovedAt != nil {
		prov["approved_at"] = pr.ApprovedAt
	}
	if !pr.UpdatedAt.IsZero() {
		prov["updated_at"] = pr.UpdatedAt
	}
	if len(pr.Tags) > 0 {
		prov["tags"] = pr.Tags
	}
	return prov
}

// promptUseResult renders a resolved prompt: content with the supplied argument
// values substituted, the argument specs, provenance, and any required
// arguments still missing.
func promptUseResult(pr *prompt.Prompt, args map[string]string) (*mcp.CallToolResult, any, error) {
	resp := map[string]any{
		fieldStatus:  "resolved",
		"prompt":     promptProvenance(pr),
		"arguments":  pr.Arguments,
		fieldContent: substituteArgs(pr.Content, args),
	}
	if missing := missingRequiredArgs(pr.Arguments, args); len(missing) > 0 {
		resp["missing_required_arguments"] = missing
	}
	return promptJSONResult(resp)
}

// promptCandidatesResult returns the ambiguous-handle response: a short ranked
// candidate list for the agent to disambiguate, never an error and never a
// silent first-match.
func promptCandidatesResult(scored []prompt.ScoredPrompt) (*mcp.CallToolResult, any, error) {
	candidates := make([]map[string]any, 0, min(len(scored), useCandidateLimit))
	for i := range scored {
		if i >= useCandidateLimit {
			break
		}
		pr := &scored[i].Prompt
		c := map[string]any{
			fieldName:      pr.Name,
			"display_name": displayOrName(pr.DisplayName, pr.Name),
			"description":  pr.Description,
			"scope":        pr.Scope,
		}
		if pr.ID != "" {
			c["reference"] = promptRefPrefix + pr.ID
		}
		if scored[i].Score > 0 {
			c["score"] = scored[i].Score
		}
		candidates = append(candidates, c)
	}
	return promptJSONResult(map[string]any{
		fieldStatus:  "ambiguous",
		"candidates": candidates,
		"message":    "multiple prompts match; call use again with the exact name or reference",
	})
}
