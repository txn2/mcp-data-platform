package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
)

// SQLExecutor runs a query against the warehouse and returns its result rows,
// for execution-result (BIRD-style) grading of SQL-producing tasks.
type SQLExecutor interface {
	Exec(ctx context.Context, sql string) ([]map[string]any, error)
	Close() error
}

// TransportError wraps an infrastructure failure (connect/transport) executing a
// grader query, distinct from a tool-level SQL error. The distinction matters
// for grading: a candidate query that fails to PARSE/RUN is the agent's miss,
// but a transport blip while running it is a harness failure and must not be
// scored against the agent.
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// platformSQL executes grader queries through a DEDICATED MCP session on the
// base (admin) credential, deliberately separate from every attempt's session
// handle: the grader's own trino_query calls audit under this session, never an
// attempt's, so they cannot perturb an attempt's audit-row accounting bounds.
// When a discovery tool is present (the knowledge arms), it opens the
// search-first gate once so its queries are not refused.
type platformSQL struct {
	session *mcp.ClientSession
	handle  string
}

// sqlExecutor lazily builds the per-run grader session. It is created only when
// a task actually needs execution-result grading, so arms/runs without exec_sql
// tasks pay nothing.
func (e *runEnv) sqlExecutor(ctx context.Context) (SQLExecutor, error) {
	if e.sql != nil {
		return e.sql, nil
	}
	t := e.opts.Target // base credential, no identity rotation
	client := mcpc.New(t.BaseURL, t.HTTPClient(e.opts.HTTPTimeout))
	session, err := client.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect grader session: %w", err)
	}
	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("mint grader handle: %w", err)
	}
	if err := openGateIfPresent(ctx, session, info.Handle); err != nil {
		_ = session.Close()
		return nil, err
	}
	e.sql = &platformSQL{session: session, handle: info.Handle}
	return e.sql, nil
}

// openGateIfPresent calls search once when the arm exposes it, satisfying the
// search-first gate for the grader session (a no-op on arms without search).
func openGateIfPresent(ctx context.Context, session *mcp.ClientSession, handle string) error {
	tools, err := mcpc.ListTools(ctx, session)
	if err != nil {
		return fmt.Errorf("list grader tools: %w", err)
	}
	for _, tl := range tools {
		if tl.Name == "search" {
			r := mcpc.Call(ctx, session, "search", map[string]any{"intent": "bench warehouse schema and tables"}, handle)
			switch {
			case r.TransportErr != nil:
				return fmt.Errorf("open grader gate: %w", r.TransportErr)
			case r.ToolErr:
				// A tool-level search error leaves the gate closed, which would
				// make every subsequent grader query fail; surface it loudly.
				return fmt.Errorf("open grader gate: search returned error (%s): %s", r.ErrorCode, r.Text)
			}
			return nil
		}
	}
	return nil
}

// Exec runs one query and returns its rows.
func (p *platformSQL) Exec(ctx context.Context, sql string) ([]map[string]any, error) {
	res, err := p.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": sql, "session_id": p.handle},
	})
	if err != nil {
		return nil, &TransportError{Err: fmt.Errorf("trino_query transport: %w", err)}
	}
	if res.IsError {
		// A tool-level error means the query itself did not run (bad SQL); this
		// is NOT a transport error, so a candidate that lands here is the
		// agent's miss.
		return nil, fmt.Errorf("trino_query error: %s", mcpc.FirstText(res))
	}
	return rowsFromResult(res)
}

// Close releases the grader session.
func (p *platformSQL) Close() error {
	if p == nil || p.session == nil {
		return nil
	}
	return p.session.Close()
}

// rowsEnvelope is the subset of trino_query's structured output the grader reads.
type rowsEnvelope struct {
	Rows []map[string]any `json:"rows"`
}

// rowsFromResult extracts result rows from a trino_query result, preferring the
// structured content (enrichment-proof: enrichment appends text/meta, not the
// structured payload) and falling back to parsing the first text block as the
// query-output JSON.
func rowsFromResult(res *mcp.CallToolResult) ([]map[string]any, error) {
	if res.StructuredContent != nil {
		if rows, ok := rowsFromAny(res.StructuredContent); ok {
			return rows, nil
		}
	}
	if text := mcpc.FirstText(res); text != "" {
		if rows, ok := rowsFromJSON([]byte(text)); ok {
			return rows, nil
		}
	}
	return nil, errors.New("trino_query result carries no parseable rows")
}

// rowsFromAny re-marshals a structured value and extracts its rows.
func rowsFromAny(v any) ([]map[string]any, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return rowsFromJSON(raw)
}

// rowsFromJSON parses either a {"rows":[...]} envelope or a bare [...] array of
// row objects.
func rowsFromJSON(raw []byte) ([]map[string]any, bool) {
	var env rowsEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && env.Rows != nil {
		return env.Rows, true
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, true
	}
	return nil, false
}
