package middleware

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/urnbuild"
)

// A table's catalog entry says what the table means. What it never says is how
// anyone actually got an answer out of it. The platform records that: every
// query it ran, what the caller said it was for, and whether anything was built
// from the result (#1321). Describing a table is exactly the moment that record
// is worth reading, so a describe carries the caller's own queries that already
// answered something on this table, beside the count of curated queries the
// catalog holds.

// provenQueryLimit bounds how many prior queries a describe carries. It is
// small on purpose: this is a nudge toward reuse, not a query history.
const provenQueryLimit = 3

// ProvenQuery is one recorded call that answered something on a table.
type ProvenQuery struct {
	// Reference is the call's mcp:call:<event_id> reference. Fetching it
	// returns the record in full, and is what makes a later re-run count as
	// reuse of it.
	Reference string `json:"reference"`
	// Purpose is what its caller said the query was for.
	Purpose string `json:"purpose,omitempty"`
	// Statement is the SQL that ran.
	Statement string `json:"statement,omitempty"`
	// Outcome and SatisfiedBy say how the query ended up being used.
	Outcome     string `json:"outcome"`
	SatisfiedBy string `json:"satisfied_by,omitempty"`
	// ReuseCount is how many later sessions re-ran it: a stranger's
	// confirmation, which the author's own verdict is not.
	ReuseCount int `json:"reuse_count,omitempty"`
	// PromotedURN is the catalog query this became, when it was promoted.
	PromotedURN string `json:"promoted_urn,omitempty"`
}

// ProvenQueryLister returns the caller's recorded queries that answered
// something on the dataset named by urn, best first.
//
// It is a function rather than an interface so the middleware states what it
// needs (a dataset, a caller, a bound) without naming the catalog that answers
// it, which lives outside this package.
type ProvenQueryLister func(ctx context.Context, urn, userID string, limit int) []ProvenQuery

// datasetURN names the catalog entity a described table belongs to, which is
// the key a recorded call's targets are stored under. The table identifier has
// already had the connection's catalog mapping applied by the enricher, so only
// the platform name is resolved here, from the same connection lookup.
//
// The connection is named by kind and name together, so the platform this
// returns is the one the statement ran against rather than one of the several a
// shared name could mean.
func (e *semanticEnricher) datasetURN(table semantic.TableIdentifier, connectionKind, connection string) string {
	if table.Schema == "" || table.Table == "" {
		return ""
	}
	var platform string
	if e.cfg.ForConnection != nil && connection != "" {
		platform, _ = e.cfg.ForConnection(connectionKind, connection)
	}
	name := table.Table
	if table.Schema != "" {
		name = table.Schema + "." + name
	}
	if table.Catalog != "" {
		name = table.Catalog + "." + name
	}
	return urnbuild.DatasetURNFromName(platform, name)
}

// appendProvenQueries adds the caller's proven queries for a table to the
// result. Best-effort and silent when there are none: an agent describing a
// table that nobody has queried yet should see nothing about it.
func (e *semanticEnricher) appendProvenQueries(
	ctx context.Context,
	result *mcp.CallToolResult,
	table semantic.TableIdentifier,
	pc *PlatformContext,
) *mcp.CallToolResult {
	if e.cfg.ProvenQueries == nil || pc == nil || pc.UserID == "" || result == nil {
		return result
	}
	urn := e.datasetURN(table, pc.ToolkitKind, pc.Connection)
	if urn == "" {
		return result
	}
	queries := e.cfg.ProvenQueries(ctx, urn, pc.UserID, provenQueryLimit)
	if len(queries) == 0 {
		return result
	}
	payload, err := json.Marshal(map[string]any{"proven_queries": queries})
	if err != nil {
		slog.Debug("proven queries not appended", keyError, err)
		return result
	}
	before := len(result.Content)
	result.Content = append(result.Content, &mcp.TextContent{Text: string(payload)})
	mirrorEnrichmentToStructured(result, before)
	return result
}
