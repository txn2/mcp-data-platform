package maps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

// The states a region's last fetch can be in.
const (
	StateQueued   = "queued"
	StateFetching = "fetching"
	StateReady    = "ready"
	StateFailed   = "failed"
)

// The ways a region came to be.
const (
	OriginFetch  = "fetch"  // extracted by the platform from the source build
	OriginUpload = "upload" // a .pmtiles file the operator put in the bucket
)

// Region is one basemap region and the archive serving it.
type Region struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Preset  string         `json:"preset,omitempty"`
	Origin  string         `json:"origin"`
	Bounds  pmtiles.Bounds `json:"bounds"`
	MaxZoom int            `json:"max_zoom"`
	// State is the last fetch's: queued, fetching, ready or failed. A failed
	// refresh of a region that was ready keeps serving Archive.
	State         string     `json:"state"`
	ProgressBytes int64      `json:"progress_bytes"`
	TotalBytes    int64      `json:"total_bytes"`
	Error         string     `json:"error,omitempty"`
	RequestedAt   time.Time  `json:"requested_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Archive       *Archive   `json:"archive,omitempty"`
	CreatedBy     string     `json:"created_by,omitempty"`
}

// Archive is the archive a region serves.
type Archive struct {
	Bucket string `json:"-"`
	Key    string `json:"-"`
	Size   int64  `json:"size_bytes"`
	// Build is the OpenStreetMap data the archive was built from, as the
	// source's metadata dates it (the replication time), or the source build's
	// name when it carries none.
	Build   string         `json:"build"`
	MinZoom int            `json:"min_zoom"`
	MaxZoom int            `json:"max_zoom"`
	Bounds  pmtiles.Bounds `json:"bounds"`
	ReadyAt time.Time      `json:"ready_at"`
}

// ETag is the archive's validator for a range read: the object key, which a
// refresh changes.
func (a Archive) ETag() string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(a.Bucket + "/" + a.Key))
	return fmt.Sprintf(`"%x"`, h.Sum64())
}

var regionID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidRegionID reports whether id can name a region: it is the archive's file
// name in the public route, so it is lowercase letters, digits and dashes.
func ValidRegionID(id string) bool { return regionID.MatchString(id) }

// Errors the region store reports.
var (
	ErrRegionNotFound = errors.New("maps: no such region")
	ErrRegionExists   = errors.New("maps: a region with this id already exists")
	ErrRegionBusy     = errors.New("maps: the region is being fetched")
)

// RegionStore persists regions.
type RegionStore interface {
	List(ctx context.Context) ([]Region, error)
	Get(ctx context.Context, id string) (*Region, error)
	Create(ctx context.Context, r Region) error
	Delete(ctx context.Context, id string) (*Region, error)
	Requeue(ctx context.Context, id string, maxZoom int) error
	ResetStale(ctx context.Context) error
	ClaimNext(ctx context.Context) (*Region, error)
	Progress(ctx context.Context, id string, done, total int64) error
	Finish(ctx context.Context, id string, a Archive) (previous *Archive, ok bool, err error)
	Fail(ctx context.Context, id, reason string) error
	PutUpload(ctx context.Context, r Region) error
	DeleteUploadsExcept(ctx context.Context, keep []string) ([]Region, error)
}

// errFinishing wraps a failure to make a fetched archive the one served.
const errFinishing = "finishing map fetch: %w"

// PostgresRegions is the map_regions table.
type PostgresRegions struct {
	db *sql.DB
}

// NewPostgresRegions returns the region store over db.
func NewPostgresRegions(db *sql.DB) *PostgresRegions {
	return &PostgresRegions{db: db}
}

const regionColumns = `id, name, preset, origin, min_lon, min_lat, max_lon, max_lat, max_zoom,
	state, progress_bytes, total_bytes, error, requested_at, started_at, finished_at,
	archive_bucket, archive_key, archive_size, archive_build, archive_min_zoom, archive_max_zoom,
	archive_min_lon, archive_min_lat, archive_max_lon, archive_max_lat, archive_ready_at, created_by`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRegion(row rowScanner) (*Region, error) {
	var (
		r       Region
		a       Archive
		readyAt sql.NullTime
		started sql.NullTime
		ended   sql.NullTime
	)
	err := row.Scan(&r.ID, &r.Name, &r.Preset, &r.Origin,
		&r.Bounds.MinLon, &r.Bounds.MinLat, &r.Bounds.MaxLon, &r.Bounds.MaxLat, &r.MaxZoom,
		&r.State, &r.ProgressBytes, &r.TotalBytes, &r.Error, &r.RequestedAt, &started, &ended,
		&a.Bucket, &a.Key, &a.Size, &a.Build, &a.MinZoom, &a.MaxZoom,
		&a.Bounds.MinLon, &a.Bounds.MinLat, &a.Bounds.MaxLon, &a.Bounds.MaxLat, &readyAt, &r.CreatedBy)
	if err != nil {
		return nil, err //nolint:wrapcheck // callers wrap with what they were reading
	}
	if started.Valid {
		r.StartedAt = &started.Time
	}
	if ended.Valid {
		r.FinishedAt = &ended.Time
	}
	if readyAt.Valid {
		a.ReadyAt = readyAt.Time
		r.Archive = &a
	}
	return &r, nil
}

// List returns every region, by name.
func (s *PostgresRegions) List(ctx context.Context) ([]Region, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+regionColumns+` FROM map_regions ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("listing map regions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Region, 0)
	for rows.Next() {
		r, err := scanRegion(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a map region: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing map regions: %w", err)
	}
	return out, nil
}

// Get returns one region.
func (s *PostgresRegions) Get(ctx context.Context, id string) (*Region, error) {
	r, err := scanRegion(s.db.QueryRowContext(ctx, `SELECT `+regionColumns+` FROM map_regions WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRegionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading map region: %w", err)
	}
	return r, nil
}

// Create adds a region queued for its first fetch.
func (s *PostgresRegions) Create(ctx context.Context, r Region) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO map_regions (id, name, preset, origin, min_lon, min_lat, max_lon, max_lat, max_zoom, state, created_by)
		 VALUES ($1, $2, $3, 'fetch', $4, $5, $6, $7, $8, 'queued', $9)
		 ON CONFLICT (id) DO NOTHING`,
		r.ID, r.Name, r.Preset, r.Bounds.MinLon, r.Bounds.MinLat, r.Bounds.MaxLon, r.Bounds.MaxLat, r.MaxZoom, r.CreatedBy)
	if err != nil {
		return fmt.Errorf("creating map region: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRegionExists
	}
	return nil
}

// Delete removes a region and returns what it was, so its archive can be
// removed from the bucket.
func (s *PostgresRegions) Delete(ctx context.Context, id string) (*Region, error) {
	r, err := scanRegion(s.db.QueryRowContext(ctx,
		`DELETE FROM map_regions WHERE id = $1 RETURNING `+regionColumns, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRegionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("deleting map region: %w", err)
	}
	return r, nil
}

// Requeue asks for a fresh fetch of a region at maxZoom. A region already
// being fetched is refused; an uploaded one is not fetched.
func (s *PostgresRegions) Requeue(ctx context.Context, id string, maxZoom int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE map_regions SET state = 'queued', max_zoom = $2, error = '', progress_bytes = 0,
		   total_bytes = 0, requested_at = NOW(), started_at = NULL, finished_at = NULL, updated_at = NOW()
		 WHERE id = $1 AND origin = 'fetch' AND state <> 'fetching'`, id, maxZoom)
	if err != nil {
		return fmt.Errorf("queueing map region: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	return ErrRegionBusy
}

// ResetStale queues again every region left fetching. It is called only by a
// worker holding the fetch lock, so a region in that state belongs to a
// worker that died.
func (s *PostgresRegions) ResetStale(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE map_regions SET state = 'queued', progress_bytes = 0, updated_at = NOW() WHERE state = 'fetching'`)
	if err != nil {
		return fmt.Errorf("requeueing interrupted map fetches: %w", err)
	}
	return nil
}

// ClaimNext marks the longest-waiting queued region fetching and returns it,
// or nil when none is queued.
func (s *PostgresRegions) ClaimNext(ctx context.Context) (*Region, error) {
	r, err := scanRegion(s.db.QueryRowContext(ctx,
		`UPDATE map_regions SET state = 'fetching', started_at = NOW(), finished_at = NULL,
		   progress_bytes = 0, total_bytes = 0, error = '', updated_at = NOW()
		 WHERE id = (SELECT id FROM map_regions WHERE state = 'queued' AND origin = 'fetch'
		             ORDER BY requested_at, id LIMIT 1 FOR UPDATE SKIP LOCKED)
		 RETURNING `+regionColumns))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing queued is an answer, not a failure
	}
	if err != nil {
		return nil, fmt.Errorf("claiming a map fetch: %w", err)
	}
	return r, nil
}

// Progress records how much of a fetch is written.
func (s *PostgresRegions) Progress(ctx context.Context, id string, done, total int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE map_regions SET progress_bytes = $2, total_bytes = $3, updated_at = NOW()
		 WHERE id = $1 AND state = 'fetching'`, id, done, total)
	if err != nil {
		return fmt.Errorf("recording map fetch progress: %w", err)
	}
	return nil
}

// Finish makes a completed fetch's archive the one served and returns the
// archive it replaced. ok is false when the region was deleted or requeued
// while the fetch ran; the caller then removes the archive it wrote.
func (s *PostgresRegions) Finish(ctx context.Context, id string, a Archive) (*Archive, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf(errFinishing, err)
	}
	defer func() { _ = tx.Rollback() }()
	prev, err := scanRegion(tx.QueryRowContext(ctx,
		`SELECT `+regionColumns+` FROM map_regions WHERE id = $1 AND state = 'fetching' FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf(errFinishing, err)
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE map_regions SET state = 'ready', finished_at = NOW(), progress_bytes = $2, total_bytes = $2,
		   error = '', archive_bucket = $3, archive_key = $4, archive_size = $2, archive_build = $5,
		   archive_min_zoom = $6, archive_max_zoom = $7, archive_min_lon = $8, archive_min_lat = $9,
		   archive_max_lon = $10, archive_max_lat = $11, archive_ready_at = NOW(), updated_at = NOW()
		 WHERE id = $1`,
		id, a.Size, a.Bucket, a.Key, a.Build, a.MinZoom, a.MaxZoom,
		a.Bounds.MinLon, a.Bounds.MinLat, a.Bounds.MaxLon, a.Bounds.MaxLat)
	if err != nil {
		return nil, false, fmt.Errorf(errFinishing, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf(errFinishing, err)
	}
	return prev.Archive, true, nil
}

// Fail records why a fetch stopped. The archive being served is untouched.
func (s *PostgresRegions) Fail(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE map_regions SET state = 'failed', error = $2, finished_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND state = 'fetching'`, id, reason)
	if err != nil {
		return fmt.Errorf("recording map fetch failure: %w", err)
	}
	return nil
}

// PutUpload records an archive the operator uploaded, as its header
// describes it, or as failed with the reason its header could not be read.
// The object key is recorded either way, so the next scan knows the file. A
// region of the same id the platform fetches is left alone.
func (s *PostgresRegions) PutUpload(ctx context.Context, r Region) error {
	a := r.Archive
	if a == nil {
		a = &Archive{}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO map_regions (id, name, origin, min_lon, min_lat, max_lon, max_lat, max_zoom, state, error,
		   total_bytes, finished_at, archive_bucket, archive_key, archive_size, archive_build, archive_min_zoom, archive_max_zoom,
		   archive_min_lon, archive_min_lat, archive_max_lon, archive_max_lat, archive_ready_at)
		 VALUES ($1, $2, 'upload', $3, $4, $5, $6, $7, $8, $9, $12, NOW(), $10, $11, $12, $13, $14, $7, $3, $4, $5, $6,
		   CASE WHEN $8 = 'ready' THEN NOW() END)
		 ON CONFLICT (id) DO UPDATE SET
		   min_lon = EXCLUDED.min_lon, min_lat = EXCLUDED.min_lat, max_lon = EXCLUDED.max_lon,
		   max_lat = EXCLUDED.max_lat, max_zoom = EXCLUDED.max_zoom, state = EXCLUDED.state, error = EXCLUDED.error,
		   total_bytes = EXCLUDED.total_bytes, finished_at = NOW(), archive_bucket = EXCLUDED.archive_bucket, archive_key = EXCLUDED.archive_key,
		   archive_size = EXCLUDED.archive_size, archive_build = EXCLUDED.archive_build,
		   archive_min_zoom = EXCLUDED.archive_min_zoom, archive_max_zoom = EXCLUDED.archive_max_zoom,
		   archive_min_lon = EXCLUDED.archive_min_lon, archive_min_lat = EXCLUDED.archive_min_lat,
		   archive_max_lon = EXCLUDED.archive_max_lon, archive_max_lat = EXCLUDED.archive_max_lat,
		   archive_ready_at = EXCLUDED.archive_ready_at, updated_at = NOW()
		 WHERE map_regions.origin = 'upload'`,
		r.ID, r.Name, r.Bounds.MinLon, r.Bounds.MinLat, r.Bounds.MaxLon, r.Bounds.MaxLat, r.MaxZoom, r.State, r.Error,
		a.Bucket, a.Key, a.Size, a.Build, a.MinZoom)
	if err != nil {
		return fmt.Errorf("recording uploaded map archive: %w", err)
	}
	return nil
}

// DeleteUploadsExcept removes the uploaded regions whose object key is not
// in keep, which is what the bucket holds now, and returns them.
func (s *PostgresRegions) DeleteUploadsExcept(ctx context.Context, keep []string) ([]Region, error) {
	if keep == nil {
		keep = []string{}
	}
	rows, err := s.db.QueryContext(ctx,
		`DELETE FROM map_regions WHERE origin = 'upload' AND NOT (archive_key = ANY($1))
		 RETURNING `+regionColumns, pq.Array(keep))
	if err != nil {
		return nil, fmt.Errorf("removing uploaded map regions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Region, 0)
	for rows.Next() {
		r, err := scanRegion(rows)
		if err != nil {
			return nil, fmt.Errorf("removing uploaded map regions: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("removing uploaded map regions: %w", err)
	}
	return out, nil
}

var (
	_ RegionStore   = (*PostgresRegions)(nil)
	_ SettingsStore = (*PostgresSettings)(nil)
)
