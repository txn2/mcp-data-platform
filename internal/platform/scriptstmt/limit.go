// Package scriptstmt decides how long a managed script's statement may run
// and records how long it took (#2073): the scripts.sql ceiling a deployment
// sets, the timeout one trino_query or trino_execute call runs under (the
// script's timeout_seconds, the tools' default, and never past the time left
// in the run), and the timing get_run reports the slowest of. It also writes
// the limit into the error of a statement Trino stopped at it, which the
// failure's sentence names. Extracted from internal/platform/scriptrun for its
// size budget.
package scriptstmt

import (
	"fmt"
	"time"
)

// The tools whose statements run under a timeout the host decides, and the
// argument the timeout is passed in.
const (
	ToolQuery   = "trino_query"
	ToolExecute = "trino_execute"
	ArgTimeout  = "timeout_seconds"
)

// IsStatementTool reports whether tool runs a statement under a timeout the
// host decides.
func IsStatementTool(tool string) bool { return tool == ToolQuery || tool == ToolExecute }

// The statement timeouts trino_query and trino_execute apply (mcp-trino):
// the one a call that names no timeout_seconds runs under, and the most a
// call may name.
const (
	ToolDefaultTimeout = 120 * time.Second
	ToolMaxTimeout     = 300 * time.Second
)

// MaxTimeoutKey is the configuration key that lowers the timeout a script's
// statement may ask for.
const MaxTimeoutKey = "scripts.sql.max_timeout_seconds"

// runMargin is kept between a statement's timeout and the run's deadline, so
// a statement that runs out of time is stopped by its own timeout, and its
// cancel confirmed, before the run is stopped by its own (#2073).
const runMargin = 5 * time.Second

// Config is the scripts.sql section (#2073).
type Config struct {
	// MaxTimeoutSeconds is the most a script's platform.query,
	// platform.execute or platform.call to trino_query or trino_execute may
	// pass as timeout_seconds. Zero takes the tools' own cap, 300 seconds,
	// which is also the most it may be: the tools cut anything longer.
	MaxTimeoutSeconds int `yaml:"max_timeout_seconds"`
}

// Validate refuses a ceiling the tools cannot honor.
func (c Config) Validate() error {
	maxSeconds := int(ToolMaxTimeout / time.Second)
	if c.MaxTimeoutSeconds < 0 || c.MaxTimeoutSeconds > maxSeconds {
		return fmt.Errorf("setting %s is %d; it is between 1 and %d, the most trino_query and trino_execute accept, or 0 for %d",
			MaxTimeoutKey, c.MaxTimeoutSeconds, maxSeconds, maxSeconds)
	}
	return nil
}

// MaxTimeout is the configured ceiling, the tools' cap when unset.
func (c Config) MaxTimeout() time.Duration {
	if c.MaxTimeoutSeconds <= 0 {
		return ToolMaxTimeout
	}
	return time.Duration(c.MaxTimeoutSeconds) * time.Second
}

// Limit is the timeout one statement runs under and where it came from.
type Limit struct {
	Timeout time.Duration
	// Source is "default" (the call named none), "requested", or
	// "run_remaining" when the run's deadline is nearer than either.
	Source string
}

// Where a statement's limit came from. LimitClamped is a request above the
// ceiling run at the ceiling, which only a version saved before the ceiling
// was enforced gets (#2085).
const (
	LimitDefault      = "default"
	LimitRequested    = "requested"
	LimitRunRemaining = "run_remaining"
	LimitClamped      = "clamped"
)

// StatementLimit decides the timeout a statement runs under: the requested
// seconds (zero is the tools' default), refused by name above ceiling, and
// cut to the run's remaining time less a margin, so the run outlasts its
// statement. A request is never cut to the ceiling: a script that asks for
// more than the deployment allows is told so.
func StatementLimit(requested int, ceiling, remaining time.Duration) (Limit, error) {
	if ceiling <= 0 {
		ceiling = ToolMaxTimeout
	}
	if requested < 0 {
		return Limit{}, fmt.Errorf("timeout_seconds is %d; it is a number of seconds above 0", requested)
	}
	limit := Limit{Timeout: min(ToolDefaultTimeout, ceiling), Source: LimitDefault}
	if requested > 0 {
		asked := time.Duration(requested) * time.Second
		if asked > ceiling {
			return Limit{}, fmt.Errorf("timeout_seconds is %d; the most a statement may run on this deployment is %d (%s)",
				requested, int(ceiling/time.Second), MaxTimeoutKey)
		}
		limit = Limit{Timeout: asked, Source: LimitRequested}
	}
	if left := remaining - runMargin; left < limit.Timeout {
		limit = Limit{Timeout: max(left, time.Second), Source: LimitRunRemaining}
	}
	return limit, nil
}

// ClampTo lowers a timeout_seconds in args above ceiling to the ceiling and
// returns the seconds that were asked for, zero when it lowered nothing (an
// absent, unreadable or allowed value is left for Decide). It is applied only
// to a run of a version saved before the ceiling was enforced (#2085): such a
// version ran for as long as it existed with mcp-trino cutting the request to
// 300 seconds, so it keeps running at the ceiling instead of failing every run
// after an upgrade. A new version is refused above the ceiling by Decide.
func ClampTo(args map[string]any, ceiling time.Duration) int {
	if ceiling <= 0 {
		ceiling = ToolMaxTimeout
	}
	requested, err := requestedSeconds(args[ArgTimeout])
	if err != nil || time.Duration(requested)*time.Second <= ceiling {
		return 0
	}
	args[ArgTimeout] = int(ceiling / time.Second)
	return requested
}

// Seconds is the limit as the tools' timeout_seconds, rounded up so a
// statement is never given less than its limit.
func (l Limit) Seconds() int {
	return int((l.Timeout + time.Second - 1) / time.Second)
}
