// Package sitetile keeps a site environment's tile (#2658): a picture of the
// release the environment serves, drawn by the tile worker in the headless
// renderer (internal/site/sitetile/tilepage builds the page) and redrawn when
// the environment's release changes. Who reads a tile is decided by the route
// that serves it, by the rule the environment's addresses apply.
package sitetile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Renderer is the generation of site tiles; raising it redraws every one,
// the ones that failed under the older generation included (#2681). Raise it
// whenever the tile page, what it serves or what it waits for changes.
//
//  1. #2658: the release's files alone.
//  2. #2681: the release as its address serves it (redirects, rewrites,
//     media, 404 page), public files fetched, the same site's hostnames
//     served in process, and a quiet network waited for.
//  3. #2701: the tiles the storage capacity pass removed as orphans,
//     drawn again on every deployment that lost them.
const Renderer = 3

// Work is one environment owed a tile.
type Work struct {
	SiteID      string
	Environment string
	Release     int
	Attempts    int
}

// Key is where an environment's tile is kept under prefix.
func Key(prefix, siteID, environment string, release int) string {
	return fmt.Sprintf("%s/sites/%s/%s/%d.png", strings.TrimRight(prefix, "/"), siteID, url.PathEscape(environment), release)
}

// Tile is an environment's stored tile.
type Tile struct {
	Release int
	Key     string
	Failure string
}

// Store keeps the tiles in site_tiles.
type Store struct {
	DB *sql.DB
}

// NewStore is the tiles' store over db, nil without one.
func NewStore(db *sql.DB) *Store {
	if db == nil {
		return nil
	}
	return &Store{DB: db}
}

// Claim leases up to limit environments owed a tile: one serving a release,
// online, whose tile is of another release or an older renderer, and not
// leased by another worker. A release that failed maxAttempts times under
// this renderer is left until it changes or the renderer does (#2681).
func (s *Store) Claim(ctx context.Context, renderer int, lease time.Duration, limit, maxAttempts int) ([]Work, error) {
	rows, err := s.DB.QueryContext(ctx, `
		WITH due AS (
		  SELECT e.site_id, e.name, e.release_number
		    FROM site_environments e
		    LEFT JOIN site_tiles t ON t.site_id = e.site_id AND t.environment = e.name
		   WHERE e.release_number IS NOT NULL AND e.status = 'online'
		     AND (t.site_id IS NULL OR t.drawn_release IS DISTINCT FROM e.release_number OR t.renderer < $1)
		     AND (t.lease_until IS NULL OR t.lease_until < now())
		     AND (t.attempt_release IS DISTINCT FROM e.release_number OR t.attempt_renderer < $1 OR t.attempts < $4)
		   ORDER BY e.updated_at
		   LIMIT $3)
		INSERT INTO site_tiles (site_id, environment, attempt_release, attempt_renderer, attempts, lease_until)
		SELECT site_id, name, release_number, $1, 1, now() + make_interval(secs => $2) FROM due
		ON CONFLICT (site_id, environment) DO UPDATE SET
		   attempts = CASE WHEN site_tiles.attempt_release = EXCLUDED.attempt_release AND site_tiles.attempt_renderer = EXCLUDED.attempt_renderer
		                   THEN site_tiles.attempts + 1 ELSE 1 END,
		   attempt_release = EXCLUDED.attempt_release,
		   attempt_renderer = EXCLUDED.attempt_renderer,
		   lease_until = EXCLUDED.lease_until
		 WHERE site_tiles.lease_until IS NULL OR site_tiles.lease_until < now()
		RETURNING site_id, environment, attempt_release, attempts`,
		renderer, lease.Seconds(), limit, maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("claiming site tiles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Work{}
	for rows.Next() {
		var w Work
		if err := rows.Scan(&w.SiteID, &w.Environment, &w.Release, &w.Attempts); err != nil {
			return nil, fmt.Errorf("claiming site tiles: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claiming site tiles: %w", err)
	}
	return out, nil
}

// Record keeps the tile of w's release, drawn by renderer.
func (s *Store) Record(ctx context.Context, w Work, key string, renderer int) error {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE site_tiles SET drawn_release = $3, tile_key = $4, renderer = $5, failure = '', lease_until = NULL, drawn_at = now()
		  WHERE site_id = $1 AND environment = $2`, w.SiteID, w.Environment, w.Release, key, renderer); err != nil {
		return fmt.Errorf("recording a site tile: %w", err)
	}
	return nil
}

// RecordFailure keeps why w's release could not be drawn; it is not tried
// again until the environment's release changes.
func (s *Store) RecordFailure(ctx context.Context, w Work, reason string, renderer int) error {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE site_tiles SET drawn_release = $3, tile_key = '', renderer = $5, failure = $4, lease_until = NULL, drawn_at = now()
		  WHERE site_id = $1 AND environment = $2`, w.SiteID, w.Environment, w.Release, reason, renderer); err != nil {
		return fmt.Errorf("recording a site tile's failure: %w", err)
	}
	return nil
}

// Hold leases an environment's tile for hold before it is tried again.
func (s *Store) Hold(ctx context.Context, siteID, environment string, hold time.Duration) error {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE site_tiles SET lease_until = now() + make_interval(secs => $3) WHERE site_id = $1 AND environment = $2`,
		siteID, environment, hold.Seconds()); err != nil {
		return fmt.Errorf("holding a site tile: %w", err)
	}
	return nil
}

// Forget clears an environment's tile whose object at key is gone, so the
// worker draws it again for the release it serves (#2701), with its attempts
// counted afresh. A row that has since recorded another key is left alone.
func (s *Store) Forget(ctx context.Context, siteID, environment, key string) error {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE site_tiles SET drawn_release = NULL, tile_key = '', attempt_release = NULL, attempts = 0 WHERE site_id = $1 AND environment = $2 AND tile_key = $3`,
		siteID, environment, key); err != nil {
		return fmt.Errorf("forgetting a site tile: %w", err)
	}
	return nil
}

// ErrNoTile is an environment with no stored tile.
var ErrNoTile = errors.New("no tile is stored for this environment")

// Get is an environment's stored tile, ErrNoTile for none.
func (s *Store) Get(ctx context.Context, siteID, environment string) (*Tile, error) {
	var (
		t       Tile
		release sql.NullInt64
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT drawn_release, tile_key, failure FROM site_tiles WHERE site_id = $1 AND environment = $2`,
		siteID, environment).Scan(&release, &t.Key, &t.Failure)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoTile
	}
	if err != nil {
		return nil, fmt.Errorf("reading a site tile: %w", err)
	}
	t.Release = int(release.Int64)
	return &t, nil
}
