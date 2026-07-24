package promptlayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// ToolNameManagePrompt is the MCP tool name of the prompt-management tool,
// exported for composition roots that bind UI apps to it.
const ToolNameManagePrompt = "manage_prompt"

const (
	promptErrGet    = "failed to get prompt"
	promptLogKey    = "name"
	promptLogKeyErr = "error"

	// Command names for the manage_prompt list and use commands.
	cmdList = "list"
	cmdUse  = "use"

	// JSON field names used in result and schema maps. These share the
	// same string value as promptLogKey/promptLogKeyErr but are kept
	// separate for documentation clarity at call sites.
	fieldName    = "name"
	fieldContent = "content"
	// fieldStatus is the JSON result key for command-status strings
	// ("created", "updated", "deleted") returned by manage_prompt.
	fieldStatus = "status"
)

// managePromptInput is the input schema for the manage_prompt tool.
type managePromptInput struct {
	Command      string            `json:"command"`
	Name         string            `json:"name,omitempty"`
	DisplayName  string            `json:"display_name,omitempty"`
	Description  string            `json:"description,omitempty"`
	Content      string            `json:"content,omitempty"`
	Arguments    []prompt.Argument `json:"arguments,omitempty"`
	Category     string            `json:"category,omitempty"`
	Scope        string            `json:"scope,omitempty"`
	OwnerEmail   string            `json:"owner_email,omitempty"`
	Personas     []string          `json:"personas,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Status       string            `json:"status,omitempty"`
	SupersededBy string            `json:"superseded_by,omitempty"`
	Search       string            `json:"search,omitempty"`

	// Query (list command) ranks visible approved prompts by relevance instead
	// of the substring Search filter; Limit caps the ranked results.
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`

	// Args (use command) carries argument values substituted into the resolved
	// prompt's content.
	Args map[string]string `json:"args,omitempty"`

	// Content editing and navigation arguments (#1033), shared verbatim with
	// manage_asset through pkg/textpatch.
	Edits        []textpatch.Edit `json:"edits,omitempty"`
	BaseVersion  int              `json:"base_version,omitempty"`
	DryRun       bool             `json:"dry_run,omitempty"`
	Find         string           `json:"find,omitempty"`
	Pattern      string           `json:"pattern,omitempty"`
	Section      string           `json:"section,omitempty"`
	Selector     string           `json:"selector,omitempty"`
	LineStart    int              `json:"line_start,omitempty"`
	LineEnd      int              `json:"line_end,omitempty"`
	ContextBytes int              `json:"context_bytes,omitempty"`
	FromVersion  int              `json:"from_version,omitempty"`
	ToVersion    int              `json:"to_version,omitempty"`

	// Promotion request (owner action on a personal prompt, applied by update).
	// Setting RequestedScope flags the prompt for the admin promotion queue
	// without changing its scope; an admin approves to apply it.
	RequestedScope    string   `json:"requested_scope,omitempty"`
	RequestedPersonas []string `json:"requested_personas,omitempty"`
}

// RegisterTool registers the manage_prompt tool with the given MCP server. No-op
// on a nil Handle or a no-DB deployment (no store to manage prompts in).
func (h *Handle) RegisterTool(server *mcp.Server) {
	if h == nil || h.store == nil {
		return
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:  ToolNameManagePrompt,
		Title: "Manage Prompts",
		Description: "Create, update, delete, list, get, or use prompts. " +
			"When a user names a report, procedure, or recurring task ('run the daily sales report'), " +
			"resolve it against the prompt library first with the 'use' command instead of listing: " +
			"'use' accepts a prompt name, display name, mcp:prompt:<id> reference, or free text, and " +
			"returns the rendered prompt with its argument specs and provenance, or ranked candidates " +
			"when the handle is ambiguous. " +
			"A resolved prompt may carry attached reference material (a report template, a checklist, a " +
			"brand asset). Attached material is authoritative: fill an attached template rather than " +
			"inventing a format, follow an attached checklist, and read any attachment delivered as a " +
			"resource link before using it. When the response reports an attachment as unavailable or " +
			"missing, proceed and say that your reference material was incomplete. " +
			"Non-admin users can manage their own personal prompts. " +
			"Admins can manage prompts at all scope levels (global, persona, personal). " +
			"Editing the content or arguments of an approved global or persona prompt saves the change as a " +
			"pending draft version (update returns status 'pending_approval'); the approved version keeps " +
			"being served until an admin approves the draft. " +
			"Management commands cover database-stored prompts only; static prompts from server " +
			"configuration are not editable here, though 'use' resolves operator, workflow, and " +
			"toolkit prompts too. " +
			textpatch.VerbsDescription,
		InputSchema: managePromptSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input managePromptInput) (*mcp.CallToolResult, any, error) {
		return h.handleManagePrompt(ctx, input)
	})
}

// promptCommandHandler handles one manage_prompt command.
type promptCommandHandler func(context.Context, managePromptInput) (*mcp.CallToolResult, any, error)

// promptCommands is the command dispatch table, built per call because the
// handlers are bound to the receiver. A table rather than a switch keeps
// dispatch flat as the command set grows, mirroring the portal toolkit's
// buildActions.
func (h *Handle) promptCommands() map[string]promptCommandHandler {
	return map[string]promptCommandHandler{
		"create":      h.handlePromptCreate,
		"update":      h.handlePromptUpdate,
		"delete":      h.handlePromptDelete,
		cmdList:       h.handlePromptList,
		"get":         h.handlePromptGet,
		cmdUse:        h.handlePromptUse,
		cmdPatch:      h.handlePromptPatch,
		cmdLocate:     h.handlePromptLocate,
		cmdGetContent: h.handlePromptGetContent,
		cmdOutline:    h.handlePromptOutline,
		cmdStats:      h.handlePromptStats,
		cmdDiff:       h.handlePromptDiff,
	}
}

// handleManagePrompt dispatches manage_prompt commands.
func (h *Handle) handleManagePrompt(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	handler, ok := h.promptCommands()[input.Command]
	if !ok {
		return promptErrorResult(fmt.Sprintf("unknown command: %s", input.Command)), nil, nil
	}
	return handler(ctx, input)
}

// handlePromptCreate creates a new prompt.
func (h *Handle) handlePromptCreate(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if err := prompt.ValidateName(input.Name); err != nil {
		return promptErrorResult(err.Error()), nil, nil
	}
	if input.Content == "" {
		return promptErrorResult("content is required"), nil, nil
	}

	scope := input.Scope
	if scope == "" {
		scope = prompt.ScopePersonal
	}
	if err := prompt.ValidateScope(scope); err != nil {
		return promptErrorResult(err.Error()), nil, nil
	}
	if err := prompt.ValidateTags(input.Tags); err != nil {
		return promptErrorResult(err.Error()), nil, nil
	}

	email := resolveEmail(ctx)
	if !h.isAdminPersona(ctx) && scope != prompt.ScopePersonal {
		return promptErrorResult("only admins can create global or persona-scoped prompts"), nil, nil
	}

	personas := input.Personas
	if personas == nil {
		personas = []string{}
	}

	pr := &prompt.Prompt{
		Name:        input.Name,
		DisplayName: input.DisplayName,
		Description: input.Description,
		Content:     input.Content,
		Arguments:   input.Arguments,
		Category:    input.Category,
		Scope:       scope,
		Personas:    personas,
		Tags:        input.Tags,
		OwnerEmail:  email,
		Source:      prompt.SourceOperator,
		Enabled:     true,
	}

	if err := h.store.Create(ctx, pr); err != nil {
		slog.Error("failed to create prompt", promptLogKey, input.Name, promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, "failed to create prompt", err), nil, nil
	}

	h.RegisterRuntimePrompt(pr)

	return promptJSONResult(map[string]any{
		fieldStatus: "created",
		"id":        pr.ID,
		fieldName:   pr.Name,
	})
}

// handlePromptUpdate updates an existing prompt.
func (h *Handle) handlePromptUpdate(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return promptErrorResult("name is required"), nil, nil
	}

	// Resolve the prompt to edit by name, not by the target scope: on update
	// input.Scope is the *new* scope to set, so passing it as a resolution filter
	// would hide the caller's own personal prompt behind a shared-only lookup and
	// report their prompt as "not found". The new scope is applied below.
	resolveInput := input
	resolveInput.Scope = ""
	existing, errResult := h.editablePrompt(ctx, resolveInput)
	if errResult != nil {
		return errResult, nil, nil
	}
	email := resolveEmail(ctx)

	// Snapshot the persisted state before mutation: ApplyEdit compares it
	// against the edited copy to decide whether the edit needs admin review.
	before := *existing

	oldScope := existing.Scope
	if errMsg := applyPromptUpdates(existing, input, h.isAdminPersona(ctx)); errMsg != "" {
		return promptErrorResult(errMsg), nil, nil
	}
	if errMsg := applyStatusTransition(existing, input.Status, input.SupersededBy, email, h.isAdminPersona(ctx)); errMsg != "" {
		return promptErrorResult(errMsg), nil, nil
	}
	if errMsg := h.checkPromotionNameFree(ctx, existing, oldScope); errMsg != "" {
		return promptErrorResult(errMsg), nil, nil
	}

	return h.persistPromptUpdate(ctx, &before, existing, oldScope, email)
}

// persistPromptUpdate lands an edited prompt through the shared review gate.
// A review-gated content edit becomes a pending draft version and the served
// prompt (and its runtime metadata) stays on the approved snapshot; every
// other edit applies and re-registers.
func (h *Handle) persistPromptUpdate(ctx context.Context, before, existing *prompt.Prompt, oldScope, email string) (*mcp.CallToolResult, any, error) {
	return h.persistPromptEdit(ctx, promptEdit{
		before: before, after: existing, oldScope: oldScope, email: email,
	})
}

// promptEdit is one edit on its way through the review gate: the persisted
// pre-edit state, the fully mutated copy, the scope the prompt held before, the
// acting author, and any extra response fields the caller wants merged into the
// result (a patch's per-edit report and diff).
type promptEdit struct {
	before   *prompt.Prompt
	after    *prompt.Prompt
	oldScope string
	email    string
	extra    map[string]any
}

// persistPromptEdit lands an edit through prompt.ApplyEdit and renders the
// outcome, so every mutation surface reports the same shape whether the edit
// applied or became a pending draft.
func (h *Handle) persistPromptEdit(ctx context.Context, e promptEdit) (*mcp.CallToolResult, any, error) {
	before, existing, oldScope, email, extra := e.before, e.after, e.oldScope, e.email, e.extra
	outcome, err := prompt.ApplyEdit(ctx, h.store, before, existing, email)
	// Both sentinels carry a message written for the author and are the author's
	// to fix, so they surface verbatim instead of as a generic failure.
	if errors.Is(err, prompt.ErrReviewRequiredMixedEdit) || errors.Is(err, prompt.ErrAttachmentScope) {
		return promptErrorResult(err.Error()), nil, nil
	}
	if err != nil {
		slog.Error("failed to update prompt", promptLogKey, existing.Name, promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, "failed to update prompt", err), nil, nil
	}
	if !outcome.Applied {
		return promptJSONResult(withExtra(map[string]any{
			fieldStatus:       "pending_approval",
			fieldName:         existing.Name,
			"pending_version": outcome.PendingVersion,
			"message": fmt.Sprintf("this prompt is approved and shared, so the content change was saved as "+
				"draft version %d; the approved version continues to be served until an admin approves the "+
				"draft in the admin portal or via the admin prompts API", outcome.PendingVersion),
		}, extra))
	}

	h.reregisterAfterUpdate(existing, oldScope)

	return promptJSONResult(withExtra(map[string]any{
		fieldStatus: "updated",
		fieldName:   existing.Name,
		"version":   existing.Version,
	}, extra))
}

// withExtra folds a caller's extra response fields into the outcome. The two
// key sets are disjoint by construction: the outcome owns the status and
// identity keys, extra carries only the patch report.
func withExtra(base, extra map[string]any) map[string]any {
	maps.Copy(base, extra)
	return base
}

// checkPromotionNameFree guards a personal-to-shared promotion: the shared
// (global/persona) namespace is globally unique, so promoting requires the name
// to be free there. Returns a non-empty user-facing error message when a
// different prompt already owns the name, or "" when the update is not a
// promotion or the name is free.
func (h *Handle) checkPromotionNameFree(ctx context.Context, existing *prompt.Prompt, oldScope string) string {
	if oldScope != prompt.ScopePersonal || existing.Scope == prompt.ScopePersonal {
		return ""
	}
	dup, _ := h.store.Get(ctx, existing.Name)
	if dup != nil && dup.ID != existing.ID {
		return fmt.Sprintf("the name %q is already used by a %s prompt; rename before promoting", existing.Name, dup.Scope)
	}
	return ""
}

// reregisterAfterUpdate refreshes the name-keyed metadata after an update.
// Personal prompts are not tracked there (names collide across owners), so only
// (un)register shared scopes; RegisterRuntimePrompt self-skips personal, and
// unregistering the old name is gated on the old scope to avoid dropping an
// unrelated shared entry.
func (h *Handle) reregisterAfterUpdate(existing *prompt.Prompt, oldScope string) {
	if oldScope != prompt.ScopePersonal {
		h.UnregisterRuntimePrompt(existing.Name)
	}
	h.RegisterRuntimePrompt(existing)
}

// authorizePromptMutation checks whether the caller may update or delete the
// target prompt. Admins may act on any prompt; a non-admin may only act on their
// own personal prompts. Returns a non-empty user-facing error message when the
// action is not permitted, or "" when it is. verb ("update"/"delete") is spliced
// into the ownership-denial message.
func authorizePromptMutation(existing *prompt.Prompt, email, verb string, isAdmin bool) string {
	if isAdmin {
		return ""
	}
	if existing.Scope != prompt.ScopePersonal {
		return "non-admins can only manage personal prompts"
	}
	if existing.OwnerEmail != email {
		return "you can only " + verb + " your own prompts"
	}
	return ""
}

// applyPromptUpdates applies non-empty fields from input to existing. Returns a
// non-empty error message when a validated field (scope, tags, promotion request)
// fails its check; the plain fields are always applied first.
func applyPromptUpdates(existing *prompt.Prompt, input managePromptInput, isAdmin bool) string {
	applyPlainPromptFields(existing, input)
	return applyValidatedPromptFields(existing, input, isAdmin)
}

// applyPlainPromptFields copies the input fields that need no validation onto
// existing, leaving an unset (empty/nil) input field untouched.
func applyPlainPromptFields(existing *prompt.Prompt, input managePromptInput) {
	if input.DisplayName != "" {
		existing.DisplayName = input.DisplayName
	}
	if input.Description != "" {
		existing.Description = input.Description
	}
	if input.Content != "" {
		existing.Content = input.Content
	}
	if input.Arguments != nil {
		existing.Arguments = input.Arguments
	}
	if input.Category != "" {
		existing.Category = input.Category
	}
	if input.Personas != nil {
		existing.Personas = input.Personas
	}
}

// applyValidatedPromptFields applies the input fields that carry authorization or
// validation rules: scope (admin-only for shared scopes), tags (format), and a
// promotion request. Returns a non-empty error message on the first failing check.
func applyValidatedPromptFields(existing *prompt.Prompt, input managePromptInput, isAdmin bool) string {
	if input.Scope != "" {
		if !isAdmin && input.Scope != prompt.ScopePersonal {
			return "only admins can set global or persona scope"
		}
		existing.Scope = input.Scope
	}
	if input.Tags != nil {
		if err := prompt.ValidateTags(input.Tags); err != nil {
			return err.Error()
		}
		existing.Tags = input.Tags
	}
	if input.RequestedScope != "" {
		if err := existing.ApplyPromotionRequest(input.RequestedScope, input.RequestedPersonas); err != nil {
			return err.Error()
		}
	}
	return ""
}

// applyStatusTransition validates and applies a prompt status change, stamping
// the lifecycle timestamps. Approval (-> approved) is admin-only. Returns a
// non-empty error message on an invalid or unauthorized transition.
func applyStatusTransition(existing *prompt.Prompt, newStatus, supersededBy, actorEmail string, isAdmin bool) string {
	if err := existing.ApplyStatusTransition(newStatus, supersededBy, actorEmail, isAdmin, time.Now().UTC()); err != nil {
		return err.Error()
	}
	return ""
}

// handlePromptDelete deletes a prompt.
func (h *Handle) handlePromptDelete(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return promptErrorResult("name is required"), nil, nil
	}

	existing, err := h.resolveManagedPrompt(ctx, input.Name, resolveEmail(ctx), input.Scope, input.OwnerEmail)
	if err != nil {
		slog.Error(promptErrGet, promptLogKey, input.Name, promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, promptErrGet, err), nil, nil
	}
	if existing == nil {
		return promptErrorResult(fmt.Sprintf("prompt %q not found", input.Name)), nil, nil
	}
	if existing.Source == prompt.SourceSystem {
		return promptErrorResult("this prompt is defined in server configuration or built in and is read-only; remove it from config"), nil, nil
	}

	email := resolveEmail(ctx)
	if msg := authorizePromptMutation(existing, email, "delete", h.isAdminPersona(ctx)); msg != "" {
		return promptErrorResult(msg), nil, nil
	}

	if err := h.store.DeleteByID(ctx, existing.ID); err != nil {
		slog.Error("failed to delete prompt", promptLogKey, input.Name, promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, "failed to delete prompt", err), nil, nil
	}

	// Personal prompts are not tracked in the name-keyed metadata; unregistering
	// by name would drop an unrelated shared entry of the same name.
	if existing.Scope != prompt.ScopePersonal {
		h.UnregisterRuntimePrompt(existing.Name)
	}

	return promptJSONResult(map[string]any{
		fieldStatus: "deleted",
		fieldName:   input.Name,
	})
}

// handlePromptList lists prompts visible to the current user. When a free-text
// query is supplied it ranks visible approved prompts by relevance; otherwise
// it returns the visible set filtered by the substring Search and scope.
func (h *Handle) handlePromptList(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Query) != "" {
		return h.handlePromptSearch(ctx, input)
	}

	filter := prompt.ListFilter{
		Scope:  input.Scope,
		Search: input.Search,
	}

	isAdmin := h.isAdminPersona(ctx)
	enabled := true
	filter.Enabled = &enabled

	if !isAdmin {
		// Non-admin with explicit scope: serve that scope directly (no owner filter for global/persona).
		// Non-admin with no scope: fetch personal + global + persona separately.
		if filter.Scope == prompt.ScopePersonal || filter.Scope == "" {
			filter.OwnerEmail = resolveEmail(ctx)
			if filter.Scope == "" {
				filter.Scope = prompt.ScopePersonal
			}
		}
	}

	prompts, err := h.store.List(ctx, filter)
	if err != nil {
		slog.Error("failed to list prompts", promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, "failed to list prompts", err), nil, nil
	}

	// For non-admins without an explicit scope, also include global and persona-scoped prompts.
	if !isAdmin && input.Scope == "" {
		prompts = h.mergeExtraScopes(ctx, prompts, &enabled)
	}

	ptrs := make([]*prompt.Prompt, len(prompts))
	for i := range prompts {
		ptrs[i] = &prompts[i]
	}
	return h.browseResponse(ctx, ptrs, map[string]any{
		"prompts": prompts,
		"count":   len(prompts),
	})
}

// browseResponse finalizes a list or search response: it batch-applies
// audit-derived usage to the returned prompts and attaches the shared
// collection list (#1010) when the store supports collections, so MCP clients
// and the prompt-browser app see the same organization model as the portal.
func (h *Handle) browseResponse(ctx context.Context, prompts []*prompt.Prompt, resp map[string]any) (*mcp.CallToolResult, any, error) {
	h.applyUsageAll(ctx, prompts)
	if cols := h.listCollections(ctx); len(cols) > 0 {
		resp["collections"] = cols
	}
	return promptJSONResult(resp)
}

// listCollections returns the shared collection list, or nil when the store
// lacks the collection capability or the read fails.
func (h *Handle) listCollections(ctx context.Context) []prompt.Collection {
	cs := prompt.AsCollectionStore(h.store)
	if cs == nil {
		return nil
	}
	cols, err := cs.ListCollections(ctx)
	if err != nil {
		slog.Warn("failed to list prompt collections", logKeyError, err)
		return nil
	}
	return cols
}

// mergeExtraScopes appends global and persona-scoped prompts for non-admin users.
func (h *Handle) mergeExtraScopes(ctx context.Context, prompts []prompt.Prompt, enabled *bool) []prompt.Prompt {
	globalPrompts, globalErr := h.store.List(ctx, prompt.ListFilter{
		Scope:   prompt.ScopeGlobal,
		Enabled: enabled,
	})
	if globalErr != nil {
		slog.Warn("failed to load global prompts", logKeyError, globalErr)
	} else {
		prompts = append(prompts, globalPrompts...)
	}

	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.PersonaName != "" {
		personaPrompts, personaErr := h.store.List(ctx, prompt.ListFilter{
			Scope:    prompt.ScopePersona,
			Personas: []string{pc.PersonaName},
			Enabled:  enabled,
		})
		if personaErr != nil {
			slog.Warn("failed to load persona prompts", logKeyError, personaErr)
		} else {
			prompts = append(prompts, personaPrompts...)
		}
	}
	return prompts
}

// handlePromptSearch ranks visible approved prompts by relevance to the query.
// Visibility is applied before ranking: a non-admin caller ranks over global,
// matching-persona, and their own personal approved prompts; an admin ranks
// over all approved prompts. Ranking is hybrid (semantic + lexical) when an
// embedding provider is configured and lexical-only otherwise, reported as the
// "ranking" field so the caller knows which path produced the results.
func (h *Handle) handlePromptSearch(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	searcher, ok := h.store.(prompt.Searcher)
	if !ok {
		return promptErrorResult("prompt search is unavailable: semantic discovery is not enabled"), nil, nil
	}

	query := strings.TrimSpace(input.Query)
	persona := ""
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		persona = pc.PersonaName
	}

	emb := embedding.EmbedForSearch(ctx, h.embedder, query)
	ranking := "lexical"
	if len(emb) > 0 {
		ranking = "hybrid"
	}

	scored, err := searcher.Search(ctx, prompt.SearchQuery{
		Embedding:  emb,
		QueryText:  query,
		OwnerEmail: resolveEmail(ctx),
		Persona:    persona,
		IsAdmin:    h.isAdminPersona(ctx),
		Scope:      input.Scope,
		Limit:      input.Limit,
	})
	if err != nil {
		slog.Error("failed to search prompts", promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, "failed to search prompts", err), nil, nil
	}

	ptrs := make([]*prompt.Prompt, len(scored))
	for i := range scored {
		ptrs[i] = &scored[i].Prompt
	}
	return h.browseResponse(ctx, ptrs, map[string]any{
		"prompts": scored,
		"count":   len(scored),
		"ranking": ranking,
	})
}

// handlePromptGet retrieves a single prompt by name.
func (h *Handle) handlePromptGet(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return promptErrorResult("name is required"), nil, nil
	}

	pr, err := h.resolveManagedPrompt(ctx, input.Name, resolveEmail(ctx), input.Scope, input.OwnerEmail)
	if err != nil {
		slog.Error(promptErrGet, promptLogKey, input.Name, promptLogKeyErr, err)
		return h.promptErrorDetail(ctx, promptErrGet, err), nil, nil
	}
	if pr == nil {
		return promptErrorResult(fmt.Sprintf("prompt %q not found", input.Name)), nil, nil
	}

	// Non-admins can only see their own personal prompts or global/persona prompts
	if !h.isAdminPersona(ctx) {
		email := resolveEmail(ctx)
		if pr.Scope == prompt.ScopePersonal && pr.OwnerEmail != email {
			return promptErrorResult("you can only view your own personal prompts"), nil, nil
		}
	}

	h.applyUsage(ctx, pr)
	return promptJSONResult(pr)
}

// ambiguousPersonalPromptError reports that more than one owner has a personal
// prompt of the addressed name, so an admin's owner-agnostic lookup cannot pick
// one. It carries the candidate owners so the caller can re-address with
// owner_email.
type ambiguousPersonalPromptError struct {
	name   string
	owners []string
}

func (e *ambiguousPersonalPromptError) Error() string {
	return fmt.Sprintf("multiple personal prompts are named %q (owners: %s); pass owner_email to target one",
		e.name, strings.Join(e.owners, ", "))
}

// foreignPersonalPromptError reports that a personal prompt of the addressed
// name exists but is owned by another user, so a non-admin caller may not reach
// it. It names the scope condition instead of collapsing to "not found"; the
// owner is deliberately withheld from non-admins.
type foreignPersonalPromptError struct {
	name string
}

func (e *foreignPersonalPromptError) Error() string {
	return fmt.Sprintf("a personal prompt named %q exists but is owned by another user; "+
		"you can only access your own personal prompts", e.name)
}

// resolveManagedPrompt finds the prompt a manage_prompt command targets by
// name. Personal names are unique only per owner, so by default the caller's own
// personal prompt takes precedence; otherwise a globally-unique global/persona
// prompt is returned. An explicit shared scope (global/persona) skips the
// personal lookup so a caller who owns a same-named personal prompt can still
// target the shared one.
//
// An admin is unrestricted by design: admin list already surfaces every owner's
// personal prompts, so the addressing verbs honor the same visibility. When the
// caller is admin and neither the own-personal nor the shared lookup matches, a
// personal prompt owned by any user resolves; owner disambiguates when more than
// one owner shares the name. A non-admin who names another owner's personal
// prompt gets an explicit scope error rather than a misleading "not found".
func (h *Handle) resolveManagedPrompt(ctx context.Context, name, email, scope, owner string) (*prompt.Prompt, error) {
	sharedOnly := scope == prompt.ScopeGlobal || scope == prompt.ScopePersona
	isAdmin := h.isAdminPersona(ctx)

	// An admin naming an owner commits to that owner's personal prompt: a miss is
	// a miss and must not silently resolve a different owner's same-named prompt.
	if isAdmin && owner != "" {
		return h.getPersonalPrompt(ctx, owner, name)
	}

	pr, err := h.resolveOwnOrShared(ctx, name, email, sharedOnly)
	if err != nil {
		return nil, err
	}
	if pr != nil || sharedOnly {
		return pr, nil
	}

	// Neither the caller's own personal prompt nor a shared prompt matched.
	return h.resolvePersonalAcrossOwners(ctx, name, isAdmin)
}

// getPersonalPrompt fetches an owner's personal prompt with the resolver's error
// context. Returns nil when no such prompt exists.
func (h *Handle) getPersonalPrompt(ctx context.Context, owner, name string) (*prompt.Prompt, error) {
	p, err := h.store.GetPersonal(ctx, owner, name)
	if err != nil {
		return nil, fmt.Errorf("resolving personal prompt: %w", err)
	}
	return p, nil
}

// resolveOwnOrShared returns the caller's own personal prompt, or the globally
// unique shared (global/persona) prompt of the name, or nil when neither exists.
// An explicit shared scope skips the personal lookup so a caller who owns a
// same-named personal prompt can still target the shared one.
func (h *Handle) resolveOwnOrShared(ctx context.Context, name, email string, sharedOnly bool) (*prompt.Prompt, error) {
	if email != "" && !sharedOnly {
		personal, err := h.getPersonalPrompt(ctx, email, name)
		if err != nil {
			return nil, err
		}
		if personal != nil {
			return personal, nil
		}
	}
	shared, err := h.store.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("resolving shared prompt: %w", err)
	}
	return shared, nil // nil when not found
}

// resolvePersonalAcrossOwners resolves a personal prompt by name regardless of
// owner, the lookup the default resolver cannot reach. For an admin it returns
// the single match, or an ambiguity error naming the owners when more than one
// exists. For a non-admin a match becomes an explicit foreign-prompt error
// (they may not reach it) rather than a misleading "not found". No match
// resolves to nil.
func (h *Handle) resolvePersonalAcrossOwners(ctx context.Context, name string, isAdmin bool) (*prompt.Prompt, error) {
	matches, err := h.store.ListPersonalByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("resolving personal prompt across owners: %w", err)
	}
	if len(matches) == 0 {
		return nil, nil //nolint:nilnil // resolver contract: nil, nil means not found
	}
	if !isAdmin {
		return nil, &foreignPersonalPromptError{name: name}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, &ambiguousPersonalPromptError{name: name, owners: personalPromptOwners(matches)}
}

// personalPromptOwners returns the distinct owner emails of the given prompts,
// in the order the store returned them.
func personalPromptOwners(prompts []prompt.Prompt) []string {
	owners := make([]string, 0, len(prompts))
	seen := make(map[string]struct{}, len(prompts))
	for i := range prompts {
		o := prompts[i].OwnerEmail
		if _, ok := seen[o]; ok {
			continue
		}
		seen[o] = struct{}{}
		owners = append(owners, o)
	}
	return owners
}

// resolveEmail returns the user email from context.
func resolveEmail(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.UserEmail != "" {
		return pc.UserEmail
	}
	return "anonymous"
}

// isAdminPersona checks if the current user has the admin persona.
func (h *Handle) isAdminPersona(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return false
	}
	return pc.PersonaName == h.adminPersona
}

// promptErrorDetail builds a tool error for a failed store or internal
// operation. The public message is always safe to show. Admins are the platform
// operators, so they additionally see the underlying error detail; non-admins
// get only a request-id breadcrumb so an operator can correlate the failure in
// the logs. Raw errors (which may carry SQL or schema detail) are never shown to
// non-admins. The full error is always written to the server log by the caller.
func (h *Handle) promptErrorDetail(ctx context.Context, public string, err error) *mcp.CallToolResult {
	// Resolution outcomes carry their own caller-safe message: an admin's
	// ambiguous owner lookup or a non-admin naming another owner's personal
	// prompt. Surface it verbatim instead of the generic "failed to ..." prefix,
	// and never through the admin-only raw-error branch below.
	var amb *ambiguousPersonalPromptError
	if errors.As(err, &amb) {
		return promptErrorResult(amb.Error())
	}
	var foreign *foreignPersonalPromptError
	if errors.As(err, &foreign) {
		return promptErrorResult(foreign.Error())
	}
	if h.isAdminPersona(ctx) {
		return promptErrorResult(fmt.Sprintf("%s: %v", public, err))
	}
	if pc := middleware.GetPlatformContext(ctx); pc != nil && pc.RequestID != "" {
		return promptErrorResult(fmt.Sprintf("%s (request_id: %s)", public, pc.RequestID))
	}
	return promptErrorResult(public)
}

// promptErrorResult creates an error tool result.
func promptErrorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}
}

// promptJSONResult creates a JSON tool result.
func promptJSONResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return promptErrorResult(fmt.Sprintf("failed to marshal result: %v", err)), nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}

// JSON schema key constants used in managePromptSchema.
const (
	schemaKeyType        = "type"        //nolint:revive // schema key constant
	schemaKeyDescription = "description" //nolint:revive // schema key constant
	schemaKeyItems       = "items"       //nolint:revive // schema key constant
	schemaKeyEnum        = "enum"        //nolint:revive // schema key constant
	schemaValString      = "string"      //nolint:revive // schema value constant
	schemaValArray       = "array"       //nolint:revive // schema value constant
)

// promotionRequestScopes are the shared scopes a personal prompt can request
// promotion into (every scope except personal). Built with append rather than a
// two-element composite literal, which a semgrep registry rule misflags as an
// unbounded make() capacity.
var promotionRequestScopes = append([]string{prompt.ScopePersona}, prompt.ScopeGlobal)

// managePromptSchema returns the JSON schema for the manage_prompt tool.
func managePromptSchema() any {
	schema := map[string]any{
		schemaKeyType: "object",
		"properties": map[string]any{
			"command": map[string]any{
				schemaKeyType: schemaValString,
				schemaKeyEnum: []string{
					"create", "update", "delete", cmdList, "get", cmdUse,
					cmdPatch, cmdLocate, cmdGetContent, cmdOutline, cmdStats, cmdDiff,
				},
				schemaKeyDescription: "The operation to perform. 'use' resolves any handle to a " +
					"ready-to-run prompt; prefer it when the user names a procedure or report. " +
					"'patch' edits part of a prompt's content without resending the whole body.",
			},
			fieldName: map[string]any{
				schemaKeyType: schemaValString,
				schemaKeyDescription: "Prompt name (required for create, update, delete, get, use, " +
					"patch, locate, get_content, outline, stats, diff). " +
					"For use it may also be a display name, an mcp:prompt:<id> reference, or free text.",
			},
			"display_name": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Human-readable display name",
			},
			schemaKeyDescription: map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Prompt description",
			},
			fieldContent: map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Prompt content template. Use {arg_name} for argument placeholders.",
			},
			"arguments": map[string]any{
				schemaKeyType: schemaValArray,
				schemaKeyItems: map[string]any{
					schemaKeyType: "object",
					"properties": map[string]any{
						fieldName:            map[string]any{schemaKeyType: schemaValString},
						schemaKeyDescription: map[string]any{schemaKeyType: schemaValString},
						"required":           map[string]any{schemaKeyType: "boolean"},
					},
				},
				schemaKeyDescription: "Prompt arguments with name, description, and required flag",
			},
			"category": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Organization category for grouping",
			},
			"scope": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyEnum:        []string{prompt.ScopeGlobal, prompt.ScopePersona, prompt.ScopePersonal},
				schemaKeyDescription: "Visibility scope. Non-admins can only use 'personal'.",
			},
			"owner_email": map[string]any{
				schemaKeyType: schemaValString,
				schemaKeyDescription: "Owner of a personal prompt to target by name (get, delete, update, " +
					"patch and the other content verbs). Admin only: lets an operator address or " +
					"disambiguate another user's personal prompt that admin list already shows. " +
					"Ignored for non-admins, who can only act on their own prompts.",
			},
			"personas": map[string]any{
				schemaKeyType:        schemaValArray,
				schemaKeyItems:       map[string]any{schemaKeyType: schemaValString},
				schemaKeyDescription: "Personas this prompt is assigned to. Defaults to empty list if omitted.",
			},
			"tags": map[string]any{
				schemaKeyType:        schemaValArray,
				schemaKeyItems:       map[string]any{schemaKeyType: schemaValString},
				schemaKeyDescription: "Free-form tags for organizing and searching prompts (create/update).",
			},
			"status": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyEnum:        []string{prompt.StatusDraft, prompt.StatusApproved, prompt.StatusDeprecated, prompt.StatusSuperseded},
				schemaKeyDescription: "Lifecycle status (update). Transitions: draft->approved->deprecated->superseded. Approval is admin-only.",
			},
			"superseded_by": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Name of the prompt that replaces this one (set when transitioning status to 'superseded').",
			},
			"search": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Substring filter on name, display name, and description (for list command).",
			},
			"query": map[string]any{
				schemaKeyType: schemaValString,
				schemaKeyDescription: "Free-text relevance query (for list command). Ranks visible approved " +
					"prompts by similarity to the query within your visibility. Takes precedence over 'search'.",
			},
			"limit": map[string]any{
				schemaKeyType:        "integer",
				schemaKeyDescription: "Max ranked results to return when 'query' is set (default 20).",
			},
			"args": map[string]any{
				schemaKeyType:          "object",
				"additionalProperties": map[string]any{schemaKeyType: schemaValString},
				schemaKeyDescription:   "Argument values for the 'use' command, substituted into the resolved prompt's content.",
			},
			"requested_scope": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyEnum:        promotionRequestScopes,
				schemaKeyDescription: "Request promotion of your personal prompt to this shared scope (update). Flags it for the admin review queue; an admin approves to apply it. Does not change the scope by itself.",
			},
			"requested_personas": map[string]any{
				schemaKeyType:        schemaValArray,
				schemaKeyItems:       map[string]any{schemaKeyType: schemaValString},
				schemaKeyDescription: "Target personas for a 'persona' promotion request (required when requested_scope is 'persona').",
			},
		},
		"required": []string{"command"},
	}
	addPatchProperties(schema)
	return schema
}

// addPatchProperties splices the shared textpatch grammar into the manage_prompt
// schema, so the patch and navigation arguments are the identical schema
// manage_asset advertises. A name manage_prompt already defines keeps its own
// wording.
func addPatchProperties(schema map[string]any) {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	for name, prop := range textpatch.PropertiesMap() {
		if _, exists := props[name]; !exists {
			props[name] = prop
		}
	}
}
