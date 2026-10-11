package scriptstmt

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// Decide sets the timeout one statement call runs under from the
// timeout_seconds in args (absent is the tools' default), refusing one above
// ceiling and cutting one the run's remaining time would not outlast. The
// limit reaches the tool in args when the script named one or it is not the
// tools' own default, so a call that names no timeout on a deployment that
// leaves the default alone is the call it always was.
func Decide(tool string, args map[string]any, ceiling, remaining time.Duration) (Limit, error) {
	requested, err := requestedSeconds(args[ArgTimeout])
	if err == nil {
		var limit Limit
		if limit, err = StatementLimit(requested, ceiling, remaining); err == nil {
			if limit.Source == LimitRequested || limit.Timeout != ToolDefaultTimeout {
				args[ArgTimeout] = limit.Seconds()
			}
			return limit, nil
		}
	}
	return Limit{}, fmt.Errorf("tool %s: %w", tool, err)
}

// requestedSeconds reads a timeout_seconds a script passed: absent is zero,
// and anything but a whole number is refused.
func requestedSeconds(v any) (int, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int:
		return n, nil
	case int64:
		if n <= math.MaxInt32 {
			return int(n), nil
		}
	case float64:
		if n == math.Trunc(n) && n <= math.MaxInt32 {
			return int(n), nil
		}
	}
	return 0, fmt.Errorf("timeout_seconds is %v; it is a whole number of seconds", v)
}

// Statement is one statement call in progress: the limit it runs under and
// when it started.
type Statement struct {
	tool    string
	limit   Limit
	started time.Time
}

// Begin decides the limit of a call to tool (Decide) and starts its clock; it
// is nil, and changes nothing, for a tool that runs no statement.
func Begin(tool string, args map[string]any, ceiling, remaining time.Duration) (*Statement, error) {
	if !IsStatementTool(tool) {
		return nil, nil //nolint:nilnil // no statement is not an error
	}
	limit, err := Decide(tool, args, ceiling, remaining)
	if err != nil {
		return nil, err
	}
	return &Statement{tool: tool, limit: limit, started: time.Now()}, nil
}

// End is the call's timing (Time), nil for no statement.
func (s *Statement) End(err error, site []string) *runstate.StatementTiming {
	if s == nil {
		return nil
	}
	return Time(s.tool, s.limit, time.Since(s.started), err, site)
}

// Time is how long a statement call took against its limit. When Trino
// stopped the statement at the limit it writes the limit into the error's
// envelope, which the failure's sentence names and a script that asked for
// its failures reads.
func Time(tool string, limit Limit, elapsed time.Duration, err error, site []string) *runstate.StatementTiming {
	timing := &runstate.StatementTiming{
		Tool: tool, ElapsedMS: elapsed.Milliseconds(),
		LimitMS: limit.Timeout.Milliseconds(), LimitSource: limit.Source, CallSite: site,
	}
	var refusal *scriptsession.RefusalError
	if errors.As(err, &refusal) {
		if st, ok := refusal.Envelope["statement_timeout"].(map[string]any); ok {
			stopped := maps.Clone(st)
			stopped["limit_ms"], stopped["limit_source"] = timing.LimitMS, limit.Source
			refusal.Envelope = maps.Clone(refusal.Envelope)
			refusal.Envelope["statement_timeout"] = stopped
			timing.TimedOut = true
			if ms, ok := st["elapsed_ms"].(float64); ok {
				timing.ElapsedMS = int64(ms)
			}
		}
		timing.QueryID, _ = refusal.Envelope["query_id"].(string)
	}
	if timing.LimitMS > 0 {
		timing.Share = math.Round(float64(timing.ElapsedMS)/float64(timing.LimitMS)*percent) / percent
	}
	return timing
}

// percent rounds a share to two decimals.
const percent = 100

// Slowest is the slower of two timings, the first when they tie; either may
// be nil.
func Slowest(current, next *runstate.StatementTiming) *runstate.StatementTiming {
	if current == nil || (next != nil && next.ElapsedMS > current.ElapsedMS) {
		return next
	}
	return current
}
