package apichange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Change statuses (#2483, #2484).
const (
	StatusApplied    = "applied"    // the fetched spec replaced the copy in use
	StatusPending    = "pending"    // held for an administrator
	StatusDiscarded  = "discarded"  // an administrator discarded a held change
	StatusReported   = "reported"   // an upstream_url difference, never applied
	StatusSuperseded = "superseded" // a held change replaced by a newer one
)

// Triggers of a check.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// ErrNoChange is a change id that names no change of the spec.
var ErrNoChange = errors.New("apichange: no such change")

// ErrNotPending is a decision on a change that is not waiting for one.
var ErrNotPending = errors.New("apichange: the change is not waiting for review")

// SpecKey names one spec of one catalog.
type SpecKey struct {
	CatalogID string
	SpecName  string
}

// Watch is how one spec is kept current.
type Watch struct {
	SpecKey
	// RefreshInterval is how often the upstream is checked; zero is never.
	RefreshInterval time.Duration
	HoldChanges     bool
	// UpstreamURL is what an uploaded or pasted spec is compared with.
	UpstreamURL     string
	UpstreamETag    string
	UpstreamContent string
	NextCheckAt     *time.Time
	LastCheckedAt   *time.Time
	LastError       string
	UpdatedBy       string
	UpdatedAt       time.Time
}

// Change is one check that found the upstream different.
type Change struct {
	ID             string     `json:"id"`
	CatalogID      string     `json:"catalog_id"`
	SpecName       string     `json:"spec_name"`
	Status         string     `json:"status"`
	Trigger        string     `json:"trigger"`
	Report         Report     `json:"report"`
	Summary        string     `json:"summary"`
	CreatedAt      time.Time  `json:"created_at"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	PendingContent string     `json:"-"`
	PendingETag    string     `json:"-"`
}

// Store is the PostgreSQL store of watches, changes, warnings and callers.
type Store struct {
	DB *sql.DB
}

const watchColumns = `catalog_id, spec_name, refresh_interval_seconds, hold_changes, upstream_url,
	upstream_etag, upstream_content, next_check_at, last_checked_at, last_error, updated_by, updated_at`

// GetWatch returns a spec's watch; a spec never configured is its zero value.
func (s Store) GetWatch(ctx context.Context, k SpecKey) (Watch, error) {
	w, err := scanWatch(s.DB.QueryRowContext(ctx,
		`SELECT `+watchColumns+` FROM api_catalog_spec_watch WHERE catalog_id = $1 AND spec_name = $2`,
		k.CatalogID, k.SpecName))
	if errors.Is(err, sql.ErrNoRows) {
		return Watch{SpecKey: k}, nil
	}
	if err != nil {
		return Watch{}, fmt.Errorf("reading the watch of %s/%s: %w", k.CatalogID, k.SpecName, err)
	}
	return w, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanWatch(row rowScanner) (Watch, error) {
	var w Watch
	var seconds int
	var next, last sql.NullTime
	if err := row.Scan(&w.CatalogID, &w.SpecName, &seconds, &w.HoldChanges, &w.UpstreamURL,
		&w.UpstreamETag, &w.UpstreamContent, &next, &last, &w.LastError, &w.UpdatedBy, &w.UpdatedAt); err != nil {
		return Watch{}, err //nolint:wrapcheck // callers name the spec
	}
	w.RefreshInterval = time.Duration(seconds) * time.Second
	if next.Valid {
		w.NextCheckAt = &next.Time
	}
	if last.Valid {
		w.LastCheckedAt = &last.Time
	}
	return w, nil
}

// upsertSettingsSQL writes what an administrator sets. A changed upstream
// URL forgets the copy read from the old one, so the next check compares
// with the spec itself again; the next check is due one interval from now,
// or never.
const upsertSettingsSQL = `
	INSERT INTO api_catalog_spec_watch (catalog_id, spec_name, refresh_interval_seconds, hold_changes,
	                                    upstream_url, next_check_at, updated_by, updated_at)
	VALUES ($1, $2, $3, $4, $5, CASE WHEN $3 > 0 THEN NOW() + make_interval(secs => $3) END, $6, NOW())
	ON CONFLICT (catalog_id, spec_name) DO UPDATE SET
	    refresh_interval_seconds = EXCLUDED.refresh_interval_seconds,
	    hold_changes = EXCLUDED.hold_changes,
	    upstream_url = EXCLUDED.upstream_url,
	    upstream_etag = CASE WHEN api_catalog_spec_watch.upstream_url = EXCLUDED.upstream_url
	                         THEN api_catalog_spec_watch.upstream_etag ELSE '' END,
	    upstream_content = CASE WHEN api_catalog_spec_watch.upstream_url = EXCLUDED.upstream_url
	                            THEN api_catalog_spec_watch.upstream_content ELSE '' END,
	    next_check_at = EXCLUDED.next_check_at,
	    updated_by = EXCLUDED.updated_by,
	    updated_at = NOW()`

// SaveSettings stores an administrator's settings for a spec.
func (s Store) SaveSettings(ctx context.Context, w Watch, by string) error {
	_, err := s.DB.ExecContext(ctx, upsertSettingsSQL, w.CatalogID, w.SpecName,
		int(w.RefreshInterval/time.Second), w.HoldChanges, w.UpstreamURL, by)
	if err != nil {
		return fmt.Errorf("saving the watch of %s/%s: %w", w.CatalogID, w.SpecName, err)
	}
	return nil
}

// recordCheckSQL stamps a check: when, what failed (empty for none), the
// upstream copy and its ETag when one was read, and the next check one
// interval on.
const recordCheckSQL = `
	INSERT INTO api_catalog_spec_watch (catalog_id, spec_name, last_checked_at, last_error, upstream_etag, upstream_content)
	VALUES ($1, $2, NOW(), $3, $4, $5)
	ON CONFLICT (catalog_id, spec_name) DO UPDATE SET
	    last_checked_at = NOW(),
	    last_error = EXCLUDED.last_error,
	    upstream_etag = CASE WHEN $6 THEN EXCLUDED.upstream_etag ELSE api_catalog_spec_watch.upstream_etag END,
	    upstream_content = CASE WHEN $6 THEN EXCLUDED.upstream_content ELSE api_catalog_spec_watch.upstream_content END,
	    next_check_at = CASE WHEN api_catalog_spec_watch.refresh_interval_seconds > 0
	                         THEN NOW() + make_interval(secs => api_catalog_spec_watch.refresh_interval_seconds) END`

// CheckRecord is what one check leaves on the watch.
type CheckRecord struct {
	Err string
	// Upstream is set when the check read a new upstream copy to keep.
	Upstream     bool
	UpstreamETag string
	UpstreamBody string
}

// RecordCheck stamps a check on the spec's watch.
func (s Store) RecordCheck(ctx context.Context, k SpecKey, rec CheckRecord) error {
	_, err := s.DB.ExecContext(ctx, recordCheckSQL, k.CatalogID, k.SpecName, rec.Err,
		rec.UpstreamETag, rec.UpstreamBody, rec.Upstream)
	if err != nil {
		return fmt.Errorf("recording the check of %s/%s: %w", k.CatalogID, k.SpecName, err)
	}
	return nil
}

// dueSQL lists the specs whose next scheduled check has come, oldest first.
const dueSQL = `SELECT catalog_id, spec_name FROM api_catalog_spec_watch
	WHERE refresh_interval_seconds > 0 AND next_check_at <= NOW()
	ORDER BY next_check_at LIMIT $1`

// Due returns up to limit specs whose scheduled check has come.
func (s Store) Due(ctx context.Context, limit int) ([]SpecKey, error) {
	rows, err := s.DB.QueryContext(ctx, dueSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("listing the specs due a check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []SpecKey{}
	for rows.Next() {
		var k SpecKey
		if err := rows.Scan(&k.CatalogID, &k.SpecName); err != nil {
			return nil, fmt.Errorf("scanning a spec due a check: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the specs due a check: %w", err)
	}
	return out, nil
}

const changeColumns = `id, catalog_id, spec_name, status, trigger_kind, report, pending_content, pending_etag,
	created_at, decided_by, decided_at`

func scanChange(row rowScanner) (Change, error) {
	var c Change
	var report []byte
	var decided sql.NullTime
	if err := row.Scan(&c.ID, &c.CatalogID, &c.SpecName, &c.Status, &c.Trigger, &report,
		&c.PendingContent, &c.PendingETag, &c.CreatedAt, &c.DecidedBy, &decided); err != nil {
		return Change{}, err //nolint:wrapcheck // callers name the change
	}
	if err := json.Unmarshal(report, &c.Report); err != nil {
		return Change{}, fmt.Errorf("decoding change %s: %w", c.ID, err)
	}
	if decided.Valid {
		c.DecidedAt = &decided.Time
	}
	c.Summary = c.Report.Counts()
	return c, nil
}

// supersedeSQL retires the change a spec holds for review when a newer one
// takes its place.
const supersedeSQL = `UPDATE api_catalog_spec_changes
	SET status = 'superseded', pending_content = '', pending_etag = '', decided_at = NOW()
	WHERE catalog_id = $1 AND spec_name = $2 AND status = 'pending'`

const insertChangeSQL = `INSERT INTO api_catalog_spec_changes
	(id, catalog_id, spec_name, status, trigger_kind, report, pending_content, pending_etag)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// AddChange records a change. A pending one first retires the change the spec
// already holds, in the same transaction, so a spec holds one at a time.
func (s Store) AddChange(ctx context.Context, c Change) error {
	report, err := json.Marshal(c.Report)
	if err != nil {
		return fmt.Errorf("encoding the change report: %w", err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recording a change of %s/%s: %w", c.CatalogID, c.SpecName, err)
	}
	defer func() { _ = tx.Rollback() }()
	if c.Status == StatusPending {
		if _, err := tx.ExecContext(ctx, supersedeSQL, c.CatalogID, c.SpecName); err != nil {
			return fmt.Errorf("retiring the held change of %s/%s: %w", c.CatalogID, c.SpecName, err)
		}
	}
	if _, err := tx.ExecContext(ctx, insertChangeSQL, c.ID, c.CatalogID, c.SpecName, c.Status, c.Trigger,
		report, c.PendingContent, c.PendingETag); err != nil {
		return fmt.Errorf("recording a change of %s/%s: %w", c.CatalogID, c.SpecName, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("recording a change of %s/%s: %w", c.CatalogID, c.SpecName, err)
	}
	return nil
}

// ListChanges returns a spec's changes, newest first, at most limit.
func (s Store) ListChanges(ctx context.Context, k SpecKey, limit int) ([]Change, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+changeColumns+` FROM api_catalog_spec_changes
		WHERE catalog_id = $1 AND spec_name = $2 ORDER BY created_at DESC, id DESC LIMIT $3`,
		k.CatalogID, k.SpecName, limit)
	if err != nil {
		return nil, fmt.Errorf("listing the changes of %s/%s: %w", k.CatalogID, k.SpecName, err)
	}
	defer func() { _ = rows.Close() }()
	out := []Change{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning a change of %s/%s: %w", k.CatalogID, k.SpecName, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the changes of %s/%s: %w", k.CatalogID, k.SpecName, err)
	}
	return out, nil
}

// GetChange returns one change of a spec, or ErrNoChange.
func (s Store) GetChange(ctx context.Context, k SpecKey, id string) (Change, error) {
	c, err := scanChange(s.DB.QueryRowContext(ctx, `SELECT `+changeColumns+` FROM api_catalog_spec_changes
		WHERE id = $1 AND catalog_id = $2 AND spec_name = $3`, id, k.CatalogID, k.SpecName))
	if errors.Is(err, sql.ErrNoRows) {
		return Change{}, ErrNoChange
	}
	if err != nil {
		return Change{}, fmt.Errorf("reading change %s: %w", id, err)
	}
	return c, nil
}

// decideSQL settles a held change. Only a pending one moves, which is what
// makes two administrators deciding at once settle it once.
const decideSQL = `UPDATE api_catalog_spec_changes
	SET status = $4, decided_by = $5, decided_at = NOW(), pending_content = '', pending_etag = ''
	WHERE id = $1 AND catalog_id = $2 AND spec_name = $3 AND status = 'pending'`

// Decide moves a pending change to status (applied or discarded).
func (s Store) Decide(ctx context.Context, k SpecKey, id, status, by string) error {
	res, err := s.DB.ExecContext(ctx, decideSQL, id, k.CatalogID, k.SpecName, status, by)
	if err != nil {
		return fmt.Errorf("deciding change %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPending
	}
	return nil
}

// PendingFor reports which of a catalog's specs hold a change for review.
func (s Store) PendingFor(ctx context.Context, catalogID string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT spec_name, id FROM api_catalog_spec_changes
		WHERE catalog_id = $1 AND status = 'pending'`, catalogID)
	if err != nil {
		return nil, fmt.Errorf("listing the held changes of %s: %w", catalogID, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var spec, id string
		if err := rows.Scan(&spec, &id); err != nil {
			return nil, fmt.Errorf("scanning a held change of %s: %w", catalogID, err)
		}
		out[spec] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the held changes of %s: %w", catalogID, err)
	}
	return out, nil
}
