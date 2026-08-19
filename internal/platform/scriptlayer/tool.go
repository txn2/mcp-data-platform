package scriptlayer

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// ToolNameManageScript is the MCP tool name of the script-management tool,
// exported for composition roots that bind UI apps to it.
const ToolNameManageScript = "manage_script"

// Command names.
const (
	cmdCreate     = "create"
	cmdUpdate     = "update"
	cmdDelete     = "delete"
	cmdGet        = "get"
	cmdList       = "list"
	cmdValidate   = "validate"
	cmdRunDraft   = "run_draft"
	cmdHelp       = "help"
	cmdPatch      = "patch"
	cmdLocate     = "locate"
	cmdGetContent = "get_content"
	cmdOutline    = "outline"
	cmdStats      = "stats"
	cmdDiff       = "diff"
	cmdRuns       = "runs"
	cmdGetRun     = "get_run"

	cmdScheduleSet     = "schedule_set"
	cmdScheduleList    = "schedule_list"
	cmdScheduleEnable  = "schedule_enable"
	cmdScheduleDisable = "schedule_disable"
)

// JSON field names shared between the schema and result maps.
const (
	fieldName    = "name"
	fieldStatus  = "status"
	fieldVersion = "version"
	fieldSource  = "source"
)

// manageScriptInput is the input schema for the manage_script tool. Every field
// here is published by manageScriptSchema and vice versa; the pairing is pinned
// by a test, because a field the schema advertises but the struct drops is
// silently ignored input (#1057).
type manageScriptInput struct {
	Command string `json:"command"`

	Name        string         `json:"name,omitempty"`
	DisplayName string         `json:"display_name,omitempty"`
	Description string         `json:"description,omitempty"`
	Source      string         `json:"source,omitempty"`
	Params      []script.Param `json:"params,omitempty"`
	Scope       string         `json:"scope,omitempty"`
	Personas    []string       `json:"personas,omitempty"`
	OwnerEmail  string         `json:"owner_email,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	// Enabled is a pointer so "not sent" (leave it alone) is distinct from
	// false (disable it).
	Enabled      *bool  `json:"enabled,omitempty"`
	Status       string `json:"status,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`

	// List filters.
	Search string `json:"search,omitempty"`
	Limit  int    `json:"limit,omitempty"`

	// Args binds parameter values for run_draft, checked against the script's
	// declared params before the run starts.
	Args map[string]any `json:"args,omitempty"`

	// Cron and Timezone carry the cadence for schedule_set. Args carries the
	// schedule's bound parameter values — the same vocabulary run_draft binds,
	// which is why it is the same argument, with the addition that a schedule's
	// values may contain the ${fire_date} token.
	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone,omitempty"`

	// RunID names one run for get_run, and RunStatus filters the runs listing.
	// RunStatus is separate from Status because the two are different
	// vocabularies: Status is a lifecycle transition to APPLY to the script,
	// while this selects runs already recorded.
	RunID     string `json:"run_id,omitempty"`
	RunStatus string `json:"run_status,omitempty"`

	// Content editing and navigation arguments, shared verbatim with
	// manage_prompt and manage_asset through pkg/textpatch.
	Edits        []textpatch.Edit `json:"edits,omitempty"`
	BaseVersion  int              `json:"base_version,omitempty"`
	DryRun       bool             `json:"dry_run,omitempty"`
	Find         string           `json:"find,omitempty"`
	Pattern      string           `json:"pattern,omitempty"`
	Section      string           `json:"section,omitempty"`
	Selector     string           `json:"selector,omitempty"`
	Occurrence   string           `json:"occurrence,omitempty"`
	LineStart    int              `json:"line_start,omitempty"`
	LineEnd      int              `json:"line_end,omitempty"`
	ContextBytes int              `json:"context_bytes,omitempty"`
	FromVersion  int              `json:"from_version,omitempty"`
	ToVersion    int              `json:"to_version,omitempty"`
}

// RegisterTool registers manage_script; where the deployment can execute
// approved versions, run_script; and the presentation-only show_scripts. It
// also captures the server the two run paths open their in-memory sessions
// against. No-op on a nil Handle or a no-database deployment (there is nowhere
// to keep a script).
func (h *Handle) RegisterTool(server *mcp.Server) {
	if h == nil || h.store == nil || server == nil {
		return
	}
	h.server = server

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolNameManageScript,
		Title:       "Manage Scripts",
		Description: manageScriptDescription,
		InputSchema: manageScriptSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input manageScriptInput) (*mcp.CallToolResult, any, error) {
		return h.handleManageScript(ctx, input)
	})
	h.registerRunScript(server)
	h.registerShowScripts(server)
}

// commandHandler handles one manage_script command.
type commandHandler func(context.Context, manageScriptInput) (*mcp.CallToolResult, any, error)

// commands is the dispatch table, built per call because the handlers are bound
// to the receiver. A table rather than a switch keeps dispatch flat as the
// command set grows, mirroring manage_prompt.
func (h *Handle) commands() map[string]commandHandler {
	return map[string]commandHandler{
		cmdCreate:     h.handleCreate,
		cmdUpdate:     h.handleUpdate,
		cmdDelete:     h.handleDelete,
		cmdGet:        h.handleGet,
		cmdList:       h.handleList,
		cmdValidate:   h.handleValidate,
		cmdRunDraft:   h.handleRunDraft,
		cmdHelp:       h.handleHelp,
		cmdPatch:      h.handlePatch,
		cmdLocate:     h.handleLocate,
		cmdGetContent: h.handleGetContent,
		cmdOutline:    h.handleOutline,
		cmdStats:      h.handleStats,
		cmdDiff:       h.handleDiff,
		cmdRuns:       h.handleRuns,
		cmdGetRun:     h.handleGetRun,

		cmdScheduleSet:     h.handleScheduleSet,
		cmdScheduleList:    h.handleScheduleList,
		cmdScheduleEnable:  h.handleScheduleEnable,
		cmdScheduleDisable: h.handleScheduleDisable,
	}
}

// handleManageScript dispatches manage_script commands.
func (h *Handle) handleManageScript(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	handler, ok := h.commands()[input.Command]
	if !ok {
		return errorResult(fmt.Sprintf("unknown command: %s", input.Command)), nil, nil
	}
	return handler(ctx, input)
}

// errorResult builds a tool error result carrying a caller-safe message.
func errorResult(msg string) *mcp.CallToolResult {
	result := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
	return result
}

// jsonResult creates a JSON tool result.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to marshal result: %v", err)), nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}

// identity is the script half of every content-verb response; the body half
// comes from pkg/textpatch, so manage_script, manage_prompt, and manage_asset
// answer the same shape for the same question.
func identity(sc *script.Script) map[string]any {
	return map[string]any{
		fieldName:    sc.Name,
		fieldVersion: sc.Version,
		fieldStatus:  sc.Status,
	}
}

// JSON schema key constants used in manageScriptSchema.
const (
	keyType        = "type"
	keyDescription = "description"
	keyEnum        = "enum"
	keyItems       = "items"
	valString      = "string"
	valArray       = "array"
	valObject      = "object"
	valInteger     = "integer"
	valBoolean     = "boolean"
)

// manageScriptSchema is the closed input schema. It is written out rather than
// generated so the command enum, the argument descriptions, and the shared
// textpatch grammar all reach the model, and so unknown arguments are refused
// instead of silently dropped (#1057).
func manageScriptSchema() any {
	props := map[string]any{
		"command": map[string]any{
			keyType: valString,
			keyEnum: []string{
				cmdCreate, cmdUpdate, cmdDelete, cmdGet, cmdList, cmdValidate,
				cmdRunDraft, cmdHelp, cmdPatch, cmdLocate, cmdGetContent,
				cmdOutline, cmdStats, cmdDiff, cmdRuns, cmdGetRun,
				cmdScheduleSet, cmdScheduleList, cmdScheduleEnable, cmdScheduleDisable,
			},
			keyDescription: "The operation to perform. Call 'help' first if you have not written a " +
				"script for this platform before: it states the dialect and what is available.",
		},
		fieldName: map[string]any{
			keyType:        valString,
			keyDescription: "Script name (lowercase letters, digits, hyphens, underscores).",
		},
		"display_name": map[string]any{keyType: valString, keyDescription: "Human-readable display name."},
		keyDescription: map[string]any{keyType: valString, keyDescription: "What the script produces."},
		fieldSource: map[string]any{
			keyType:        valString,
			keyDescription: "The Starlark source. Call 'help' for the dialect contract and worked examples.",
		},
		"params": map[string]any{
			keyType: valArray,
			keyDescription: "Typed parameter contract: {name, type: string|int|float|bool|date|enum|connection, required, default, description, values}. " +
				"A connection parameter takes the name of a platform connection; the surfaces that ask for one offer the set this script may reach.",
			keyItems: map[string]any{keyType: valObject},
		},
		"scope": map[string]any{
			keyType: valString, keyEnum: []string{script.ScopeGlobal, script.ScopePersona, script.ScopePersonal},
			keyDescription: "Visibility. Defaults to personal; only admins create shared scripts.",
		},
		"personas": map[string]any{
			keyType: valArray, keyItems: map[string]any{keyType: valString},
			keyDescription: "Personas a persona-scoped script is visible to.",
		},
		"owner_email":   map[string]any{keyType: valString, keyDescription: "Owner of the script; admins use it to address another owner's personal script."},
		"tags":          map[string]any{keyType: valArray, keyItems: map[string]any{keyType: valString}, keyDescription: "Free-form tags."},
		"enabled":       map[string]any{keyType: valBoolean, keyDescription: "Whether the script is available."},
		fieldStatus:     map[string]any{keyType: valString, keyEnum: []string{script.StatusActive, script.StatusDeprecated, script.StatusSuperseded}, keyDescription: "Lifecycle transition to apply."},
		"superseded_by": map[string]any{keyType: valString, keyDescription: "Name of the replacing script when superseding."},
		"search":        map[string]any{keyType: valString, keyDescription: "Substring filter for list."},
		"limit":         map[string]any{keyType: valInteger, keyDescription: "Maximum rows for list, or matches for locate."},
		"args": map[string]any{
			keyType: valObject,
			keyDescription: "Parameter values for run_draft, or the bound values a schedule fires with, " +
				"checked against the script's declared params. A schedule's value may contain " +
				script.FireDateToken + ", which expands to the date of the fire in the schedule's timezone.",
		},
		"cron": map[string]any{
			keyType: valString,
			keyDescription: "Cadence for schedule_set: a standard five-field cron expression " +
				"(\"0 7 * * 1-5\" is 07:00 on weekdays) or a descriptor (@daily, @hourly, @every 30m). " +
				"A schedule may fire at most once a minute.",
		},
		"timezone": map[string]any{
			keyType:        valString,
			keyDescription: "IANA timezone the cron expression is read in (default UTC), for example America/Los_Angeles.",
		},
		"run_id": map[string]any{
			keyType:        valString,
			keyDescription: "Identifies one run for get_run; run_script and the runs listing report it.",
		},
		"run_status": map[string]any{
			keyType: valString,
			keyEnum: []string{
				script.RunStatusPending, script.RunStatusRunning,
				script.RunStatusSucceeded, script.RunStatusFailed,
			},
			keyDescription: "Filters the runs listing to one run status.",
		},
	}
	maps.Copy(props, textpatchProperties())
	return map[string]any{
		keyType:                valObject,
		"properties":           props,
		"required":             []string{"command"},
		"additionalProperties": false,
	}
}

// textpatchProperties publishes the shared content-editing grammar. It is kept
// separate so the manage_script schema and the input struct stay in step with
// pkg/textpatch rather than with a hand-copied list.
func textpatchProperties() map[string]any {
	return map[string]any{
		"edits": map[string]any{
			keyType: valArray, keyItems: map[string]any{keyType: valObject},
			keyDescription: "Anchored edits for patch. " + textpatch.VerbsDescription,
		},
		"base_version":  map[string]any{keyType: valInteger, keyDescription: "Version the edits were written against; the patch is refused if the script moved on."},
		"dry_run":       map[string]any{keyType: valBoolean, keyDescription: "Report what patch would do without saving it."},
		"find":          map[string]any{keyType: valString, keyDescription: "Literal anchor for locate."},
		"pattern":       map[string]any{keyType: valString, keyDescription: "Regular-expression anchor for locate."},
		"section":       map[string]any{keyType: valString, keyDescription: "Section to scope a content verb to."},
		"selector":      map[string]any{keyType: valString, keyDescription: "Structural selector to scope a content verb to."},
		"occurrence":    map[string]any{keyType: valString, keyDescription: "Which match to act on when a selector matches more than once."},
		"line_start":    map[string]any{keyType: valInteger, keyDescription: "First line for get_content."},
		"line_end":      map[string]any{keyType: valInteger, keyDescription: "Last line for get_content."},
		"context_bytes": map[string]any{keyType: valInteger, keyDescription: "Bytes of surrounding context to return with each locate match."},
		"from_version":  map[string]any{keyType: valInteger, keyDescription: "Older version for diff."},
		"to_version":    map[string]any{keyType: valInteger, keyDescription: "Newer version for diff."},
	}
}
