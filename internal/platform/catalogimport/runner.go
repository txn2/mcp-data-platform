package catalogimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// LockKey is the advisory lock a run holds, so two administrators, or two
// replicas, never import at once.
const LockKey int64 = 4713210202

// ErrRunning reports an import already under way.
var ErrRunning = errors.New("an import from DataHub is already running")

// ErrNoSource reports a start that names no DataHub when the configuration
// names none either.
var ErrNoSource = errors.New("the configuration names no DataHub (toolkits.datahub); give its url and token")

// The log keys a run's lines carry.
const (
	logImport = "import"
	logError  = "error"
)

// StartedByUpgrade is who a run the platform started on upgrade is recorded as.
const StartedByUpgrade = "platform (upgrade from DataHub)"

// SourceConfig names the DataHub an import reads.
type SourceConfig struct {
	URL   string
	Token string
}

// SourceFactory opens what an import reads, for the DataHub an administrator
// named.
type SourceFactory func(url, token string) (Source, error)

// Runner starts imports in the background, one at a time across replicas, and
// records each run.
type Runner struct {
	db       *sql.DB
	store    *Store
	importer *Importer
	rewrite  Rewrite
	open     SourceFactory
	// configured is the DataHub the configuration names, or nil.
	configured *SourceConfig

	mu     sync.Mutex
	ctx    context.Context //nolint:containedctx // the lifetime of the runs started by requests
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRunner builds a runner that imports with importer, rewrites with rw and
// reads the DataHub open returns.
func NewRunner(db *sql.DB, importer *Importer, rw Rewrite, open SourceFactory) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{db: db, store: NewStore(db), importer: importer, rewrite: rw, open: open, ctx: ctx, cancel: cancel}
}

// Configure names the DataHub the configuration names: the one an upgrade
// imports from, and the one a start that names none reads.
func (rn *Runner) Configure(src *SourceConfig) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.configured = src
}

// source is the configured DataHub, or nil.
func (rn *Runner) source() *SourceConfig {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	return rn.configured
}

// StartConfigured begins an import from the DataHub the configuration names,
// answering ErrNoSource when it names none.
func (rn *Runner) StartConfigured(ctx context.Context, by string) (Run, error) {
	src := rn.source()
	if src == nil {
		return Run{}, ErrNoSource
	}
	return rn.Start(ctx, src.URL, src.Token, by)
}

// ImportOnUpgrade begins an import from the DataHub the configuration names
// unless a run against it has finished without error. It is called after
// each catalog scan, so the import reads a catalog that holds the
// deployment's datasets, and a failed import is tried again after the next
// scan. Another replica's run under way is left to finish.
func (rn *Runner) ImportOnUpgrade(ctx context.Context) {
	src := rn.source()
	if src == nil {
		return
	}
	done, err := rn.store.imported(ctx, src.URL)
	if err != nil {
		slog.WarnContext(ctx, "catalog import: reading whether DataHub was imported failed",
			logError, logsan.SanitizeForLog(err.Error()))
		return
	}
	if done {
		return
	}
	run, err := rn.Start(ctx, src.URL, src.Token, StartedByUpgrade)
	switch {
	case errors.Is(err, ErrRunning):
	case err != nil:
		slog.WarnContext(ctx, "catalog import: starting the import from DataHub failed",
			logError, logsan.SanitizeForLog(err.Error()))
	default:
		slog.InfoContext(ctx, "catalog import: importing DataHub into the platform catalog",
			logImport, run.ID, "datahub", logsan.SanitizeForLog(src.URL))
	}
}

// Start begins an import from the DataHub at url and returns its run. It
// answers ErrRunning while another run holds the lock, and an error naming
// the problem when the DataHub cannot be opened.
func (rn *Runner) Start(ctx context.Context, url, token, by string) (Run, error) {
	src, err := rn.open(url, token)
	if err != nil {
		return Run{}, fmt.Errorf("opening DataHub at %s: %w", url, err)
	}
	conn, err := rn.db.Conn(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("taking a connection for the import's lock: %w", err)
	}
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", LockKey).Scan(&got); err != nil {
		_ = conn.Close()
		return Run{}, fmt.Errorf("taking the import's lock: %w", err)
	}
	if !got {
		_ = conn.Close()
		return Run{}, ErrRunning
	}
	run, err := rn.store.begin(ctx, url, by)
	if err != nil {
		release(conn)
		return Run{}, err
	}
	rn.mu.Lock()
	runCtx := rn.ctx
	rn.wg.Add(1)
	rn.mu.Unlock()
	go func() {
		defer rn.wg.Done()
		defer release(conn)
		rn.execute(runCtx, run.ID, src)
	}()
	return run, nil
}

// execute runs one import and records how it ended.
func (rn *Runner) execute(ctx context.Context, id string, src Source) {
	report, err := rn.importer.Run(ctx, src, rn.rewrite)
	failure := ""
	if err != nil {
		failure = err.Error()
		slog.ErrorContext(ctx, "catalog import failed", logImport, id, logError, logsan.SanitizeForLog(failure))
	} else {
		slog.InfoContext(ctx, "catalog import finished", logImport, id,
			"datasets", report.Datasets.Imported, "aspects_written", report.AspectsWritten)
	}
	if err := rn.store.finish(context.WithoutCancel(ctx), id, report, failure); err != nil {
		slog.ErrorContext(ctx, "recording the catalog import's end failed", logImport, id,
			logError, logsan.SanitizeForLog(err.Error()))
	}
}

// release lets go of the lock and its connection.
func release(conn *sql.Conn) {
	_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", LockKey)
	_ = conn.Close()
}

// Runs lists the latest runs, newest first, saying which is under way.
func (rn *Runner) Runs(ctx context.Context, limit int) ([]Run, error) {
	running, err := rn.locked(ctx)
	if err != nil {
		return nil, err
	}
	return rn.store.Runs(ctx, limit, running)
}

// locked reports whether some replica holds the import's lock.
func (rn *Runner) locked(ctx context.Context) (bool, error) {
	conn, err := rn.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("taking a connection to read the import's lock: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", LockKey).Scan(&got); err != nil {
		return false, fmt.Errorf("reading the import's lock: %w", err)
	}
	if got {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", LockKey)
	}
	return !got, nil
}

// Configured is the address of the DataHub the configuration names, or "".
func (rn *Runner) Configured() string {
	if src := rn.source(); src != nil {
		return src.URL
	}
	return ""
}

// Stop cancels a run under way and waits for it to record its end.
func (rn *Runner) Stop() {
	if rn == nil {
		return
	}
	rn.mu.Lock()
	rn.cancel()
	rn.mu.Unlock()
	rn.wg.Wait()
}
