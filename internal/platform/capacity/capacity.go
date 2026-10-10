// Package capacity reports how full the platform's database and buckets are
// and whether objects and rows still agree (#1899). Running out of disk or
// bucket quota is one of the commonest real outages, and before it nothing
// reported either.
//
// Three loops run on every replica:
//
//   - the table sampler reads each growing table's size and row estimate, each
//     HNSW index's size and the transaction id age from the catalog
//     (MCP_PLATFORM_CAPACITY_INTERVAL, default 15m);
//   - the usage flush adds the puts and deletes this replica made to the shared
//     storage_usage rows and reads every row back (every 10s), so an upload
//     shows within seconds on every replica;
//   - the full listing walks every platform prefix, resets the usage rows to
//     what it found and counts orphaned objects and dangling rows
//     (MCP_PLATFORM_STORAGE_SCAN_INTERVAL, default 6h). It runs on one replica
//     at a time, under an advisory lock.
//
// Between listings a usage row is the last listing plus the writes since. A
// delete removes its object from the count but not its bytes, which the
// listing corrects.
package capacity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Defaults for the loops.
const (
	DefaultTableInterval = 15 * time.Minute
	DefaultScanInterval  = 6 * time.Hour
	DefaultFlushInterval = 10 * time.Second
	DefaultOrphanGrace   = 15 * time.Minute

	// scanLockKey is the full listing's advisory lock, distinct from every
	// other maintenance lock (retention 4713210101-4713210105, the call
	// catalog 4713210001, maps 4713210301).
	scanLockKey int64 = 4713210401

	unlockTimeout = 5 * time.Second
)

// Sources is what the service reads.
type Sources struct {
	DB     *sql.DB
	Layout Layout
	// Walkers maps each platform bucket to the client that lists it.
	Walkers map[string]Walker
	// Endpoints maps each platform bucket to the S3 endpoint of the
	// connection it is reached through, each asked once which object store it
	// is unless Config.Backend names it (DetectBackend). The portal's and the
	// managed resources' buckets may be on different stores.
	Endpoints map[string]string
}

// Service runs the loops and holds the last readings.
type Service struct {
	src      Sources
	cfg      Config
	recorder *Recorder
	scanner  *Scanner

	mu         sync.RWMutex
	database   observability.DatabaseCapacity
	storage    observability.StorageCapacity
	backends   map[string]string // by bucket
	detectedAt time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
	now    func() time.Time
}

// Start installs the usage recorder, registers the service as m's capacity
// sampler and starts the loops. With no database or no metrics it does
// nothing and returns a service whose Close is a no-op.
func Start(m *observability.Metrics, src Sources, cfg Config) *Service {
	s := newService(src, cfg)
	if m == nil || src.DB == nil {
		return s
	}
	objectobs.SetUsageRecorder(s.recorder)
	m.RegisterCapacity(s.Sample)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, l := range []bgloop.Loop{
		{Name: bgloop.NameCapacityTables, Every: s.cfg.TableInterval, Immediate: true, Body: s.sampleTables},
		{Name: bgloop.NameStorageUsageFlush, Every: s.cfg.FlushInterval, Immediate: true, Body: s.flush},
		{Name: bgloop.NameStorageScan, Every: s.cfg.ScanInterval, Immediate: true, Body: s.scan},
	} {
		s.wg.Go(func() { bgloop.Run(ctx, l) })
	}
	return s
}

// newService builds the service without starting it.
func newService(src Sources, cfg Config) *Service {
	cfg = cfg.withDefaults()
	return &Service{
		src: src, cfg: cfg, recorder: NewRecorder(src.Layout),
		scanner: &Scanner{DB: src.DB, Layout: src.Layout, Walkers: src.Walkers, OrphanGrace: cfg.OrphanGrace},
	}
}

// Close stops the loops, writes what the recorder still holds, and removes
// it. Nil-safe.
func (s *Service) Close(ctx context.Context) error {
	if s == nil || s.cancel == nil {
		return nil
	}
	s.cancel()
	s.wg.Wait()
	objectobs.SetUsageRecorder(nil)
	return s.recorder.Flush(ctx, s.src.DB)
}

// Sample is the scrape-time read: the last readings, never a query.
func (s *Service) Sample(_ context.Context) observability.CapacitySample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := observability.CapacitySample{Database: s.database, Storage: s.storage}
	out.Storage.Budgets = s.cfg.Budgets
	out.Storage.Backends = distinctBackends(s.backends)
	return out
}

// sampleTables is one iteration of the table sampler.
func (s *Service) sampleTables(ctx context.Context) error {
	d, err := SampleTables(ctx, s.src.DB)
	if err != nil {
		slog.WarnContext(ctx, "capacity: sampling tables failed", "error", logsan.SanitizeForLog(err.Error()))
		return err
	}
	s.mu.Lock()
	s.database = d
	s.mu.Unlock()
	return nil
}

// flush is one iteration of the usage flush: write this replica's deltas,
// then read every row back.
func (s *Service) flush(ctx context.Context) error {
	if s.shouldDetect() {
		backends := s.detectBackends(ctx)
		s.mu.Lock()
		s.backends, s.detectedAt = backends, s.clock()
		s.mu.Unlock()
	}
	ferr := s.recorder.Flush(ctx, s.src.DB)
	s.mu.RLock()
	backends := s.backends
	s.mu.RUnlock()
	st, rerr := ReadStorage(ctx, s.src.DB, backends)
	if rerr == nil {
		s.mu.Lock()
		s.storage = st
		s.mu.Unlock()
	}
	if err := errors.Join(ferr, rerr); err != nil {
		slog.WarnContext(ctx, "capacity: storage usage flush failed", "error", logsan.SanitizeForLog(err.Error()))
		return fmt.Errorf("capacity: %w", err)
	}
	return nil
}

// backendRetry is how long a bucket whose store did not name itself waits
// before it is asked again: a store still starting when the platform did
// answers a later ask.
const backendRetry = 5 * time.Minute

// shouldDetect reports whether the buckets' stores are to be asked: never
// asked yet, or one read as other from an endpoint that may yet answer.
func (s *Service) shouldDetect() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.backends == nil {
		return true
	}
	if s.cfg.Backend != "" || s.clock().Sub(s.detectedAt) < backendRetry {
		return false
	}
	for bucket, kind := range s.backends {
		if kind == observability.StorageBackendOther && s.src.Endpoints[bucket] != "" {
			return true
		}
	}
	return false
}

// clock is now, overridable in tests.
func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// detectBackends names the object store each listed bucket is on: the
// configured kind for all of them when Config.Backend is set, otherwise each
// bucket's endpoint asked once (a store two buckets share is asked once).
func (s *Service) detectBackends(ctx context.Context) map[string]string {
	out := make(map[string]string, len(s.src.Walkers))
	asked := map[string]string{}
	for bucket := range s.src.Walkers {
		if s.cfg.Backend != "" {
			out[bucket] = s.cfg.Backend
			continue
		}
		endpoint := s.src.Endpoints[bucket]
		kind, ok := asked[endpoint]
		if !ok {
			kind = DetectBackend(ctx, endpoint)
			asked[endpoint] = kind
		}
		out[bucket] = kind
	}
	return out
}

// distinctBackends is each object store kind the buckets are on, once, sorted.
func distinctBackends(byBucket map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, kind := range byBucket {
		if kind != "" && !seen[kind] {
			seen[kind] = true
			out = append(out, kind)
		}
	}
	slices.Sort(out)
	return out
}

// scan is one iteration of the full listing, under its lock. A replica that
// finds the lock held skips.
func (s *Service) scan(ctx context.Context) error {
	if len(s.src.Walkers) == 0 {
		return bgloop.ErrSkipped
	}
	ran, err := s.locked(ctx, func(ctx context.Context) error {
		recent, err := s.listedRecently(ctx)
		if err != nil {
			return err
		}
		if recent {
			return errListedRecently
		}
		// This replica's writes go into the rows before the listing reads
		// them, so the listing's snapshot already holds them.
		if err := s.recorder.Flush(ctx, s.src.DB); err != nil {
			return err
		}
		res, err := s.scanner.Scan(ctx)
		if err != nil {
			return err
		}
		return res.Save(ctx, s.src.DB)
	})
	switch {
	case errors.Is(err, errListedRecently), err == nil && !ran:
		// Skipped, not succeeded: the loop's last success then means a
		// listing really finished, which is what a stalled listing is
		// alerted on.
		return bgloop.ErrSkipped
	case err != nil:
		slog.WarnContext(ctx, "capacity: bucket listing failed", "error", logsan.SanitizeForLog(err.Error()))
		return err
	}
	return s.flush(ctx)
}

// recentQuery reports whether the last listing finished within $1 seconds.
const recentQuery = `SELECT COALESCE(bool_or(finished_at > NOW() - make_interval(secs => $1)), false) FROM storage_scans`

// listedRecently reports whether a listing finished within most of the scan
// interval: every replica runs the loop, at its own start and on its own
// timer, and only one listing per interval is wanted. Nine tenths of it, so a
// timer that fires a little early does not skip its turn.
func (s *Service) listedRecently(ctx context.Context) (bool, error) {
	var recent bool
	window := s.cfg.ScanInterval.Seconds() * recentFraction
	if err := s.src.DB.QueryRowContext(ctx, recentQuery, window).Scan(&recent); err != nil {
		return false, fmt.Errorf("reading the last listing: %w", err)
	}
	return recent, nil
}

// errListedRecently is a listing skipped because another finished within the
// interval.
var errListedRecently = errors.New("capacity: listed recently")

// recentFraction is the share of the scan interval a listing counts as
// recent for.
const recentFraction = 0.9

// locked runs fn while holding the listing's advisory lock on a dedicated
// connection, since an advisory lock belongs to the session that took it.
func (s *Service) locked(ctx context.Context, fn func(context.Context) error) (bool, error) {
	conn, err := s.src.DB.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("acquiring a connection for the lock: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", scanLockKey).Scan(&got); err != nil {
		return false, fmt.Errorf("taking the advisory lock: %w", err)
	}
	if !got {
		return false, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", scanLockKey); err != nil {
			slog.WarnContext(ctx, "capacity: advisory unlock failed", "error", logsan.SanitizeForLog(err.Error()))
		}
	}()
	return true, fn(ctx)
}
