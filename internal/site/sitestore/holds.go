package sitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Holds on a site's stored objects (#2602). A publish stores its files before
// the transaction that records the release naming them, and a release sweep
// deletes the objects no kept release names after the transaction that
// prunes. Content-addressed objects make the two meet: a publish re-storing a
// file only a pruned release named writes the very key the sweep deletes. So
// a publish holds an upload from its first stored file until its release is
// recorded, and a sweep holds a sweep from before it decides until its
// deletes finish. Each is taken under the site's row lock and refused while
// the other is held, so a sweep never decides with a publish's files in
// flight and a publish never stores while a sweep deletes. Holds expire, so a
// process that died holding one blocks the other only until then; a live one
// is renewed while its work goes on, and a publish whose hold expired is
// refused rather than recorded.

const (
	holdUpload = "upload"
	holdSweep  = "sweep"
)

// The defaults of a hold's timing; a Store's zero fields take them.
const (
	// defaultUploadFor is how long an upload hold lasts past its last renewal.
	// A publish renews it as it stores files, and the longest stretch without
	// a stored file is a render, bounded at five minutes.
	defaultUploadFor = 15 * time.Minute
	// defaultSweepFor is how long a sweep hold lasts past its last renewal; a
	// sweep renews it between batches of deletes.
	defaultSweepFor = 10 * time.Minute
	// defaultRenewAfter is how old a hold's last renewal must be before it is
	// renewed again, so a publish of thousands of files writes a row a minute
	// rather than one per file.
	defaultRenewAfter = time.Minute
	// defaultHoldWait is how long a publish waits for a sweep's deletes before
	// it is refused, and defaultHoldPoll how often it looks.
	defaultHoldWait = time.Minute
	defaultHoldPoll = time.Second
)

var (
	// ErrCleanupRunning is a publish refused because a sweep held the site's
	// stored files for longer than a publish waits.
	ErrCleanupRunning = errors.New("the release sweep is removing this site's unused stored files; publish again in a few minutes")
	// ErrHoldExpired is a publish whose hold on the site's stored files ran
	// out before its release was recorded: a sweep may have run since, so the
	// release is not recorded.
	ErrHoldExpired = errors.New("the publish took longer than its hold on the site's stored files allows, so it was not recorded; publish again")
)

// holdTiming is a Store's hold timing; zero fields are the defaults.
type holdTiming struct {
	uploadFor, sweepFor, renewAfter, wait, poll time.Duration
	// after is the wait between a publish's tries, time.After unless a test
	// steps it.
	after func(time.Duration) <-chan time.Time
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Hold is one hold on a site's stored objects. A nil Hold holds nothing, and
// its methods do nothing, which is what a store without holds hands back.
type Hold struct {
	db         *sql.DB
	id         string
	ttl        time.Duration
	renewAfter time.Duration
	renewed    atomic.Int64
}

// HoldUpload holds a site's stored objects for a publish about to store files,
// waiting while a sweep holds them and refusing with ErrCleanupRunning past
// the wait.
func (s *Store) HoldUpload(ctx context.Context, siteID string) (*Hold, error) {
	wait, poll := orDefault(s.holds.wait, defaultHoldWait), orDefault(s.holds.poll, defaultHoldPoll)
	after := s.holds.after
	if after == nil {
		after = time.After
	}
	attempts := int(wait/poll) + 1
	for i := range attempts {
		h, err := s.takeHold(ctx, siteID, holdUpload)
		if h != nil || err != nil {
			return h, err
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for the release sweep: %w", ctx.Err())
		case <-after(poll):
		}
	}
	return nil, ErrCleanupRunning
}

// HoldSweep holds a site's stored objects for a sweep about to decide what to
// delete, or is nil when the site is busy: a publish holds its files, or a
// publish or copy job is running.
func (s *Store) HoldSweep(ctx context.Context, siteID string) (*Hold, error) {
	return s.takeHold(ctx, siteID, holdSweep)
}

// takeHold inserts a hold of one kind under the site's row lock, or is nil
// when a live hold of the other kind (or, for a sweep, a running job) stands
// in its way. Expired holds of the site are cleared on the way.
func (s *Store) takeHold(ctx context.Context, siteID, kind string) (*Hold, error) {
	ttl := orDefault(s.holds.uploadFor, defaultUploadFor)
	if kind == holdSweep {
		ttl = orDefault(s.holds.sweepFor, defaultSweepFor)
	}
	h := &Hold{db: s.db, id: "shold_" + uuid.NewString(), ttl: ttl, renewAfter: orDefault(s.holds.renewAfter, defaultRenewAfter)}
	taken := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM sites WHERE id = $1 FOR UPDATE`, siteID).Scan(new(int)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return site.ErrNotFound
			}
			return fmt.Errorf("lock site: %w", err)
		}
		var err error
		taken, err = insertHold(ctx, tx, h, siteID, kind)
		return err
	})
	if err != nil || !taken {
		return nil, err
	}
	h.renewed.Store(time.Now().UnixNano())
	return h, nil
}

// insertHold clears the site's expired holds and inserts h, under the site's
// row lock the caller holds; false when a live hold of the other kind (or,
// for a sweep, a running job) stands in its way.
func insertHold(ctx context.Context, tx *sql.Tx, h *Hold, siteID, kind string) (bool, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_object_holds WHERE site_id = $1 AND expires_at <= clock_timestamp()`, siteID); err != nil {
		return false, fmt.Errorf("clear expired holds: %w", err)
	}
	var blocked bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM site_object_holds WHERE site_id = $1 AND kind <> $2 AND expires_at > clock_timestamp())
		    OR ($2 = 'sweep' AND EXISTS (SELECT 1 FROM site_jobs WHERE site_id = $1 AND status = 'running'
		                                   AND updated_at > NOW() - make_interval(secs => $3)))`,
		siteID, kind, jobFresh.Seconds()).Scan(&blocked); err != nil {
		return false, fmt.Errorf("read the site's holds: %w", err)
	}
	if blocked {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO site_object_holds (id, site_id, kind, expires_at)
		VALUES ($1, $2, $3, clock_timestamp() + make_interval(secs => $4))`, h.id, siteID, kind, h.ttl.Seconds()); err != nil {
		return false, fmt.Errorf("hold the site's files: %w", err)
	}
	return true, nil
}

// Renew extends a live hold once its last renewal is old enough, and is
// ErrHoldExpired when the hold has run out: the work it covered must stop,
// since the other kind may have been taken since. Safe from many goroutines.
func (h *Hold) Renew(ctx context.Context) error {
	if h == nil || h.db == nil {
		return nil
	}
	last, now := h.renewed.Load(), time.Now().UnixNano()
	if time.Duration(now-last) < h.renewAfter || !h.renewed.CompareAndSwap(last, now) {
		return nil
	}
	res, err := h.db.ExecContext(ctx, `
		UPDATE site_object_holds SET expires_at = clock_timestamp() + make_interval(secs => $2)
		 WHERE id = $1 AND expires_at > clock_timestamp()`, h.id, h.ttl.Seconds())
	if err != nil {
		return fmt.Errorf("renew the hold on the site's files: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return ErrHoldExpired
	}
	return nil
}

// Release ends a hold. It is best effort: a hold left behind expires.
func (h *Hold) Release(ctx context.Context) {
	if h == nil || h.db == nil {
		return
	}
	_, _ = h.db.ExecContext(context.WithoutCancel(ctx), `DELETE FROM site_object_holds WHERE id = $1`, h.id)
}

type holdKey struct{}

// WithHold carries a publish's upload hold on its context: storing a file
// renews it (HoldOf(ctx).Renew), and RecordRelease checks it is still held in
// the transaction that records the release.
func WithHold(ctx context.Context, h *Hold) context.Context {
	return context.WithValue(ctx, holdKey{}, h)
}

// HoldOf is the hold a context carries, nil for none.
func HoldOf(ctx context.Context) *Hold {
	h, _ := ctx.Value(holdKey{}).(*Hold)
	return h
}

// checkHold refuses to record a release whose upload hold ran out, read
// under the site's row lock against the clock rather than the transaction's
// start, which may predate a wait for the lock.
func checkHold(ctx context.Context, tx *sql.Tx) error {
	h := HoldOf(ctx)
	if h == nil || h.db == nil {
		return nil
	}
	var live bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM site_object_holds WHERE id = $1 AND expires_at > clock_timestamp())`, h.id).Scan(&live); err != nil {
		return fmt.Errorf("read the publish's hold: %w", err)
	}
	if !live {
		return ErrHoldExpired
	}
	return nil
}
