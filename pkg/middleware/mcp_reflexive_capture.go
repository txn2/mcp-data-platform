package middleware

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/sqltables"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

// maxCapturedSQLChars bounds each SQL snippet embedded in a correction so a very
// large statement cannot blow past the memory content limit.
const maxCapturedSQLChars = 800

// maxCorrectionContentChars mirrors memstore.MaxContentLen; the assembled body
// is clamped to it so a capture never fails validation on length.
const maxCorrectionContentChars = memstore.MaxContentLen

// reflexiveCaptureTimeout bounds the detached mint so a hung embedder or DB call
// cannot leak the goroutine for the process lifetime.
const reflexiveCaptureTimeout = 30 * time.Second

// CorrectionCapture is a platform-minted "misconception + fix" memory the
// reflexive middleware asks the captor to persist. It is fully specified by the
// middleware (which owns the reflexive-capture policy); the captor only
// persists it, keeping the middleware decoupled from the memory toolkit.
type CorrectionCapture struct {
	SinkClass  string
	Category   string
	Content    string
	EntityURNs []string
	CreatedBy  string
	Persona    string
	UserID     string
	SessionID  string
	Metadata   map[string]any
}

// ReflexiveCaptor persists a platform-minted correction memory. Implemented by
// an adapter over the memory toolkit's AutoCapture; declared here so the
// middleware does not import the memory toolkit package.
type ReflexiveCaptor interface {
	CaptureCorrection(ctx context.Context, c CorrectionCapture) error
}

// URNBuilder builds a DataHub dataset URN from a query table reference, applying
// the connection's catalog mapping. Returns "" when a URN cannot be formed.
type URNBuilder func(connection, catalog, schema, table string) string

// ReflexiveCaptureConfig configures the reflexive-capture middleware.
type ReflexiveCaptureConfig struct {
	// Captor persists corrections. Required; a nil captor disables the middleware.
	Captor ReflexiveCaptor
	// Tracker holds per-session failures. Required; a nil tracker disables it.
	Tracker *SessionErrorTracker
	// URNBuilder optionally entity-keys corrections to their tables. May be nil.
	URNBuilder URNBuilder
	// CapturePermitted reports whether the caller's persona is authorized to
	// create memory (the memory_capture tool grant). Reflexive capture is a
	// memory write, so a persona denied that tool must not have records minted on
	// its behalf. Nil means no persona gating is configured (allow), matching the
	// tools/list visibility middleware's behavior when no authorizer is wired.
	CapturePermitted func(ctx context.Context, pc *PlatformContext) bool
}

// MCPReflexiveCaptureMiddleware observes trino_query / trino_execute results and
// turns a query error followed by a later related, same-connection success in
// the session into one auto-minted "misconception + fix" memory, with no
// operator prompt (#635). It records worth-capturing failures and, on a matching
// success, pairs them and dispatches the capture asynchronously so the tool
// response is never blocked. It is strictly observational: it never mutates the
// request or the result.
func MCPReflexiveCaptureMiddleware(cfg ReflexiveCaptureConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if pc, ok := cfg.observableQuery(ctx, method, err); ok {
				cfg.observe(req, result, pc)
			}
			return result, err
		}
	}
}

// observableQuery reports whether this call is a query outcome the middleware
// should act on and, if so, returns its PlatformContext. It filters out non-
// tool-calls, protocol-level errors (no readable tool result), non-query tools,
// callers without a session, and personas not permitted the memory_capture tool.
func (cfg ReflexiveCaptureConfig) observableQuery(ctx context.Context, method string, err error) (*PlatformContext, bool) {
	if cfg.Captor == nil || cfg.Tracker == nil || method != methodToolsCall || err != nil {
		return nil, false
	}
	pc := GetPlatformContext(ctx)
	if pc == nil || pc.SessionID == "" || !isReflexiveQueryTool(pc.ToolName) {
		return nil, false
	}
	if cfg.CapturePermitted != nil && !cfg.CapturePermitted(ctx, pc) {
		return nil, false
	}
	return pc, true
}

// observe records a failure or resolves a prior one for a single query call.
func (cfg ReflexiveCaptureConfig) observe(req mcp.Request, result mcp.Result, pc *PlatformContext) {
	sql, connection := sqlAndConnectionFromRequest(req)
	if sql == "" {
		return
	}
	refs := sqltables.Extract(sql)
	if len(refs) == 0 {
		return // must concern a physical dataset, not a table-less expression
	}

	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || callResult == nil {
		return
	}

	if callResult.IsError {
		cfg.recordFailure(pc.SessionID, sql, connection, callResult)
		return
	}
	cfg.resolveFailure(pc, connection, sql, refs)
}

// recordFailure stores a worth-capturing query error for later pairing.
func (cfg ReflexiveCaptureConfig) recordFailure(sessionID, sql, connection string, result *mcp.CallToolResult) {
	errMsg := errorMessageFromResult(result)
	if !worthCapturingQueryError(errMsg) {
		return
	}
	cfg.Tracker.RecordFailure(sessionID, FailedQuery{
		NormalizedSQL: normalizeSQLText(sql),
		RawSQL:        sql,
		Idents:        meaningfulIdentifiers(sql),
		Connection:    connection,
		ErrorMessage:  errMsg,
		FailedAt:      time.Now(),
	})
}

// resolveFailure pairs a successful query with the single best-matching prior
// failure on the same connection and dispatches the correction asynchronously.
func (cfg ReflexiveCaptureConfig) resolveFailure(pc *PlatformContext, connection, successSQL string, refs []sqltables.Ref) {
	failed := cfg.Tracker.TakeResolved(pc.SessionID, connection, meaningfulIdentifiers(successSQL), normalizeSQLText(successSQL))
	if failed == nil {
		return
	}

	capture := cfg.buildCorrection(pc, *failed, connection, successSQL, refs)
	if capture.Content == "" {
		return
	}

	// Persist asynchronously with a bounded detached context: the mint embeds and
	// writes to the store and must neither block the tool response nor be canceled
	// when the request ends; the timeout stops a hung mint from leaking. Identity
	// travels in the capture, not the context. The session id is copied out so the
	// goroutine never reads the PlatformContext concurrently with outer middleware
	// that may still be writing to it.
	sessionID := pc.SessionID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), reflexiveCaptureTimeout)
		defer cancel()
		if err := cfg.Captor.CaptureCorrection(ctx, capture); err != nil {
			slog.Warn("reflexive capture: failed to mint correction",
				"session_id", sessionID, "error", err)
		}
	}()
}

// buildCorrection assembles the CorrectionCapture for a failure/fix pair. Entity
// URNs are keyed with the SUCCESS query's connection and tables (the corrected,
// physically-real dataset), not the failed query's.
func (cfg ReflexiveCaptureConfig) buildCorrection(pc *PlatformContext, failed FailedQuery, connection, successSQL string, refs []sqltables.Ref) CorrectionCapture {
	return CorrectionCapture{
		SinkClass:  memstore.SinkSchemaEntity,
		Category:   memstore.CategoryCorrection,
		Content:    buildCorrectionContent(failed, successSQL),
		EntityURNs: cfg.entityURNs(connection, refs),
		CreatedBy:  pc.UserEmail,
		Persona:    pc.PersonaName,
		UserID:     pc.UserID,
		SessionID:  pc.SessionID,
		Metadata: map[string]any{
			"reflexive_trigger": "query_error_fix",
		},
	}
}

// entityURNs builds best-effort dataset URNs for the fully-qualified tables in
// refs, capped at the record limit. Returns nil when no builder is wired or no
// ref carries a catalog+schema+table triple.
func (cfg ReflexiveCaptureConfig) entityURNs(connection string, refs []sqltables.Ref) []string {
	if cfg.URNBuilder == nil {
		return nil
	}
	var urns []string
	seen := make(map[string]struct{})
	for _, r := range refs {
		if r.Catalog == "" || r.Schema == "" || r.Table == "" {
			continue
		}
		urn := cfg.URNBuilder(connection, r.Catalog, r.Schema, r.Table)
		if urn == "" {
			continue
		}
		if _, dup := seen[urn]; dup {
			continue
		}
		seen[urn] = struct{}{}
		urns = append(urns, urn)
		if len(urns) >= memstore.MaxEntityURNs {
			break
		}
	}
	return urns
}

// isReflexiveQueryTool reports whether a tool is a query tool the reflexive
// middleware observes.
func isReflexiveQueryTool(toolName string) bool {
	return toolName == toolNameTrinoQuery || toolName == toolNameTrinoExecute
}

// worthCapturingSignatures are the error-text markers of a data-model
// misunderstanding: the class of failure whose fix is worth keeping for the next
// agent. The allowlist is deliberately conservative (low false-positive bias):
// an error that matches none of these is treated as noise (infra, policy, or a
// transient failure) and never captured.
var worthCapturingSignatures = []string{
	"cannot be resolved",    // unknown column / identifier
	"does not exist",        // unknown table / schema / catalog
	"not registered",        // unknown function
	"cannot be applied to",  // operator / type mismatch
	"is ambiguous",          // ambiguous column reference
	"unexpected parameters", // wrong function arity
	"must be an aggregate",  // GROUP BY misconception
	"neither an aggregate",  // GROUP BY misconception (alternate phrasing)
	"type mismatch",         // explicit type mismatch
}

// excludedSignatures veto a capture even when a worth-capturing marker is
// present, covering infra/policy failures that are not misconceptions.
var excludedSignatures = []string{
	"access denied",
	"not authorized",
	"permission denied",
	"connection refused",
	"context deadline exceeded",
}

// worthCapturingQueryError reports whether a query error is a data-model
// misunderstanding worth capturing, rather than infra, policy, or transient
// noise.
func worthCapturingQueryError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	for _, ex := range excludedSignatures {
		if strings.Contains(lower, ex) {
			return false
		}
	}
	for _, sig := range worthCapturingSignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// sqlKeywords are the common SQL keywords and aggregate/date functions excluded
// from the identifier set used to score how related two queries are, so the
// similarity reflects schema identifiers (columns, tables, functions the user
// named) rather than boilerplate every query shares.
var sqlKeywords = map[string]bool{
	"select": true, "from": true, "where": true, "join": true, "on": true,
	"and": true, "or": true, "not": true, "in": true, "is": true, "null": true,
	"group": true, "by": true, "order": true, "having": true, "limit": true,
	"offset": true, "as": true, "inner": true, "left": true, "right": true,
	"outer": true, "cross": true, "natural": true, "full": true, "union": true,
	"all": true, "distinct": true, "case": true, "when": true, "then": true,
	"else": true, "end": true, "like": true, "between": true, "asc": true,
	"desc": true, "with": true, "using": true, "count": true, "sum": true,
	"avg": true, "min": true, "max": true, "cast": true, "true": true, "false": true,
}

// meaningfulIdentifiers returns the set of schema identifiers in a SQL statement
// (columns, tables, functions), lexed and lowercased, with SQL keywords removed.
// Used to score how related a successful query is to a prior failure.
func meaningfulIdentifiers(sql string) map[string]struct{} {
	ids := extractIdentifiers(sql)
	out := make(map[string]struct{}, len(ids))
	for id := range ids {
		if sqlKeywords[id] {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

// normalizeSQLText lowercases and collapses whitespace so two statements that
// differ only in formatting compare equal (and so an identical retry of a failed
// statement is not mistaken for a fix).
func normalizeSQLText(sql string) string {
	return strings.ToLower(strings.Join(strings.Fields(sql), " "))
}

// buildCorrectionContent renders the misconception/fix pair into the memory body,
// clamped to the content limit.
func buildCorrectionContent(failed FailedQuery, successSQL string) string {
	var b strings.Builder
	b.WriteString("A query error was corrected in the same session. ")
	b.WriteString("An earlier attempt failed and a later attempt over the same table(s) succeeded, so the fix is likely durable.\n\n")
	b.WriteString("Failed query:\n")
	b.WriteString(truncateForCapture(failed.RawSQL, maxCapturedSQLChars))
	b.WriteString("\n\nError:\n")
	b.WriteString(truncateForCapture(failed.ErrorMessage, maxCapturedSQLChars))
	b.WriteString("\n\nCorrected query that succeeded:\n")
	b.WriteString(truncateForCapture(successSQL, maxCapturedSQLChars))

	return clampUTF8(b.String(), maxCorrectionContentChars)
}

// truncateForCapture trims s to at most maxLen bytes on a rune boundary,
// appending an ellipsis marker when truncated.
func truncateForCapture(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return clampUTF8(s, maxLen) + " ...(truncated)"
}

// clampUTF8 returns s truncated to at most maxLen bytes without splitting a
// multi-byte rune, so the result is always valid UTF-8 (a mid-rune byte slice
// would make the Postgres INSERT fail and silently drop the capture).
func clampUTF8(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// sqlAndConnectionFromRequest reads the sql and connection arguments from a
// tools/call request.
func sqlAndConnectionFromRequest(req mcp.Request) (sql, connection string) {
	if req == nil {
		return "", ""
	}
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || params == nil {
		return "", ""
	}
	args := extractArgumentsMap(params)
	sql, _ = args["sql"].(string)
	connection, _ = args["connection"].(string)
	return sql, connection
}

// errorMessageFromResult extracts an error string from a tool-error result,
// preferring the structured error's bare message over the rendered text.
func errorMessageFromResult(result *mcp.CallToolResult) string {
	if getErr := result.GetError(); getErr != nil {
		return getErr.Error()
	}
	return extractMCPErrorMessage(result)
}
