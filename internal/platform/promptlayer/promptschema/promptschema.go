// Package promptschema builds the manage_prompt input schema.
//
// It is a separate package for the reason the structural gate exists: the
// prompt layer is at its size ceiling, and the tool schema is the piece that
// comes out whole — a pure function over the command set, with no store, no
// context, and no behavior. Extracting it also closes a drift seam: the command
// enum is now DERIVED from the dispatch table the caller passes in, so a command
// the layer handles can no longer be missing from the schema an agent reads.
package promptschema

import (
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// JSON schema keys and values, exported because the sibling show_prompts schema
// is written in the same vocabulary.
const (
	KeyType        = "type"
	KeyDescription = "description"
	KeyItems       = "items"
	KeyEnum        = "enum"
	ValString      = "string"
	ValArray       = "array"
)

// Field names the schema advertises, matching the JSON tags of the tool's input
// struct (the drift between the two is what TestPromptSchemas_ClosedAndInSyncWithInputStructs
// holds).
const (
	fieldName    = "name"
	fieldContent = "content"
	fieldScript  = "script"
)

// promotionRequestScopes are the shared scopes a personal prompt can request
// promotion into (every scope except personal). Built with append rather than a
// two-element composite literal, which a semgrep registry rule misflags as an
// unbounded make() capacity.
var promotionRequestScopes = append([]string{prompt.ScopePersona}, prompt.ScopeGlobal)

// ManagePrompt returns the JSON schema for the manage_prompt tool. commands is
// the set the tool dispatches, sorted here so the advertised enum is
// deterministic and can never omit a command the layer handles.
func ManagePrompt(commands []string) any {
	enum := append([]string(nil), commands...)
	sort.Strings(enum)
	schema := map[string]any{
		KeyType: "object",
		"properties": map[string]any{
			"command": map[string]any{
				KeyType: ValString,
				KeyEnum: enum,
				KeyDescription: "The operation to perform. 'use' resolves any handle to a " +
					"ready-to-run prompt; prefer it when the user names a procedure or report. " +
					"'patch' edits part of a prompt's content without resending the whole body. " +
					"'attach_script' references a managed script from a prompt, so serving the prompt " +
					"carries that script's contract and latest results.",
			},
			fieldName: map[string]any{
				KeyType: ValString,
				KeyDescription: "Prompt name (required for create, update, delete, get, use, " +
					"patch, locate, get_content, outline, stats, diff). " +
					"For use it may also be a display name, an mcp:prompt:<id> reference, or free text.",
			},
			"display_name": map[string]any{
				KeyType:        ValString,
				KeyDescription: "Human-readable display name",
			},
			fieldScript: map[string]any{
				KeyType: ValString,
				KeyDescription: "Managed script to reference (required for attach_script and " +
					"detach_script): the mcp:script:<id> reference search and fetch return, or a bare " +
					"script id from manage_script. A referenced script must be at least as widely " +
					"visible as the prompt carrying it.",
			},
			KeyDescription: map[string]any{
				KeyType:        ValString,
				KeyDescription: "Prompt description",
			},
			fieldContent: map[string]any{
				KeyType:        ValString,
				KeyDescription: "Prompt content template. Use {arg_name} for argument placeholders.",
			},
			"arguments": map[string]any{
				KeyType: ValArray,
				KeyItems: map[string]any{
					KeyType: "object",
					"properties": map[string]any{
						fieldName:      map[string]any{KeyType: ValString},
						KeyDescription: map[string]any{KeyType: ValString},
						"required":     map[string]any{KeyType: "boolean"},
					},
				},
				KeyDescription: "Prompt arguments with name, description, and required flag",
			},
			"category": map[string]any{
				KeyType:        ValString,
				KeyDescription: "Organization category for grouping",
			},
			"scope": map[string]any{
				KeyType:        ValString,
				KeyEnum:        []string{prompt.ScopeGlobal, prompt.ScopePersona, prompt.ScopePersonal},
				KeyDescription: "Visibility scope. Non-admins can only use 'personal'.",
			},
			"owner_email": map[string]any{
				KeyType: ValString,
				KeyDescription: "Owner of a personal prompt to target by name (get, delete, update, " +
					"patch and the other content verbs). Admin only: lets an operator address or " +
					"disambiguate another user's personal prompt that admin list already shows. " +
					"Ignored for non-admins, who can only act on their own prompts.",
			},
			"personas": map[string]any{
				KeyType:        ValArray,
				KeyItems:       map[string]any{KeyType: ValString},
				KeyDescription: "Personas this prompt is assigned to. Defaults to empty list if omitted.",
			},
			"tags": map[string]any{
				KeyType:        ValArray,
				KeyItems:       map[string]any{KeyType: ValString},
				KeyDescription: "Free-form tags for organizing and searching prompts (create/update).",
			},
			"status": map[string]any{
				KeyType:        ValString,
				KeyEnum:        []string{prompt.StatusDraft, prompt.StatusApproved, prompt.StatusDeprecated, prompt.StatusSuperseded},
				KeyDescription: "Lifecycle status (update). Transitions: draft->approved->deprecated->superseded. Approval is admin-only.",
			},
			"superseded_by": map[string]any{
				KeyType:        ValString,
				KeyDescription: "Name of the prompt that replaces this one (set when transitioning status to 'superseded').",
			},
			"search": map[string]any{
				KeyType:        ValString,
				KeyDescription: "Substring filter on name, display name, and description (for list command).",
			},
			"query": map[string]any{
				KeyType: ValString,
				KeyDescription: "Free-text relevance query (for list command). Ranks the prompts visible " +
					"to you (approved shared prompts plus your own) by similarity to the query. " +
					"Takes precedence over 'search'.",
			},
			"limit": map[string]any{
				KeyType:        "integer",
				KeyDescription: "Max ranked results to return when 'query' is set (default 20).",
			},
			"args": map[string]any{
				KeyType:                "object",
				"additionalProperties": map[string]any{KeyType: ValString},
				KeyDescription:         "Argument values for the 'use' command, substituted into the resolved prompt's content.",
			},
			"requested_scope": map[string]any{
				KeyType:        ValString,
				KeyEnum:        promotionRequestScopes,
				KeyDescription: "Request promotion of your personal prompt to this shared scope (update). Flags it for the admin review queue; an admin approves to apply it. Does not change the scope by itself.",
			},
			"collection_id": map[string]any{
				KeyType: ValString,
				KeyDescription: "Collection to place the prompt in (create/update): an id from the " +
					"'collections' array returned by list. An empty string clears the placement. " +
					"Collections themselves are created and managed in the portal.",
			},
			"requested_personas": map[string]any{
				KeyType:        ValArray,
				KeyItems:       map[string]any{KeyType: ValString},
				KeyDescription: "Target personas for a 'persona' promotion request (required when requested_scope is 'persona').",
			},
		},
		"required":             []string{"command"},
		"additionalProperties": false,
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
