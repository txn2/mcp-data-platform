package callrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// recentWindow is how far back an approver reads a requester's calls.
const recentWindow = 24 * time.Hour

// recentLimit bounds the audit rows read for one request.
const recentLimit = 200

// recentSQL reads the requester's calls of the request's tool on its
// connection in the window, newest first, from the audit record.
const recentSQL = `SELECT timestamp, parameters, success, COALESCE(handlers, '[]'::jsonb)
	FROM audit_logs
	WHERE lower(user_email) = lower($1) AND connection = $2 AND tool_name = $3 AND timestamp > $4
	ORDER BY timestamp DESC LIMIT $5`

// RecentCall is one of a requester's calls an approver reads beside a held
// call's request (#2437): when it was made, how many items it carried, and
// what came of it. Never its arguments.
type RecentCall struct {
	At      time.Time `json:"at"`
	Items   int       `json:"items"`
	Outcome string    `json:"outcome" example:"held"`
}

// The outcomes a recent call is shown with.
const (
	RecentRan       = "ran"
	RecentFailed    = "failed"
	RecentHeld      = "held"
	RecentRefused   = "refused"
	RecentAllowance = "ran_on_allowance"
)

// RecentCalls are the requester's calls of a call request's operation on its
// connection in the last day, from the audit record, each read as the
// handlers read it: its operation and its item count. A request that is not a
// call's has none.
func (e *Evaluator) RecentCalls(ctx context.Context, db *sql.DB, r *approval.Request) ([]RecentCall, error) {
	out := []RecentCall{}
	if e == nil || db == nil || r == nil || r.SubjectKind != approval.SubjectCall {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, recentSQL, r.RequesterEmail, r.CallConnection, r.CallTool, e.clock().Add(-recentWindow), recentLimit)
	if err != nil {
		return nil, fmt.Errorf("reading the requester's calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	pc := &middleware.PlatformContext{Connection: r.CallConnection, UserEmail: r.RequesterEmail, PersonaName: r.RequesterPersona, ToolkitKind: kindOf(r.CallTool)}
	for rows.Next() {
		var row recentRow
		if err := rows.Scan(&row.at, &row.params, &row.success, &row.runs); err != nil {
			return nil, fmt.Errorf("reading the requester's calls: %w", err)
		}
		if call, ok := e.recentOf(ctx, pc, r, row); ok {
			out = append(out, call)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the requester's calls: %w", err)
	}
	return out, nil
}

// recentRow is one audited call as recentSQL reads it.
type recentRow struct {
	at           time.Time
	params, runs []byte
	success      bool
}

// recentOf reads one audited call as the handlers read it, or reports that it
// is another operation's.
func (e *Evaluator) recentOf(ctx context.Context, pc *middleware.PlatformContext, r *approval.Request, row recentRow) (RecentCall, bool) {
	var args map[string]any
	_ = json.Unmarshal(row.params, &args)
	if args == nil {
		args = map[string]any{}
	}
	args["connection"] = r.CallConnection
	c := e.BuildCall(ctx, pc, r.CallTool, args)
	if c.Operation != r.CallOperation {
		return RecentCall{}, false
	}
	var runs []audit.HandlerRun
	_ = json.Unmarshal(row.runs, &runs)
	return RecentCall{At: row.at.UTC(), Items: e.itemsOf(c), Outcome: recentOutcome(row.success, runs)}, true
}

// itemsOf is a call's item count as the policies on it count it: the first
// matching policy naming a body field, else one.
func (e *Evaluator) itemsOf(c *callhandler.Call) int {
	for _, h := range e.Index.Match(c, callhandler.KindPolicy) {
		if strings.TrimSpace(h.ItemsPath) != "" {
			return callhandler.ItemCount(c.Body, h.ItemsPath)
		}
	}
	return 1
}

// recentOutcome reads what came of a call from its audit row.
func recentOutcome(success bool, runs []audit.HandlerRun) string {
	held, allowance := false, false
	for _, r := range runs {
		switch r.Outcome {
		case OutcomeDeny:
			return RecentRefused
		case OutcomeApprove:
			held = true
		case OutcomeAllowance:
			allowance = true
		}
	}
	switch {
	case allowance:
		return RecentAllowance
	case held:
		return RecentHeld
	case !success:
		return RecentFailed
	}
	return RecentRan
}
