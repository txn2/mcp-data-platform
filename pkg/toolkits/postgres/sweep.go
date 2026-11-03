package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/internal/advisorylock"
	"github.com/txn2/mcp-data-platform/internal/bgloop"
)

// The sweep of throwaway databases a dead script test left behind (#2407).
// Every replica runs it; an advisory lock on the platform's database lets one
// sweep at a time.
const (
	// TestSweepLockKey is the platform-database advisory lock the sweep holds.
	TestSweepLockKey int64 = 4713210601
	// DefaultTestSweepInterval is how often the sweep runs.
	DefaultTestSweepInterval = 10 * time.Minute
	// DefaultTestMaxAge is how old a build or clone is before the sweep
	// drops it: far longer than any test runs.
	DefaultTestMaxAge = time.Hour
)

// The environment the sweep reads, beside the other MCP_PLATFORM_*
// operational knobs.
const (
	EnvTestSweepInterval = "MCP_PLATFORM_APPDB_TEST_SWEEP_INTERVAL"
	EnvTestMaxAge        = "MCP_PLATFORM_APPDB_TEST_MAX_AGE"
)

// testSweepFromEnv reads the sweep's cadence and the age it drops at; an
// unset value takes its default, and one that does not parse is logged and
// takes its default.
func testSweepFromEnv() (interval, maxAge time.Duration) {
	return envDuration(EnvTestSweepInterval, DefaultTestSweepInterval), envDuration(EnvTestMaxAge, DefaultTestMaxAge)
}

func envDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("appdb: ignoring an unreadable duration; using the default", "variable", name, "default", def.String())
		return def
	}
	return d
}

// StartTestSweep runs the sweep every MCP_PLATFORM_APPDB_TEST_SWEEP_INTERVAL,
// dropping builds and clones older than MCP_PLATFORM_APPDB_TEST_MAX_AGE, until
// the toolkit closes. The first pass runs at once, so a restart clears what
// the process it replaced left. It starts once.
func (t *Toolkit) StartTestSweep(platform *sql.DB) {
	interval, maxAge := testSweepFromEnv()
	t.runTestSweep(platform, interval, maxAge)
}

// runTestSweep is StartTestSweep with its cadence and age given.
func (t *Toolkit) runTestSweep(platform *sql.DB, interval, maxAge time.Duration) {
	if platform == nil {
		return
	}
	t.sweepOnce.Do(func() {
		stop := make(chan struct{})
		t.sweepMu.Lock()
		t.sweepStop = stop
		t.sweepMu.Unlock()
		go bgloop.Run(context.Background(), bgloop.Loop{
			Name: bgloop.NameAppDBTestSweep, Every: interval, Immediate: true, Stop: stop,
			Body: func(ctx context.Context) error { return t.sweepOnceLocked(ctx, platform, maxAge) },
		})
	})
}

// sweepOnceLocked runs one sweep under the lock, or skips when another
// replica holds it.
func (t *Toolkit) sweepOnceLocked(ctx context.Context, platform *sql.DB, maxAge time.Duration) error {
	release, got, err := advisorylock.Try(ctx, platform, advisorylock.ID(TestSweepLockKey))
	if err != nil {
		return fmt.Errorf("appdb: taking the test sweep lock: %w", err)
	}
	if !got {
		return bgloop.ErrSkipped
	}
	defer release()
	n, err := t.SweepTestDatabases(ctx, maxAge)
	bgloop.Purged(ctx, bgloop.NameAppDBTestSweep, int64(n))
	return err
}

// stopSweep ends the sweep, when one runs.
func (t *Toolkit) stopSweep() {
	t.sweepMu.Lock()
	defer t.sweepMu.Unlock()
	if t.sweepStop != nil {
		close(t.sweepStop)
		t.sweepStop = nil
	}
}
