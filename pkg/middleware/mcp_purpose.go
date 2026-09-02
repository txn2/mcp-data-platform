package middleware

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrCategoryPurposeRequired is the error category for a gated data-access call
// that carried no purpose (issue #1317).
const ErrCategoryPurposeRequired = "purpose_required"

// purposeArg is the tool-call argument name carrying the caller's stated reason
// for the call. It is named purpose, not intent, because search already takes an
// intent argument that is the query text and keeps that meaning.
const purposeArg = "purpose"

// purposeSchemaDescription is the description advertised on the injected purpose
// property. It is the whole contract the model sees, so it says both what to
// write and what not to.
const purposeSchemaDescription = "One sentence: the wider task you are working on and why this call serves it. " +
	"Do not repeat argument values; no PII or secrets."

// maxPurposeChars bounds a recorded purpose. A purpose is one sentence of prose;
// anything past this is not a purpose, and an unbounded agent-supplied string
// would flow verbatim into every audit row. Over-long values are truncated
// rather than refused: the call is legitimate, only the prose is out of contract.
const maxPurposeChars = 1000

// purposeKindPrefix marks an entry in the configured tool set that names a
// TOOLKIT KIND rather than a tool-name glob. It exists because one member of the
// default set cannot be written as a glob: every tool an MCP gateway connection
// proxies from an upstream server, whose names are chosen upstream and change
// when the upstream does. "kind:mcp" gates all of them, now and after the
// upstream adds a tool.
const purposeKindPrefix = "kind:"

// defaultPurposeTools is the tool set the purpose argument is advertised and
// enforced on when purpose.tools is unset: the data-access surface, where "why
// did this call happen" is a question an operator actually asks of a stored row.
// Orientation and platform-management tools (platform_info, list_connections,
// platform_find_tools, memory_*, manage_*, save_asset) are deliberately absent —
// their purpose is their name, and gating them would tax every call the agent
// makes to set itself up.
var defaultPurposeTools = []string{
	"search",
	"fetch",
	"trino_query",
	"trino_execute",
	"trino_export",
	"trino_describe_table",
	"api_invoke_endpoint",
	"api_export",
	"datahub_get_*",
	"s3_object",
	"s3_list",
	purposeKindPrefix + "mcp",
}

// DefaultPurposeTools returns a copy of the default purpose tool set, for the
// config layer to fall back to and for the docs check to render.
func DefaultPurposeTools() []string {
	out := make([]string, len(defaultPurposeTools))
	copy(out, defaultPurposeTools)
	return out
}

// PurposeConfig configures a PurposeResolver.
type PurposeConfig struct {
	// Enabled activates schema advertisement, stripping, and recording. When
	// false the resolver is a no-op and no tool advertises a purpose argument.
	Enabled bool

	// Require refuses a gated call that states no purpose. When false the
	// purpose is still advertised, stripped, and recorded when present, but its
	// absence never refuses a call.
	Require bool

	// Tools is the gated set: tool-name globs (filepath.Match semantics) plus
	// "kind:<toolkit-kind>" entries that gate every tool a toolkit of that kind
	// serves. Empty means defaultPurposeTools.
	Tools []string

	// Lookup resolves a tool's toolkit so "kind:" entries can be evaluated. May
	// be nil, in which case only the name globs apply.
	Lookup ToolkitLookup
}

// PurposeResolver advertises, consumes, and enforces the purpose argument
// (issue #1317). Audit records what a call did and, without this, never why: the
// platform already requires the model to thread a session handle on every call,
// and this asks the same model to state, in one sentence, what wider task the
// call serves. The stated purpose is recorded on the audit row, not passed to
// the tool.
//
// A nil *PurposeResolver is a valid no-op, so callers need not branch on whether
// purpose is configured.
type PurposeResolver struct {
	enabled  bool
	require  bool
	patterns []string
	kinds    map[string]bool
	lookup   ToolkitLookup
}

// NewPurposeResolver builds a PurposeResolver from its config.
func NewPurposeResolver(cfg PurposeConfig) *PurposeResolver {
	tools := cfg.Tools
	if len(tools) == 0 {
		tools = defaultPurposeTools
	}
	r := &PurposeResolver{
		enabled: cfg.Enabled,
		require: cfg.Require,
		kinds:   map[string]bool{},
		lookup:  cfg.Lookup,
	}
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if kind, ok := strings.CutPrefix(t, purposeKindPrefix); ok {
			if kind != "" {
				r.kinds[kind] = true
			}
			continue
		}
		r.patterns = append(r.patterns, t)
	}
	return r
}

// Gates reports whether a tool carries the purpose argument. It is the single
// predicate behind both paths — the tools/list decorator that advertises the
// argument and the tools/call resolver that consumes it — so what the platform
// asks for and what it enforces cannot drift.
//
// Because it decides from the tool name and its toolkit kind alone, the answer
// is the same on every replica and in both requests, without either path having
// to remember what the other did.
func (r *PurposeResolver) Gates(toolName string) bool {
	if r == nil || !r.enabled || toolName == "" {
		return false
	}
	for _, p := range r.patterns {
		// An invalid pattern matches nothing (filepath.Match's error path), so a
		// typo in the config narrows the gated set rather than widening it.
		if matched, err := filepath.Match(p, toolName); err == nil && matched {
			return true
		}
	}
	if len(r.kinds) == 0 || r.lookup == nil {
		return false
	}
	match := r.lookup.GetToolkitForTool(toolName)
	return match.Found && r.kinds[match.Kind]
}

// resolve takes the purpose off a tools/call request, records it on the platform
// context, and decides whether the call may proceed. It returns a non-nil error
// result only when the call must be refused with PURPOSE_REQUIRED; nil means
// proceed.
//
// It runs inside MCPToolCallMiddleware, after the session resolver, so
// pc.SessionHandleThreaded is set and the argument is stripped before the
// handler — or a gateway-proxied upstream server — can observe it.
//
// The refusal is conditioned on the caller having threaded an explicit session
// handle on this same call, which is the platform's only proof that a caller CAN
// thread a platform-injected argument. That one condition subsumes every
// exemption the feature needs: an MCP App's sandboxed call is session-adopted
// from its authenticated identity and threads nothing (#1040); the gateway REST
// shim, the admin tool runner, and a managed script drive a fresh in-memory
// session per request and thread nothing (#811); and an isolated dpp_/dpx_ run
// has its session minted server-side rather than passed in (#859). None of them
// can state a purpose, so none of them is refused for not stating one — while a
// real MCP agent, which the platform has already required to thread a handle, is.
func (r *PurposeResolver) resolve(req mcp.Request, pc *PlatformContext, toolName string) mcp.Result {
	if r == nil || !r.enabled || !r.Gates(toolName) {
		return nil
	}

	// Always take (and strip) the purpose before the handler or any
	// gateway-proxied upstream server can observe the platform-injected arg.
	purpose, _ := takeStringArg(req, purposeArg, nil)
	purpose = boundPurpose(purpose)
	pc.Purpose = purpose

	if purpose != "" || !r.require || !pc.SessionHandleThreaded {
		return nil
	}
	slog.Warn("purpose: gated call stated none",
		logKeyTool, toolName, logKeyUserID, pc.UserID)
	return createPurposeRequiredError(toolName)
}

// boundPurpose normalizes a stated purpose: surrounding whitespace is trimmed
// (a whitespace-only purpose is no purpose), and prose past maxPurposeChars is
// dropped. Truncation counts runes, not bytes, so a multi-byte character is
// never split into invalid UTF-8 on its way to the audit row.
func boundPurpose(purpose string) string {
	purpose = strings.TrimSpace(purpose)
	runes := []rune(purpose)
	if len(runes) <= maxPurposeChars {
		return purpose
	}
	return string(runes[:maxPurposeChars])
}

// createPurposeRequiredError builds a PURPOSE_REQUIRED error result. It emits
// the full self-describing envelope because the resolver short-circuits before
// the error-contract normalizer (which is registered inner to it).
func createPurposeRequiredError(toolName string) mcp.Result {
	return BuildErrorResult(NewToolError(
		CodePurposeRequired, ErrCategoryPurposeRequired,
		"PURPOSE_REQUIRED: "+toolName+" needs a purpose argument. Send one sentence naming the "+
			"wider task you are working on and why this call serves it, without repeating argument values.",
		"Retry the same call with purpose set, for example "+
			`purpose: "Answering how Q3 revenue split by region for the board deck." `+
			"This is a call-contract requirement, not a platform outage.",
	))
}

// MCPPurposeSchemaMiddleware creates MCP protocol-level middleware that
// advertises the purpose argument on the input schema of every gated tool in
// tools/list responses. It mirrors MCPSessionHandleSchemaMiddleware: it touches
// only the list response, so upstream toolkits are never modified.
func MCPPurposeSchemaMiddleware(resolver *PurposeResolver) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != methodToolsList {
				return result, err
			}
			return injectListedToolProperty(result, purposeArg, map[string]any{
				"type":        "string",
				"description": purposeSchemaDescription,
			}, resolver.Gates), nil
		}
	}
}
