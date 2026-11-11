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

	"github.com/txn2/mcp-data-platform/internal/sqlident"
	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/audit"
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
	elicitActionDecline = "decline"
)

// nanEstimatePattern matches an estimate Trino could not make, which EXPLAIN
// (TYPE IO) writes as the bare token NaN: not JSON a decoder accepts, so it is
// read as null, an unknown estimate (#2586).
var nanEstimatePattern = regexp.MustCompile(`:\s*-?NaN\b`)

// elicitationDeclinedCategory mirrors middleware.ErrCategoryDeclined to avoid
// an import cycle. The audit middleware uses the CategorizedError interface to
// extract this value.
const elicitationDeclinedCategory = "user_declined"

// ElicitationDeclinedError is a confirmation prompt (cost estimation, PII
// consent) that did not come back accepted. Unanswered tells the two ways
// apart (#2586): the person declined, or nobody answered (the client canceled
// or gave up waiting), which is not a person saying no and is recorded under
// its own category.
type ElicitationDeclinedError struct {
	Reason     string
	Unanswered bool
}

func (e *ElicitationDeclinedError) Error() string {
	return e.Reason
}

// ErrorCategory implements middleware.CategorizedError.
func (e *ElicitationDeclinedError) ErrorCategory() string {
	if e.Unanswered {
		return audit.CategoryPromptUnanswered
	}
	return elicitationDeclinedCategory
}

// refusal is the error a prompt's answer action makes: decline is the person
// saying no; anything else that is not accept (cancel, or an action the
// client invented) means the person was not reached.
func refusal(id, action string) *ElicitationDeclinedError {
	if action == elicitActionDecline {
		return &ElicitationDeclinedError{Reason: declineReasons[id]}
	}
	return &ElicitationDeclinedError{Reason: unansweredReasons[id], Unanswered: true}
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
	// limits is trino_query's row limits, the default connection's as the
	// query tool is built with them.
	limits rowLimits

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
		limits: limitsOf(instances[defaultConn]),
	}
}

// limitsOf is a connection's row limits with the toolkit's defaults where it
// sets none.
func limitsOf(cfg Config) rowLimits {
	l := rowLimits{defaultRows: cfg.DefaultLimit, maxRows: cfg.MaxLimit}
	if l.defaultRows <= 0 {
		l.defaultRows = defaultQueryLimit
	}
	if l.maxRows <= 0 {
		l.maxRows = defaultMaxLimit
	}
	return l
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

// methodToolsCall is the MCP method a tool call arrives as.
const methodToolsCall = "tools/call"

// Input request ids, which a client echoes back with its answers.
const (
	inputCostEstimate = "trino_cost_estimate"
	inputPIIConsent   = "trino_pii_consent"
)

// declineReasons are what a declined prompt answers with, by input request.
var declineReasons = map[string]string{
	inputCostEstimate: "query declined: the person did not approve the estimated row count",
	inputPIIConsent:   "query declined: PII access not authorized by user",
}

// unansweredReasons are what a prompt nobody answered answers with: the
// person was never reached, so sending the same call again only asks again
// (#2586).
var unansweredReasons = map[string]string{
	inputCostEstimate: "query not run: the confirmation of its estimated row count was not answered; " +
		"the person was not reached, and sending the same call again will only ask again. " +
		"Ask the person directly, or narrow the query, before retrying",
	inputPIIConsent: "query not run: the PII access confirmation was not answered; " +
		"the person was not reached, and sending the same call again will only ask again. " +
		"Ask the person directly before retrying",
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
			if !ok || method != methodToolsCall || ctr.Params == nil || ctr.Params.Name != string(trinotools.ToolQuery) {
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
		res.SetError(refusal(id, r.Action))
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
		Limit      int    `json:"limit"`
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
		rowLimit:         em.limits.effective(in.Limit),
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
	// rowLimit is the most rows the call will return: its limit argument
	// clamped as trino_query clamps it.
	rowLimit int64
}

// rowLimits is trino_query's row limits, which bound what a call returns.
type rowLimits struct {
	defaultRows, maxRows int
}

// effective is the row limit a call asking for requested runs with, by
// trino_query's own rule: none (or a non-positive one) is the default, and
// none may exceed the maximum.
func (l rowLimits) effective(requested int) int64 {
	limit := requested
	if limit <= 0 {
		limit = l.defaultRows
	}
	if limit > l.maxRows {
		limit = l.maxRows
	}
	return int64(limit)
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
func ask(ctx context.Context, e elicitor, p *mcp.ElicitParams, id string) error {
	result, err := e.Elicit(ctx, p)
	if err != nil {
		slog.Debug("elicitation: confirmation skipped",
			logKeyReason, "elicit call failed",
			logKeyError, err,
		)
		return nil // graceful degradation
	}
	if result.Action != elicitActionAccept {
		return refusal(id, result.Action)
	}
	return nil
}

// checkCostEstimation runs EXPLAIN IO to estimate how many rows the query
// returns and elicits confirmation if that exceeds the configured threshold.
func (em *connElicitation) checkCostEstimation(ctx context.Context, e elicitor, sql string) error {
	p := em.costPrompt(ctx, sql)
	if p == nil {
		return nil
	}
	return ask(ctx, e, p, inputCostEstimate)
}

// costPrompt is the cost confirmation the statement is owed, or nil: the
// check is off, the estimate failed or is unknown, or what the call returns
// is within the threshold.
//
// The figure is the rows the statement RETURNS (#2586): the estimate of its
// output, capped by the row limit the call runs with. The rows a table scan
// reads are not put to a person: the query timeout bounds a scan, and a
// person shown "1,430,361 rows" for an aggregate that returns 60 has nothing
// to decide on.
func (em *connElicitation) costPrompt(ctx context.Context, sql string) *mcp.ElicitParams {
	if !em.config.CostEstimation.Enabled {
		return nil
	}
	estimated, known, err := em.estimateRows(ctx, sql)
	if err != nil {
		slog.Debug("elicitation: cost estimation skipped",
			logKeyReason, "explain failed",
			logKeyError, err,
		)
		return nil // graceful degradation
	}
	if !known {
		// The row limit and the result budget still bound what comes back.
		return nil
	}
	if em.rowLimit > 0 && estimated > em.rowLimit {
		estimated = em.rowLimit
	}
	threshold := em.config.CostEstimation.RowThreshold
	if estimated <= threshold {
		return nil
	}
	return confirmation(fmt.Sprintf(
		"This query is estimated to return about %s rows (threshold: %s). Proceed?",
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
	return ask(ctx, e, p, inputPIIConsent)
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
	read := readColumns(sql)
	for _, ref := range extractTablesFromSQL(sql) {
		if em.tableTaggedPII(ctx, ref) {
			tables++
		}
		columns += em.piiColumns(ctx, ref, read)
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

// columnsRead is which of a table's columns a statement reads: every one
// under a star projection, else the names the statement mentions.
type columnsRead struct {
	all   bool
	names map[string]bool
}

// readColumns reads a statement lexically for the columns it touches
// (#2586). A column is counted when its name appears anywhere in the
// statement outside strings and comments, which over-counts a name shared
// with a table or an alias and never misses a column the statement names.
func readColumns(sql string) columnsRead {
	return columnsRead{all: sqlident.ReadsEveryColumn(sql), names: sqlident.Identifiers(sql)}
}

func (r columnsRead) includes(column string) bool {
	return r.all || r.names[strings.ToLower(column)]
}

// piiColumns counts the table's columns the catalog tags PII that the
// statement reads. A column it never names is not counted (#2586): an
// aggregate over non-PII columns was told it accessed a PII column it never
// selected.
func (em *connElicitation) piiColumns(ctx context.Context, ref semantic.TableIdentifier, read columnsRead) int {
	cols, err := em.semanticProvider.GetColumnsContext(ctx, ref)
	if err != nil {
		slog.Debug("elicitation: PII check skipped for table",
			"table", ref.String(),
			logKeyError, err,
		)
		return 0
	}
	n := 0
	for name, col := range cols {
		if col.IsPII && read.includes(name) {
			n++
		}
	}
	return n
}

// estimateRows runs EXPLAIN IO and reads the estimate of the rows the
// statement returns; known is false when Trino could not estimate it.
//
// The EXPLAIN is a statement the platform sends to Trino, so it is measured as
// one: trino_queries_total{query_kind="explain"} and a trino.explain span
// under the tool call's.
func (em *connElicitation) estimateRows(ctx context.Context, sql string) (rows int64, known bool, err error) {
	if em.client == nil {
		return 0, false, errors.New("explain io: no client for the connection")
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
		return 0, false, fmt.Errorf("explain io: %w", err)
	}
	rows, known = parseReturnedRows(result.Plan)
	return rows, known, nil
}

// parseReturnedRows reads the estimate of the rows a statement returns from
// EXPLAIN (TYPE IO): the document's top-level estimate.outputRowCount
// (#2586). Each inputTableColumnInfos entry's estimate is the rows read from
// one table, which an aggregate or a LIMIT can make vastly larger than what
// comes back. An estimate Trino could not make (NaN), a document with no
// top-level estimate, or one that is not JSON is unknown.
func parseReturnedRows(plan string) (rows int64, known bool) {
	var io struct {
		Estimate *struct {
			OutputRowCount *float64 `json:"outputRowCount"`
		} `json:"estimate"`
	}
	if err := json.Unmarshal([]byte(nanEstimatePattern.ReplaceAllString(plan, ": null")), &io); err != nil {
		return 0, false
	}
	if io.Estimate == nil || io.Estimate.OutputRowCount == nil || *io.Estimate.OutputRowCount < 0 {
		return 0, false
	}
	return int64(*io.Estimate.OutputRowCount), true
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
