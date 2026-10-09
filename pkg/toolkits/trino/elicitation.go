package trino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Constants for number formatting and table-part parsing.
const (
	// base10 is the numeric base for integer formatting.
	base10 = 10
	// commaGroupSize is the number of digits between commas.
	commaGroupSize = 3
	// int64BitSize is the bit size for int64 parsing.
	int64BitSize = 64
	// tablePartsThree represents a fully-qualified catalog.schema.table reference.
	tablePartsThree = 3
	// tablePartsTwo represents a schema.table reference.
	tablePartsTwo = 2
)

// Log field keys used across elicitation functions.
const (
	logKeyError  = "error"
	logKeyReason = "reason"
)

// Elicitation schema and result-action constants.
const (
	schemaPropertiesKey = "properties"
	elicitActionAccept  = "accept"
)

// rowEstimatePattern matches "rows: 12345", the estimate a text plan carries.
var rowEstimatePattern = regexp.MustCompile(`rows:\s*(\d+)`)

// outputRowCountPattern matches an estimate's "outputRowCount" : 1100000.0 in
// EXPLAIN (TYPE IO) output, which is JSON (#2052). It reads the value where
// the document is not JSON a decoder accepts: Trino writes an unknown
// estimate as the bare token NaN.
var outputRowCountPattern = regexp.MustCompile(`"outputRowCount"\s*:\s*(\d+(?:\.\d+)?)`)

// elicitationDeclinedCategory mirrors middleware.ErrCategoryDeclined to avoid
// an import cycle. The audit middleware uses the CategorizedError interface to
// extract this value.
const elicitationDeclinedCategory = "user_declined"

// ElicitationDeclinedError indicates the user declined an elicitation request.
// This error is returned when the user explicitly declines or cancels a
// confirmation prompt (cost estimation, PII consent, etc.).
type ElicitationDeclinedError struct {
	Reason string
}

func (e *ElicitationDeclinedError) Error() string {
	return e.Reason
}

// ErrorCategory implements middleware.CategorizedError.
func (*ElicitationDeclinedError) ErrorCategory() string {
	return elicitationDeclinedCategory
}

// elicitor abstracts ServerSession methods used by elicitation middleware.
// *mcp.ServerSession satisfies this implicitly.
type elicitor interface {
	Elicit(ctx context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error)
	InitializeParams() *mcp.InitializeParams
}

// queryExplainer abstracts Explain for cost estimation.
// *trinoclient.Client satisfies this implicitly.
type queryExplainer interface {
	Explain(ctx context.Context, sql string, explainType trinoclient.ExplainType) (*trinoclient.ExplainResult, error)
}

// Compile-time interface satisfaction checks.
var (
	_ elicitor       = (*mcp.ServerSession)(nil)
	_ queryExplainer = (*trinoclient.Client)(nil)
)

// ElicitationMiddleware intercepts Trino query execution to request user
// confirmation when queries exceed cost thresholds or access PII data. The
// platform installs Middleware in its tools/call chain.
//
// Each connection carries its own settings (the platform injects them into
// every instance, and an instance's own elicitation block wins), and the
// estimate's EXPLAIN runs on the connection the call names. The toolkit keeps
// both current as connections are added and removed (#2052: the middleware
// used to be built only by the single-connection constructor the platform
// never called, so neither prompt ever ran).
type ElicitationMiddleware struct {
	// explainer resolves the client a connection's EXPLAIN runs on.
	explainer   func(connection string) (queryExplainer, error)
	telemetry   *queryTelemetry
	defaultConn string

	mu               sync.RWMutex
	configs          map[string]ElicitationConfig
	semanticProvider semantic.Provider
}

// newElicitationMiddleware builds the middleware over the connections'
// settings, resolving each call's explain client through explainer.
func newElicitationMiddleware(
	defaultConn string, instances map[string]Config,
	explainer func(string) (queryExplainer, error), telemetry *queryTelemetry,
) *ElicitationMiddleware {
	configs := make(map[string]ElicitationConfig, len(instances))
	for name, cfg := range instances {
		configs[name] = cfg.Elicitation
	}
	return &ElicitationMiddleware{
		explainer: explainer, telemetry: telemetry, defaultConn: defaultConn, configs: configs,
	}
}

// SetConnection records the elicitation settings of a connection added or
// replaced at runtime.
func (em *ElicitationMiddleware) SetConnection(name string, cfg ElicitationConfig) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.configs[name] = cfg
}

// ForgetConnection drops a removed connection's settings.
func (em *ElicitationMiddleware) ForgetConnection(name string) {
	em.mu.Lock()
	defer em.mu.Unlock()
	delete(em.configs, name)
}

// SetSemanticProvider updates the semantic provider (called after toolkit init).
func (em *ElicitationMiddleware) SetSemanticProvider(p semantic.Provider) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.semanticProvider = p
}

// getSemanticProvider returns the current semantic provider.
func (em *ElicitationMiddleware) getSemanticProvider() semantic.Provider {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.semanticProvider
}

// configFor is the named connection's settings; "" is the default connection.
func (em *ElicitationMiddleware) configFor(conn string) ElicitationConfig {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.configs[conn]
}

// multiRoundTripVersion is the first MCP revision on which a server may not
// send elicitation/create while serving a request: the prompt travels as an
// input request on the result, and the client retries the call with the
// answer (SEP-2322). The SDK keeps its copy of the constant unexported.
const multiRoundTripVersion = "2026-07-28"

// Input request ids, which a client echoes back with its answers.
const (
	inputCostEstimate = "trino_cost_estimate"
	inputPIIConsent   = "trino_pii_consent"
)

// declineReasons are what a declined prompt answers with, by input request.
var declineReasons = map[string]string{
	inputCostEstimate: "query declined: the estimated row count was not confirmed",
	inputPIIConsent:   "query declined: PII access not authorized by user",
}

// Middleware is the receiving middleware that puts the prompts in front of a
// trino_query (#2052). It runs as a tools/call layer rather than inside the
// mcp-trino toolkit because a client on 2026-07-28 is asked by returning
// input requests from the call and answering its retry, which a toolkit
// middleware can neither return nor read. An older client is asked in band,
// as before. The platform places it inside authorization and the gates, so a
// call nobody may make is never estimated or prompted for.
func (em *ElicitationMiddleware) Middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctr, ok := req.(*mcp.CallToolRequest)
			if !ok || method != "tools/call" || ctr.Params == nil || ctr.Params.Name != string(trinotools.ToolQuery) {
				return next(ctx, method, req)
			}
			if res := em.gate(ctx, ctr); res != nil {
				return res, nil
			}
			return next(ctx, method, req)
		}
	}
}

// gate answers a trino_query that must not run yet: the prompts it is owed,
// or the refusal a declined prompt is. Nil lets the call through.
func (em *ElicitationMiddleware) gate(ctx context.Context, req *mcp.CallToolRequest) *mcp.CallToolResult {
	// A retry carrying answers to this middleware's prompts is decided by
	// them. Any other answer is accepted: it is the client's consent.
	if answers := req.Params.InputResponses; len(answers) > 0 {
		return declinedAnswer(answers)
	}
	call, sql := em.callFor(req)
	if call == nil {
		return nil
	}
	if caps := req.ClientCapabilities(); caps == nil || caps.Elicitation == nil {
		return nil
	}
	if req.ProtocolVersion() >= multiRoundTripVersion {
		prompts := call.pending(ctx, sql)
		if len(prompts) == 0 {
			return nil
		}
		return &mcp.CallToolResult{InputRequests: prompts}
	}
	if req.Session == nil {
		return nil
	}
	if err := call.beforeWithSession(ctx, req.Session, sql); err != nil {
		res := &mcp.CallToolResult{}
		res.SetError(err)
		return res
	}
	return nil
}

// declinedAnswer is the refusal for the first of this middleware's prompts
// the client declined or canceled, or nil when none was.
func declinedAnswer(answers mcp.InputResponseMap) *mcp.CallToolResult {
	for _, id := range []string{inputCostEstimate, inputPIIConsent} {
		r, ok := answers[id].(*mcp.ElicitResult)
		if !ok || r == nil || r.Action == elicitActionAccept {
			continue
		}
		res := &mcp.CallToolResult{}
		res.SetError(&ElicitationDeclinedError{Reason: declineReasons[id]})
		return res
	}
	return nil
}

// callFor is the call's view of the middleware and its statement, or nil when
// the connection it names has prompts off or there is no statement.
func (em *ElicitationMiddleware) callFor(req *mcp.CallToolRequest) (call *connElicitation, sql string) {
	var in struct {
		SQL        string `json:"sql"`
		Connection string `json:"connection"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &in); err != nil || in.SQL == "" {
		return nil, ""
	}
	conn := in.Connection
	if conn == "" {
		conn = em.defaultConn
	}
	cfg := em.configFor(conn)
	if !cfg.Enabled {
		return nil, ""
	}
	call = &connElicitation{
		conn: conn, config: cfg, telemetry: em.telemetry,
		semanticProvider: em.getSemanticProvider(),
	}
	if em.explainer != nil {
		explainer, err := em.explainer(conn)
		if err != nil {
			// The query names the same connection and reports the failure;
			// the estimate is skipped, as it is when EXPLAIN fails.
			slog.Debug("elicitation: cost estimation skipped", logKeyReason, "no client", logKeyError, err)
		} else {
			call.client = explainer
		}
	}
	return call, in.SQL
}

// connElicitation is one call's view of the middleware: the connection it
// names, that connection's settings and explain client, and the semantic
// provider the PII check reads.
type connElicitation struct {
	conn             string
	client           queryExplainer
	config           ElicitationConfig
	semanticProvider semantic.Provider
	telemetry        *queryTelemetry
}

// beforeWithSession asks an older client in band, one prompt after another,
// and is the refusal of the first one declined.
func (em *connElicitation) beforeWithSession(ctx context.Context, e elicitor, sql string) error {
	// Check if the client supports elicitation.
	if !clientSupportsElicitation(e) {
		return nil
	}
	if err := em.checkCostEstimation(ctx, e, sql); err != nil {
		return err
	}
	return em.checkPIIConsent(ctx, e, sql)
}

// pending is the prompts the statement is owed, as the input requests a
// 2026-07-28 client is answered with.
func (em *connElicitation) pending(ctx context.Context, sql string) mcp.InputRequestMap {
	prompts := mcp.InputRequestMap{}
	if p := em.costPrompt(ctx, sql); p != nil {
		prompts[inputCostEstimate] = p
	}
	if p := em.piiPrompt(ctx, sql); p != nil {
		prompts[inputPIIConsent] = p
	}
	return prompts
}

// confirmation is a prompt with nothing to fill in: accept or decline.
func confirmation(message string) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Message: message,
		RequestedSchema: map[string]any{
			"type":              "object",
			schemaPropertiesKey: map[string]any{},
		},
	}
}

// ask puts one prompt to an older client in band. A failed elicit degrades
// to running the query, as an unanswerable prompt always has.
func ask(ctx context.Context, e elicitor, p *mcp.ElicitParams, declined string) error {
	result, err := e.Elicit(ctx, p)
	if err != nil {
		slog.Debug("elicitation: confirmation skipped",
			logKeyReason, "elicit call failed",
			logKeyError, err,
		)
		return nil // graceful degradation
	}
	if result.Action != elicitActionAccept {
		return &ElicitationDeclinedError{Reason: declined}
	}
	return nil
}

// checkCostEstimation runs EXPLAIN IO to estimate query cost and elicits
// confirmation if the estimated row count exceeds the configured threshold.
func (em *connElicitation) checkCostEstimation(ctx context.Context, e elicitor, sql string) error {
	p := em.costPrompt(ctx, sql)
	if p == nil {
		return nil
	}
	return ask(ctx, e, p, declineReasons[inputCostEstimate])
}

// costPrompt is the cost confirmation the statement is owed, or nil: the
// check is off, the estimate failed, or it is within the threshold.
func (em *connElicitation) costPrompt(ctx context.Context, sql string) *mcp.ElicitParams {
	if !em.config.CostEstimation.Enabled {
		return nil
	}
	estimated, err := em.estimateRows(ctx, sql)
	if err != nil {
		slog.Debug("elicitation: cost estimation skipped",
			logKeyReason, "explain failed",
			logKeyError, err,
		)
		return nil // graceful degradation
	}
	threshold := em.config.CostEstimation.RowThreshold
	if estimated <= threshold {
		return nil
	}
	return confirmation(fmt.Sprintf(
		"This query is estimated to scan approximately %s rows (threshold: %s). Proceed?",
		formatRowCount(estimated),
		formatRowCount(threshold),
	))
}

// checkPIIConsent checks if the query accesses PII columns and elicits
// consent if any are found.
func (em *connElicitation) checkPIIConsent(ctx context.Context, e elicitor, sql string) error {
	p := em.piiPrompt(ctx, sql)
	if p == nil {
		return nil
	}
	return ask(ctx, e, p, declineReasons[inputPIIConsent])
}

// piiPrompt is the consent the statement is owed, or nil: the check is off,
// there is no semantic provider, or nothing it reads is tagged PII.
//
// A table tagged PII counts as well as a column (#2052): a catalog commonly
// tags the dataset that holds personal data rather than each column of it,
// and a check that read column tags alone never asked about such a table.
func (em *connElicitation) piiPrompt(ctx context.Context, sql string) *mcp.ElicitParams {
	if !em.config.PIIConsent.Enabled || em.semanticProvider == nil {
		return nil
	}
	var columns, tables int
	for _, ref := range extractTablesFromSQL(sql) {
		if em.tableTaggedPII(ctx, ref) {
			tables++
		}
		columns += em.piiColumns(ctx, ref)
	}
	switch {
	case columns > 0 && tables > 0:
		return confirmation(fmt.Sprintf(
			"This query accesses %d PII column(s) and %d table(s) tagged PII. Proceed with access?", columns, tables))
	case columns > 0:
		return confirmation(fmt.Sprintf("This query accesses %d PII column(s). Proceed with access?", columns))
	case tables > 0:
		return confirmation(fmt.Sprintf("This query reads %d table(s) tagged PII. Proceed with access?", tables))
	}
	return nil
}

// tableTaggedPII reports whether the catalog tags the table itself PII, by
// the rule a column's tags are read with: a tag whose name contains "pii".
func (em *connElicitation) tableTaggedPII(ctx context.Context, ref semantic.TableIdentifier) bool {
	tc, err := em.semanticProvider.GetTableContext(ctx, ref)
	if err != nil || tc == nil {
		if err != nil {
			slog.Debug("elicitation: PII table check skipped", "table", ref.String(), logKeyError, err)
		}
		return false
	}
	for _, tag := range tc.Tags {
		if strings.Contains(strings.ToLower(tag), "pii") {
			return true
		}
	}
	return false
}

// piiColumns counts the table's columns the catalog tags PII.
func (em *connElicitation) piiColumns(ctx context.Context, ref semantic.TableIdentifier) int {
	cols, err := em.semanticProvider.GetColumnsContext(ctx, ref)
	if err != nil {
		slog.Debug("elicitation: PII check skipped for table",
			"table", ref.String(),
			logKeyError, err,
		)
		return 0
	}
	n := 0
	for _, col := range cols {
		if col.IsPII {
			n++
		}
	}
	return n
}

// estimateRows runs EXPLAIN IO and parses estimated row counts from the output.
// Returns 0 if parsing fails (graceful degradation).
//
// The EXPLAIN is a statement the platform sends to Trino, so it is measured as
// one: trino_queries_total{query_kind="explain"} and a trino.explain span
// under the tool call's.
func (em *connElicitation) estimateRows(ctx context.Context, sql string) (int64, error) {
	if em.client == nil {
		return 0, errors.New("explain io: no client for the connection")
	}
	var c *call
	if em.telemetry != nil {
		ctx, c = em.telemetry.begin(ctx, kindExplain, sqltables.Summary(sql), em.conn)
	}
	result, err := em.client.Explain(ctx, sql, trinoclient.ExplainIO)
	if c != nil {
		em.telemetry.finish(ctx, c, false, err, errorState(err))
	}
	if err != nil {
		return 0, fmt.Errorf("explain io: %w", err)
	}
	return parseRowEstimates(result.Plan), nil
}

// parseRowEstimates extracts row count estimates from Trino EXPLAIN IO output
// and returns the maximum single-table estimate.
//
// EXPLAIN (TYPE IO) answers with JSON: each table the statement reads is an
// inputTableColumnInfos entry whose estimate carries outputRowCount. The
// matcher read only the "rows: N" a text plan prints, so no estimate was ever
// found and the cost prompt never fired against a real Trino (#2052). A text
// plan is still read, and a document a decoder refuses (an unknown estimate
// is written NaN) is read by its outputRowCount fields.
func parseRowEstimates(plan string) int64 {
	var io struct {
		InputTableColumnInfos []struct {
			Estimate struct {
				OutputRowCount float64 `json:"outputRowCount"`
			} `json:"estimate"`
		} `json:"inputTableColumnInfos"`
	}
	if err := json.Unmarshal([]byte(plan), &io); err == nil && len(io.InputTableColumnInfos) > 0 {
		var maxRows int64
		for _, info := range io.InputTableColumnInfos {
			if n := int64(info.Estimate.OutputRowCount); n > maxRows {
				maxRows = n
			}
		}
		return maxRows
	}
	maxRows := maxMatch(rowEstimatePattern, plan)
	if n := maxMatch(outputRowCountPattern, plan); n > maxRows {
		maxRows = n
	}
	return maxRows
}

// maxMatch is the largest number pattern's first group matches in text.
func maxMatch(pattern *regexp.Regexp, text string) int64 {
	var maxRows int64
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		if len(match) < tablePartsTwo {
			continue
		}
		f, err := strconv.ParseFloat(match[1], int64BitSize)
		if err != nil {
			continue
		}
		if n := int64(f); n > maxRows {
			maxRows = n
		}
	}
	return maxRows
}

// extractTablesFromSQL names the tables a statement reads, for PII checking.
//
// The extraction itself is the platform's one extractor (internal/sqltables);
// what this adds is the shape the semantic layer asks in. A reference with
// neither catalog nor schema is dropped: a bare name cannot be resolved to a
// catalog entity, so it can carry no PII classification to check.
func extractTablesFromSQL(sql string) []semantic.TableIdentifier {
	refs := sqltables.Extract(sql)
	var tables []semantic.TableIdentifier
	for _, ref := range refs {
		if ref.Schema == "" {
			continue
		}
		tables = append(tables, semantic.TableIdentifier{
			Catalog: ref.Catalog,
			Schema:  ref.Schema,
			Table:   ref.Table,
		})
	}
	return tables
}

// clientSupportsElicitation checks whether the connected client has
// declared elicitation support in its capabilities.
func clientSupportsElicitation(e elicitor) bool {
	params := e.InitializeParams()
	if params == nil || params.Capabilities == nil {
		return false
	}
	return params.Capabilities.Elicitation != nil
}

// formatRowCount formats a large number with comma separators for readability.
func formatRowCount(n int64) string {
	s := strconv.FormatInt(n, base10)
	if len(s) <= commaGroupSize {
		return s
	}

	var result []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%commaGroupSize == 0 {
			result = append(result, ',')
		}
		result = append(result, s[i])
	}
	return string(result)
}
