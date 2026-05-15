package connoauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

// Default cadence and lead times for the refresher. Each one is
// chosen to satisfy the most aggressive disclosed-or-configured IdP
// invalidation window the platform's documented targets enforce:
//
//   - Keycloak: 30-minute SSO Session Idle by default. A 5-minute
//     check interval with a 1-hour refresh-deadline lead time means
//     the refresher fires well before the idle clock can run out
//     unless the cluster is completely down for >30m.
//
//   - Salesforce / fixed wall-clock IdPs: refresh token has a fixed
//     lifetime from issuance (commonly 30 or 90 days, varies by org
//     policy). Combined with the per-connection
//     oauth2_refresh_max_lifetime hint, the refresher fires at most
//     refreshLeadTime ahead of the deadline.
//
//   - Microsoft Graph / Auth0 with sliding-window rotation: every
//     refresh resets the clock; firing on access-token expiry alone
//     keeps the chain alive indefinitely.
const (
	defaultRefresherInterval  = 5 * time.Minute
	defaultAccessLeadTime     = 5 * time.Minute
	defaultRefreshLeadTime    = 1 * time.Hour
	defaultLockHoldTimeout    = 60 * time.Second
	defaultRefreshTickTimeout = 90 * time.Second
)

// ConfigResolver is the small surface the refresher needs to obtain
// per-(kind, name) connoauth.Config and the operator-configured
// max-lifetime hint without importing the platform package (which
// would create an import cycle). The platform provides a concrete
// implementation that delegates to ConnectionStore + per-kind
// OAuthKindHandlers + the toolkit-specific config schema.
type ConfigResolver interface {
	// ResolveConfig returns the OAuth config for the persisted (kind,
	// name) row. Returns ErrConfigNotResolvable when the connection
	// no longer exists OR is no longer configured for
	// authorization_code OAuth — the refresher treats this as
	// "skip" rather than "fail" so a deleted-connection row can't
	// stop the loop from processing the rest.
	ResolveConfig(ctx context.Context, key Key) (Config, error)
	// MaxLifetime returns the operator-configured wall-clock max
	// lifetime for the refresh token (e.g., 30d for Salesforce, 90d
	// for Microsoft). Zero means rely on the IdP-disclosed
	// refresh_expires_at and access-token expiry only.
	MaxLifetime(ctx context.Context, key Key) time.Duration
}

// ErrConfigNotResolvable signals that the refresher should skip a row
// without treating it as a failure. Distinct sentinel so the loop's
// classifier doesn't have to string-match.
var ErrConfigNotResolvable = errors.New("connoauth: connection config not resolvable")

// AdvisoryLocker prevents two replicas from refreshing the same
// connection simultaneously. The Postgres implementation uses
// pg_try_advisory_lock — non-blocking, so a contended row is silently
// skipped on that tick; the next replica picks it up on its own tick.
//
// Single-replica deployments can pass NoopLocker to skip locking
// entirely. The refresher remains correct without locks; the cost is
// only redundant IdP round-trips when multiple replicas race.
type AdvisoryLocker interface {
	// TryLock attempts to acquire a non-blocking lock keyed on
	// (kind, name). Returns (release, true, nil) on acquire,
	// (nil, false, nil) when contended (some other replica owns it),
	// (nil, false, err) on operational failure (DB unreachable).
	//
	//nolint:revive // named returns are intentional on this contract
	TryLock(ctx context.Context, k Key) (release func(), ok bool, err error)
}

// NoopLocker always grants the lock. Use in single-replica
// deployments and in tests.
type NoopLocker struct{}

// TryLock always returns (no-op release, true, nil).
func (NoopLocker) TryLock(_ context.Context, _ Key) (release func(), ok bool, err error) {
	return func() {}, true, nil
}

// PostgresLocker wraps a *sql.DB with pg_try_advisory_lock semantics.
// The lock is held in the session associated with the *sql.Conn the
// locker checks out from the pool; release() closes the connection
// back to the pool which Postgres treats as a session end (and
// auto-releases the lock).
type PostgresLocker struct {
	db *sql.DB
}

// NewPostgresLocker wraps db with advisory-lock helpers. Multi-replica
// deployments pass this; single-replica or no-DB deployments pass
// NoopLocker{}.
func NewPostgresLocker(db *sql.DB) *PostgresLocker {
	return &PostgresLocker{db: db}
}

// TryLock acquires a session-scoped advisory lock keyed by
// hashtext('connoauth-refresh:<kind>/<name>'). The hash uses FNV
// here (Postgres-equivalent stability is not required because every
// lock key is held by the same process for at most one tick) and
// converts to a bigint for pg_try_advisory_lock.
func (l *PostgresLocker) TryLock(ctx context.Context, k Key) (release func(), ok bool, err error) {
	if !k.IsValid() {
		return nil, false, errInvalidKey
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("connoauth: locker checkout: %w", err)
	}
	keyID := advisoryLockKey(k)
	var acquired bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock($1)`, keyID).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("connoauth: try lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	release = func() {
		// Use a short detached context so the unlock doesn't depend
		// on the caller's ctx (a refresh that overran its tick
		// timeout would otherwise leak the lock for the duration of
		// the session-end-on-conn-close).
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, keyID); err != nil {
			slog.Warn("connoauth: advisory unlock failed (lock released on conn close)",
				logKeyKind, k.Kind, logKeyName, k.Name, logKeyError, err)
		}
		_ = conn.Close()
	}
	return release, true, nil
}

// advisoryLockKey hashes "connoauth-refresh:<kind>/<name>" into the
// int64 space pg_try_advisory_lock expects. Collisions across
// distinct keys are theoretically possible but practically
// negligible at the connection cardinality this platform deals with
// (low hundreds at the very most).
func advisoryLockKey(k Key) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("connoauth-refresh:"))
	_, _ = h.Write([]byte(k.Kind))
	_, _ = h.Write([]byte("/"))
	_, _ = h.Write([]byte(k.Name))
	// #nosec G115 -- intentional reinterpret cast: pg_try_advisory_lock
	// takes int8 (bigint), and we want the full 64-bit hash space; the
	// wraparound on the high bit is the desired behavior.
	return int64(h.Sum64()) //nolint:gosec // see comment
}

// RefresherConfig governs the refresher's cadence and lead times.
// Zero values fall back to the defaults declared above.
type RefresherConfig struct {
	// Interval is how often the loop wakes up. Default 5 minutes.
	// MUST be smaller than the smallest IdP idle window in scope.
	Interval time.Duration
	// AccessLeadTime is the duration before an access-token's
	// disclosed expiry at which the refresher will refresh. Default
	// 5 minutes.
	AccessLeadTime time.Duration
	// RefreshLeadTime is the duration before a refresh-token's
	// disclosed or synthetic expiry at which the refresher will
	// refresh. Default 1 hour.
	RefreshLeadTime time.Duration
}

func (r RefresherConfig) withDefaults() RefresherConfig {
	if r.Interval <= 0 {
		r.Interval = defaultRefresherInterval
	}
	if r.AccessLeadTime <= 0 {
		r.AccessLeadTime = defaultAccessLeadTime
	}
	if r.RefreshLeadTime <= 0 {
		r.RefreshLeadTime = defaultRefreshLeadTime
	}
	return r
}

// Refresher proactively refreshes OAuth tokens before they expire so
// connections survive arbitrary periods of inactivity (specialist-
// admin scenario: one operator connects, no one touches the
// connection for hours, day-to-day operators still see it working).
//
// The loop is correct without an AdvisoryLocker (multiple replicas
// will harmlessly race, occasionally producing redundant refreshes);
// passing a real locker is a cost optimization, not a correctness
// requirement.
type Refresher struct {
	store   Store
	configs ConfigResolver
	events  *authevents.Writer
	locker  AdvisoryLocker
	cfg     RefresherConfig
	now     func() time.Time // overridable for tests

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewRefresher constructs a Refresher. Pass NoopLocker{} for single-
// replica deployments. events may be nil (no-op writes).
func NewRefresher(store Store, configs ConfigResolver, events *authevents.Writer,
	locker AdvisoryLocker, cfg RefresherConfig,
) *Refresher {
	if locker == nil {
		locker = NoopLocker{}
	}
	return &Refresher{
		store:   store,
		configs: configs,
		events:  events,
		locker:  locker,
		cfg:     cfg.withDefaults(),
		now:     time.Now,
	}
}

// Start launches the refresher loop. Idempotent — repeat calls after
// the first are no-ops. The loop runs until Stop is called.
func (r *Refresher) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	done := make(chan struct{})
	r.done = done
	// Pass done explicitly so the loop's defer close(done) doesn't
	// race a concurrent Stop() that nils r.done.
	go r.loop(ctx, done)
	slog.Info("connoauth: refresher started",
		"interval", r.cfg.Interval,
		"access_lead", r.cfg.AccessLeadTime,
		"refresh_lead", r.cfg.RefreshLeadTime)
}

// Stop signals the loop to exit and waits for it. Safe to call even
// when the refresher was never started.
func (r *Refresher) Stop(ctx context.Context) error {
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("connoauth: refresher: stop wait: %w", ctx.Err())
	}
}

func (r *Refresher) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	// Fire once immediately so an operator who just deployed sees
	// the keepalive run without waiting a full interval. The ticker
	// then drives subsequent ticks.
	r.tick(ctx)
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// tick lists rows and processes each in turn. One per-row failure
// does not abort the tick — operators have multiple connections and
// a transient DB hiccup on one row must not stall keepalive on the
// others.
func (r *Refresher) tick(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, defaultRefreshTickTimeout)
	defer cancel()
	rows, err := r.store.List(ctx)
	if err != nil {
		slog.Warn("connoauth: refresher: list rows failed", logKeyError, err)
		return
	}
	for _, row := range rows {
		r.processRow(ctx, row)
	}
}

// processRow decides whether row needs refresh; if so, acquires the
// advisory lock and calls connoauth.Source to perform it. The
// per-row context is independent so a slow refresh on one row
// doesn't blow the whole tick.
func (r *Refresher) processRow(ctx context.Context, row PersistedToken) {
	if row.RefreshToken == "" {
		// No refresh token persisted — nothing the refresher can do.
		// Skip silently; an event is only emitted when the refresh
		// path actually runs.
		return
	}
	cfg, err := r.configs.ResolveConfig(ctx, row.Key)
	if err != nil {
		if errors.Is(err, ErrConfigNotResolvable) {
			return
		}
		slog.Warn("connoauth: refresher: resolve config failed",
			logKeyKind, row.Key.Kind, logKeyName, row.Key.Name, logKeyError, err)
		return
	}
	maxLife := r.configs.MaxLifetime(ctx, row.Key)
	if !r.shouldRefresh(row, maxLife) {
		return
	}
	release, ok, err := r.locker.TryLock(ctx, row.Key)
	if err != nil {
		slog.Warn("connoauth: refresher: lock failed",
			logKeyKind, row.Key.Kind, logKeyName, row.Key.Name, logKeyError, err)
		return
	}
	if !ok {
		// Contended: another replica is handling this row. Skip.
		return
	}
	defer release()

	lockedCtx, cancel := context.WithTimeout(ctx, defaultLockHoldTimeout)
	defer cancel()
	src := NewSource(r.store, row.Key, cfg).
		WithEvents(r.events).
		WithActor(authevents.SystemBackgroundRefresh)
	if err := src.Reacquire(lockedCtx); err != nil {
		// Reacquire's revoked path already emits its own events and
		// log lines. Transient errors are noted here so operators
		// can spot a systemic IdP outage across the cluster.
		if !errors.Is(err, ErrNeedsReauth) {
			slog.Warn("connoauth: refresher: reacquire failed (transient)",
				logKeyKind, row.Key.Kind, logKeyName, row.Key.Name, logKeyError, err)
		}
	}
}

// shouldRefresh decides whether a row's refresh-token chain needs
// proactive renewal NOW. The decision is the union of three
// conditions:
//
//   - access-token expiry within AccessLeadTime
//   - IdP-disclosed refresh-token expiry within RefreshLeadTime
//   - operator-configured max-lifetime within RefreshLeadTime of
//     (authenticated_at + max_lifetime)
//
// The last condition is the only defense against IdPs that don't
// disclose refresh deadlines (Google, default Salesforce) AND have
// a wall-clock max enforced server-side regardless (Microsoft,
// some Salesforce session policies).
func (r *Refresher) shouldRefresh(row PersistedToken, maxLife time.Duration) bool {
	now := r.now()
	// `!leadPoint.Before(deadline)` means "deadline at or before
	// leadPoint" = "we're within or already past the lead window."
	// `.After` would have excluded the exact-boundary case which is
	// the most common natural breakpoint a clock produces.
	if !row.ExpiresAt.IsZero() && !now.Add(r.cfg.AccessLeadTime).Before(row.ExpiresAt) {
		return true
	}
	if !row.RefreshExpiresAt.IsZero() && !now.Add(r.cfg.RefreshLeadTime).Before(row.RefreshExpiresAt) {
		return true
	}
	if maxLife > 0 && !row.AuthenticatedAt.IsZero() {
		synthetic := row.AuthenticatedAt.Add(maxLife)
		if !now.Add(r.cfg.RefreshLeadTime).Before(synthetic) {
			return true
		}
	}
	return false
}
