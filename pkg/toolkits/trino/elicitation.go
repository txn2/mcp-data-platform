package trino

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
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

// rowEstimatePattern matches "rows: 12345" in Trino EXPLAIN IO output.
var rowEstimatePattern = regexp.MustCompile(`rows:\s*(\d+)`)

// tableFromSQLPattern extracts table references from SQL FROM and JOIN clauses.
var tableFromSQLPattern = regexp.MustCompile(
	`(?i)(?:FROM|JOIN)\s+` +
		`([a-zA-Z_]\w*(?:\.[a-zA-Z_]\w*){1,2})` +
		`(?:\s|$|,|;|\))`,
)

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
// confirmation when queries exceed cost thresholds or access PII data.
// It implements trinotools.ToolMiddleware.
type ElicitationMiddleware struct {
	client           queryExplainer
	config           ElicitationConfig
	semanticProvider semantic.Provider
	mu               sync.RWMutex
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

// Before checks cost and PII triggers before query execution.
// Returns an error to abort the query if the user declines.
func (em *ElicitationMiddleware) Before(ctx context.Context, tc *trinotools.ToolContext) (context.Context, error) {
	// Only activate for trino_query.
	if tc.Name != trinotools.ToolQuery {
		return ctx, nil
	}

	// Extract SQL from the query input.
	sql := extractSQLFromInput(tc.Input)
	if sql == "" {
		return ctx, nil
	}

	// Get the server session for elicitation.
	ss := mcpcontext.GetServerSession(ctx)
	if ss == nil {
		return ctx, nil
	}

	return ctx, em.beforeWithSession(ctx, ss, sql)
}

// beforeWithSession contains the core elicitation logic, extracted from Before
// so it can be tested with a mock elicitor without requiring a real MCP session.
func (em *ElicitationMiddleware) beforeWithSession(ctx context.Context, e elicitor, sql string) error {
	// Check if the client supports elicitation.
	if !clientSupportsElicitation(e) {
		return nil
	}

	// Cost estimation trigger.
	if em.config.CostEstimation.Enabled {
		if err := em.checkCostEstimation(ctx, e, sql); err != nil {
			return err
		}
	}

	// PII consent trigger.
	if em.config.PIIConsent.Enabled {
		if err := em.checkPIIConsent(ctx, e, sql); err != nil {
			return err
		}
	}

	return nil
}

// After is a no-op — elicitation happens before query execution.
func (*ElicitationMiddleware) After(
	_ context.Context,
	_ *trinotools.ToolContext,
	result *mcp.CallToolResult,
	handlerErr error,
) (*mcp.CallToolResult, error) {
	return result, handlerErr
}

// checkCostEstimation runs EXPLAIN IO to estimate query cost and elicits
// confirmation if the estimated row count exceeds the configured threshold.
func (em *ElicitationMiddleware) checkCostEstimation(ctx context.Context, e elicitor, sql string) error {
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

	message := fmt.Sprintf(
		"This query is estimated to scan approximately %s rows (threshold: %s). Proceed?",
		formatRowCount(estimated),
		formatRowCount(threshold),
	)

	result, err := e.Elicit(ctx, &mcp.ElicitParams{
		Message: message,
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	})
	if err != nil {
		slog.Debug("elicitation: cost confirmation skipped",
			logKeyReason, "elicit call failed",
			logKeyError, err,
		)
		return nil // graceful degradation
	}

	if result.Action != "accept" {
		return &ElicitationDeclinedError{
			Reason: fmt.Sprintf("query declined: estimated %s rows exceeds threshold", formatRowCount(estimated)),
		}
	}

	return nil
}

// checkPIIConsent checks if the query accesses PII columns and elicits
// consent if any are found.
func (em *ElicitationMiddleware) checkPIIConsent(ctx context.Context, e elicitor, sql string) error {
	sp := em.getSemanticProvider()
	if sp == nil {
		return nil
	}

	tables := extractTablesFromSQL(sql)
	if len(tables) == 0 {
		return nil
	}

	var piiColumns []string
	for _, ref := range tables {
		cols, err := sp.GetColumnsContext(ctx, ref)
		if err != nil {
			slog.Debug("elicitation: PII check skipped for table",
				"table", ref.String(),
				logKeyError, err,
			)
			continue
		}
		for name, col := range cols {
			if col.IsPII {
				piiColumns = append(piiColumns, ref.String()+"."+name)
			}
		}
	}

	if len(piiColumns) == 0 {
		return nil
	}

	message := fmt.Sprintf(
		"This query accesses %d PII column(s). Proceed with access?",
		len(piiColumns),
	)

	result, err := e.Elicit(ctx, &mcp.ElicitParams{
		Message: message,
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	})
	if err != nil {
		slog.Debug("elicitation: PII consent skipped",
			logKeyReason, "elicit call failed",
			logKeyError, err,
		)
		return nil // graceful degradation
	}

	if result.Action != "accept" {
		return &ElicitationDeclinedError{
			Reason: "query declined: PII access not authorized by user",
		}
	}

	return nil
}

// estimateRows runs EXPLAIN IO and parses estimated row counts from the output.
// Returns 0 if parsing fails (graceful degradation).
func (em *ElicitationMiddleware) estimateRows(ctx context.Context, sql string) (int64, error) {
	result, err := em.client.Explain(ctx, sql, trinoclient.ExplainIO)
	if err != nil {
		return 0, fmt.Errorf("explain io: %w", err)
	}
	return parseRowEstimates(result.Plan), nil
}

// parseRowEstimates extracts row count estimates from Trino EXPLAIN IO output
// and returns the maximum single-table estimate.
func parseRowEstimates(plan string) int64 {
	matches := rowEstimatePattern.FindAllStringSubmatch(plan, -1)
	var maxRows int64
	for _, match := range matches {
		if len(match) < tablePartsTwo {
			continue
		}
		n, err := strconv.ParseInt(match[1], base10, int64BitSize)
		if err != nil {
			continue
		}
		if n > maxRows {
			maxRows = n
		}
	}
	return maxRows
}

// extractTablesFromSQL extracts table identifiers from SQL for PII checking.
// This is a simplified extractor that handles catalog.schema.table and
// schema.table patterns from FROM and JOIN clauses.
func extractTablesFromSQL(sql string) []semantic.TableIdentifier {
	matches := tableFromSQLPattern.FindAllStringSubmatch(sql, -1)
	seen := make(map[string]bool, len(matches))
	var tables []semantic.TableIdentifier

	for _, match := range matches {
		if len(match) < tablePartsTwo {
			continue
		}
		ref := match[1]
		if seen[ref] {
			continue
		}
		seen[ref] = true

		parts := strings.Split(ref, ".")
		var tid semantic.TableIdentifier
		switch len(parts) {
		case tablePartsThree:
			tid = semantic.TableIdentifier{Catalog: parts[0], Schema: parts[1], Table: parts[2]}
		case tablePartsTwo:
			tid = semantic.TableIdentifier{Schema: parts[0], Table: parts[1]}
		default:
			continue
		}
		tables = append(tables, tid)
	}

	return tables
}

// extractSQLFromInput extracts the SQL string from a tool context input.
func extractSQLFromInput(input any) string {
	qi, ok := input.(trinotools.QueryInput)
	if !ok {
		return ""
	}
	return qi.SQL
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
