package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

const (
	promptErrGet    = "failed to get prompt"
	promptLogKey    = "name"
	promptLogKeyErr = "error"

	// Command names for the manage_prompt tool.
	cmdList = "list"

	// JSON field names used in result and schema maps. These share the
	// same string value as promptLogKey/promptLogKeyErr but are kept
	// separate for documentation clarity at call sites.
	fieldName    = "name"
	fieldContent = "content"
	// fieldStatus is the JSON result key for command-status strings
	// ("created", "updated", "deleted") returned by manage_prompt.
	fieldStatus = "status"
)

// platformPromptCreator adapts the prompt store and platform for the
// knowledge toolkit's PromptCreator interface.
type platformPromptCreator struct {
	store    prompt.Store
	platform *Platform
}

// Create delegates prompt creation to the backing store.
func (c *platformPromptCreator) Create(ctx context.Context, p *prompt.Prompt) error {
	if err := c.store.Create(ctx, p); err != nil {
		return fmt.Errorf("prompt store create: %w", err)
	}
	return nil
}

// RegisterRuntimePrompt delegates runtime registration to the platform.
func (c *platformPromptCreator) RegisterRuntimePrompt(p *prompt.Prompt) {
	c.platform.RegisterRuntimePrompt(p)
}

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
	Personas     []string          `json:"personas,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Status       string            `json:"status,omitempty"`
	SupersededBy string            `json:"superseded_by,omitempty"`
	Search       string            `json:"search,omitempty"`
}

// registerPromptTool registers the manage_prompt tool with the MCP server.
func (p *Platform) registerPromptTool() {
	if p.promptStore == nil {
		return
	}

	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:  "manage_prompt",
		Title: "Manage Prompts",
		Description: "Create, update, delete, list, or get prompts. " +
			"Non-admin users can manage their own personal prompts. " +
			"Admins can manage prompts at all scope levels (global, persona, personal). " +
			"This tool manages database-stored prompts only; static prompts from server " +
			"configuration are not listed or editable here.",
		InputSchema: managePromptSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input managePromptInput) (*mcp.CallToolResult, any, error) {
		return p.handleManagePrompt(ctx, input)
	})
}

// handleManagePrompt dispatches manage_prompt commands.
func (p *Platform) handleManagePrompt(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	switch input.Command {
	case "create":
		return p.handlePromptCreate(ctx, input)
	case "update":
		return p.handlePromptUpdate(ctx, input)
	case "delete":
		return p.handlePromptDelete(ctx, input)
	case cmdList:
		return p.handlePromptList(ctx, input)
	case "get":
		return p.handlePromptGet(ctx, input)
	default:
		return promptErrorResult(fmt.Sprintf("unknown command: %s", input.Command)), nil, nil
	}
}

// handlePromptCreate creates a new prompt.
func (p *Platform) handlePromptCreate(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
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
	if !p.isAdminPersona(ctx) && scope != prompt.ScopePersonal {
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

	if err := p.promptStore.Create(ctx, pr); err != nil {
		slog.Error("failed to create prompt", promptLogKey, input.Name, promptLogKeyErr, err)
		return promptErrorResult("failed to create prompt"), nil, nil
	}

	p.RegisterRuntimePrompt(pr)

	return promptJSONResult(map[string]any{
		fieldStatus: "created",
		"id":        pr.ID,
		fieldName:   pr.Name,
	})
}

// handlePromptUpdate updates an existing prompt.
func (p *Platform) handlePromptUpdate(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return promptErrorResult("name is required"), nil, nil
	}

	existing, err := p.resolveManagedPrompt(ctx, input.Name, resolveEmail(ctx), input.Scope)
	if err != nil {
		slog.Error(promptErrGet, promptLogKey, input.Name, promptLogKeyErr, err)
		return promptErrorResult(promptErrGet), nil, nil
	}
	if existing == nil {
		return promptErrorResult(fmt.Sprintf("prompt %q not found", input.Name)), nil, nil
	}

	email := resolveEmail(ctx)
	if !p.isAdminPersona(ctx) {
		if existing.Scope != prompt.ScopePersonal {
			return promptErrorResult("non-admins can only manage personal prompts"), nil, nil
		}
		if existing.OwnerEmail != email {
			return promptErrorResult("you can only update your own prompts"), nil, nil
		}
	}

	oldScope := existing.Scope
	if errMsg := applyPromptUpdates(existing, input, p.isAdminPersona(ctx)); errMsg != "" {
		return promptErrorResult(errMsg), nil, nil
	}
	if errMsg := applyStatusTransition(existing, input.Status, input.SupersededBy, email, p.isAdminPersona(ctx)); errMsg != "" {
		return promptErrorResult(errMsg), nil, nil
	}
	// Promoting a personal prompt into the shared (global/persona) namespace
	// requires a name that is free there; the shared names are globally unique.
	if oldScope == prompt.ScopePersonal && existing.Scope != prompt.ScopePersonal {
		if dup, _ := p.promptStore.Get(ctx, existing.Name); dup != nil && dup.ID != existing.ID {
			return promptErrorResult(fmt.Sprintf(
				"the name %q is already used by a %s prompt; rename before promoting", existing.Name, dup.Scope)), nil, nil
		}
	}

	if err := p.promptStore.Update(ctx, existing); err != nil {
		slog.Error("failed to update prompt", promptLogKey, input.Name, promptLogKeyErr, err)
		return promptErrorResult("failed to update prompt"), nil, nil
	}

	// Re-register the name-keyed metadata. Personal prompts are not tracked
	// there (names collide across owners), so only (un)register shared scopes;
	// RegisterRuntimePrompt self-skips personal, and unregistering the old name
	// is gated on the old scope to avoid dropping an unrelated shared entry.
	if oldScope != prompt.ScopePersonal {
		p.UnregisterRuntimePrompt(existing.Name)
	}
	p.RegisterRuntimePrompt(existing)

	return promptJSONResult(map[string]any{
		fieldStatus: "updated",
		fieldName:   existing.Name,
	})
}

// applyPromptUpdates applies non-empty fields from input to existing.
// Returns a non-empty error message if a scope check fails.
func applyPromptUpdates(existing *prompt.Prompt, input managePromptInput, isAdmin bool) string {
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
	if input.Scope != "" {
		if !isAdmin && input.Scope != prompt.ScopePersonal {
			return "only admins can set global or persona scope"
		}
		existing.Scope = input.Scope
	}
	if input.Personas != nil {
		existing.Personas = input.Personas
	}
	if input.Tags != nil {
		if err := prompt.ValidateTags(input.Tags); err != nil {
			return err.Error()
		}
		existing.Tags = input.Tags
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
func (p *Platform) handlePromptDelete(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return promptErrorResult("name is required"), nil, nil
	}

	existing, err := p.resolveManagedPrompt(ctx, input.Name, resolveEmail(ctx), input.Scope)
	if err != nil {
		slog.Error(promptErrGet, promptLogKey, input.Name, promptLogKeyErr, err)
		return promptErrorResult(promptErrGet), nil, nil
	}
	if existing == nil {
		return promptErrorResult(fmt.Sprintf("prompt %q not found", input.Name)), nil, nil
	}

	email := resolveEmail(ctx)
	if !p.isAdminPersona(ctx) {
		if existing.Scope != prompt.ScopePersonal {
			return promptErrorResult("non-admins can only manage personal prompts"), nil, nil
		}
		if existing.OwnerEmail != email {
			return promptErrorResult("you can only delete your own prompts"), nil, nil
		}
	}

	if err := p.promptStore.DeleteByID(ctx, existing.ID); err != nil {
		slog.Error("failed to delete prompt", promptLogKey, input.Name, promptLogKeyErr, err)
		return promptErrorResult("failed to delete prompt"), nil, nil
	}

	// Personal prompts are not tracked in the name-keyed metadata; unregistering
	// by name would drop an unrelated shared entry of the same name.
	if existing.Scope != prompt.ScopePersonal {
		p.UnregisterRuntimePrompt(existing.Name)
	}

	return promptJSONResult(map[string]any{
		fieldStatus: "deleted",
		fieldName:   input.Name,
	})
}

// handlePromptList lists prompts visible to the current user.
func (p *Platform) handlePromptList(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	filter := prompt.ListFilter{
		Scope:  input.Scope,
		Search: input.Search,
	}

	isAdmin := p.isAdminPersona(ctx)
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

	prompts, err := p.promptStore.List(ctx, filter)
	if err != nil {
		slog.Error("failed to list prompts", promptLogKeyErr, err)
		return promptErrorResult("failed to list prompts"), nil, nil
	}

	// For non-admins without an explicit scope, also include global and persona-scoped prompts.
	if !isAdmin && input.Scope == "" {
		prompts = p.mergeExtraScopes(ctx, prompts, &enabled)
	}

	return promptJSONResult(map[string]any{
		"prompts": prompts,
		"count":   len(prompts),
	})
}

// mergeExtraScopes appends global and persona-scoped prompts for non-admin users.
func (p *Platform) mergeExtraScopes(ctx context.Context, prompts []prompt.Prompt, enabled *bool) []prompt.Prompt {
	globalPrompts, globalErr := p.promptStore.List(ctx, prompt.ListFilter{
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
		personaPrompts, personaErr := p.promptStore.List(ctx, prompt.ListFilter{
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

// handlePromptGet retrieves a single prompt by name.
func (p *Platform) handlePromptGet(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return promptErrorResult("name is required"), nil, nil
	}

	pr, err := p.resolveManagedPrompt(ctx, input.Name, resolveEmail(ctx), input.Scope)
	if err != nil {
		slog.Error(promptErrGet, promptLogKey, input.Name, promptLogKeyErr, err)
		return promptErrorResult(promptErrGet), nil, nil
	}
	if pr == nil {
		return promptErrorResult(fmt.Sprintf("prompt %q not found", input.Name)), nil, nil
	}

	// Non-admins can only see their own personal prompts or global/persona prompts
	if !p.isAdminPersona(ctx) {
		email := resolveEmail(ctx)
		if pr.Scope == prompt.ScopePersonal && pr.OwnerEmail != email {
			return promptErrorResult("you can only view your own personal prompts"), nil, nil
		}
	}

	return promptJSONResult(pr)
}

// resolveManagedPrompt finds the prompt a manage_prompt command targets by
// name. Personal names are unique only per owner, so by default the caller's own
// personal prompt takes precedence; otherwise a globally-unique global/persona
// prompt is returned. An explicit shared scope (global/persona) skips the
// personal lookup so a caller who owns a same-named personal prompt can still
// target the shared one.
func (p *Platform) resolveManagedPrompt(ctx context.Context, name, email, scope string) (*prompt.Prompt, error) {
	sharedOnly := scope == prompt.ScopeGlobal || scope == prompt.ScopePersona
	if email != "" && !sharedOnly {
		personal, err := p.promptStore.GetPersonal(ctx, email, name)
		if err != nil {
			return nil, fmt.Errorf("resolving personal prompt: %w", err)
		}
		if personal != nil {
			return personal, nil
		}
	}
	shared, err := p.promptStore.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("resolving shared prompt: %w", err)
	}
	return shared, nil
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
func (p *Platform) isAdminPersona(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return false
	}
	return pc.PersonaName == p.config.Admin.Persona
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
	schemaValString      = "string"      //nolint:revive // schema value constant
)

// managePromptSchema returns the JSON schema for the manage_prompt tool.
func managePromptSchema() any {
	schema := map[string]any{
		schemaKeyType: "object",
		"properties": map[string]any{
			"command": map[string]any{
				schemaKeyType:        schemaValString,
				"enum":               []string{"create", "update", "delete", cmdList, "get"},
				schemaKeyDescription: "The operation to perform",
			},
			fieldName: map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Prompt name (required for create, update, delete, get)",
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
				schemaKeyType: "array",
				"items": map[string]any{
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
				"enum":               []string{prompt.ScopeGlobal, prompt.ScopePersona, prompt.ScopePersonal},
				schemaKeyDescription: "Visibility scope. Non-admins can only use 'personal'.",
			},
			"personas": map[string]any{
				schemaKeyType:        "array",
				"items":              map[string]any{schemaKeyType: schemaValString},
				schemaKeyDescription: "Personas this prompt is assigned to. Defaults to empty list if omitted.",
			},
			"tags": map[string]any{
				schemaKeyType:        "array",
				"items":              map[string]any{schemaKeyType: schemaValString},
				schemaKeyDescription: "Free-form tags for organizing and searching prompts (create/update).",
			},
			"status": map[string]any{
				schemaKeyType:        schemaValString,
				"enum":               []string{prompt.StatusDraft, prompt.StatusApproved, prompt.StatusDeprecated, prompt.StatusSuperseded},
				schemaKeyDescription: "Lifecycle status (update). Transitions: draft->approved->deprecated->superseded. Approval is admin-only.",
			},
			"superseded_by": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Name of the prompt that replaces this one (set when transitioning status to 'superseded').",
			},
			"search": map[string]any{
				schemaKeyType:        schemaValString,
				schemaKeyDescription: "Free-text search filter (for list command)",
			},
		},
		"required": []string{"command"},
	}
	return schema
}
