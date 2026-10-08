// Package scripttiles keeps the tile each managed script is shown by in the
// scripts listing's grid view (#1909): its flow diagram (#1906), drawn by the
// tile worker (internal/platform/thumbworker) in the same headless browser
// every other tile is drawn in, light and dark.
//
// A tile is keyed by the script and names the version it was drawn from, so
// saving a version owes a new one whatever the version changed. A version that
// does not parse is recorded as not drawable, with the parse error as the
// reason, and draws when a later version parses. Claiming, holding back and
// giving up follow the rules the worker applies to every other tile.
package scripttiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Variants of a tile.
const (
	VariantLight = "light"
	VariantDark  = "dark"
)

// Work is one script owed a tile.
type Work struct {
	ScriptID string
	Name     string
	Version  int
	Source   string
	// Library is whether the script is a library, as recorded when it was
	// created; a library's tile names it rather than drawing a diagram
	// (#1970).
	Library bool
	// Key is the tile stored now, empty when there is none.
	Key      string
	Attempts int
}

// Orphan is the stored tile of a script that no longer exists.
type Orphan struct {
	ScriptID string
	Key      string
}

// Store is the Postgres record of every script's tile.
type Store struct {
	db *sql.DB
}

// NewPostgres builds the store over db.
func NewPostgres(db *sql.DB) *Store { return &Store{db: db} }

// Key is where a script's tile variant is stored: under the portal's prefix,
// one pair per script, redrawn in place.
func Key(prefix, scriptID, variant string) string {
	name := "tile.png"
	if variant == VariantDark {
		name = "tile-dark.png"
	}
	return strings.TrimPrefix(path.Join(prefix, "scripts", scriptID, name), "/")
}

// DarkKey is the dark tile stored beside the light one at lightKey.
func DarkKey(lightKey string) string {
	return path.Join(path.Dir(lightKey), "tile-dark.png")
}

// claimSQL leases the scripts owed a tile: one never drawn, drawn from an
// older version or by an older renderer, whose current version is not already
// recorded as not drawable, and not leased by another worker. The script rows
// are locked while they are claimed, so two workers never lease one script.
const claimSQL = `
WITH owed AS (
    SELECT s.id
      FROM scripts s
      LEFT JOIN script_tiles t ON t.script_id = s.id
     WHERE ` + owedWhere + `
       AND (t.claimed_until IS NULL OR t.claimed_until < NOW())
     ORDER BY s.updated_at DESC
     LIMIT $3
       FOR UPDATE OF s SKIP LOCKED
)
INSERT INTO script_tiles (script_id, claimed_until, attempts)
SELECT id, NOW() + make_interval(secs => $2), 1 FROM owed
ON CONFLICT (script_id) DO UPDATE
   SET claimed_until = EXCLUDED.claimed_until, attempts = script_tiles.attempts + 1, updated_at = NOW()
RETURNING script_id`

// owedWhere is when script s, joined to its tile row t, is owed a tile,
// whoever holds it; the renderer generation is $1.
const owedWhere = `(t.script_id IS NULL OR t.version < s.version OR t.renderer < $1 OR t.s3_key = '')
       AND COALESCE(t.failed_version, 0) < s.version`

// backlogSQL counts the scripts owed a tile, split by whether a worker holds
// the lease (#1897), with the claim's own predicate.
const backlogSQL = `SELECT
    COUNT(*) FILTER (WHERE t.claimed_until IS NULL OR t.claimed_until < NOW()),
    COUNT(*) FILTER (WHERE t.claimed_until >= NOW())
  FROM scripts s
  LEFT JOIN script_tiles t ON t.script_id = s.id
 WHERE ` + owedWhere

// Backlog counts the scripts owed a tile by renderer generation renderer:
// the ones no worker holds, and the ones held now or held back after an
// attempt that did not finish.
func (s *Store) Backlog(ctx context.Context, renderer int) (pending, waiting int64, err error) {
	if err := s.db.QueryRowContext(ctx, backlogSQL, renderer).Scan(&pending, &waiting); err != nil {
		return 0, 0, fmt.Errorf("counting script tiles owed: %w", err)
	}
	return pending, waiting, nil
}

// readSQL reads what a claimed script's tile is drawn from.
const readSQL = `
SELECT s.id, s.name, s.version, s.source_code, s.library, t.s3_key, t.attempts
  FROM scripts s JOIN script_tiles t ON t.script_id = s.id
 WHERE s.id = ANY($1::uuid[])`

// Claim leases up to limit scripts owed a tile for lease, drawn by renderer
// generation renderer.
func (s *Store) Claim(ctx context.Context, renderer int, lease time.Duration, limit int) ([]Work, error) {
	rows, err := s.db.QueryContext(ctx, claimSQL, renderer, lease.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("claiming script tiles: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil || len(ids) == 0 {
		return []Work{}, err
	}
	rows, err = s.db.QueryContext(ctx, readSQL, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("reading claimed scripts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Work, 0, len(ids))
	for rows.Next() {
		var w Work
		if err := rows.Scan(&w.ScriptID, &w.Name, &w.Version, &w.Source, &w.Library, &w.Key, &w.Attempts); err != nil {
			return nil, fmt.Errorf("reading a claimed script: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err() //nolint:wrapcheck // the iteration error of the read above
}

func scanIDs(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reading a claimed script id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err() //nolint:wrapcheck // the iteration error of the claim above
}

// Record stores that version's tile at key, drawn by renderer.
func (s *Store) Record(ctx context.Context, scriptID string, version int, key string, renderer int) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE script_tiles
   SET version = $2, s3_key = $3, renderer = $4, failure = '', failed_version = 0,
       claimed_until = NULL, attempts = 0, updated_at = NOW()
 WHERE script_id = $1`, scriptID, version, key, renderer)
	if err != nil {
		return fmt.Errorf("recording a script tile: %w", err)
	}
	return nil
}

// RecordFailure stores that version cannot be drawn, and why. It is not
// claimed again until a later version is saved.
func (s *Store) RecordFailure(ctx context.Context, scriptID string, version int, reason string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE script_tiles
   SET failure = $3, failed_version = $2, claimed_until = NULL, attempts = 0, updated_at = NOW()
 WHERE script_id = $1`, scriptID, version, reason)
	if err != nil {
		return fmt.Errorf("recording a script tile failure: %w", err)
	}
	return nil
}

// Hold holds a script back from the next claim for hold after an attempt that
// did not finish.
func (s *Store) Hold(ctx context.Context, scriptID string, hold time.Duration, attempts int) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE script_tiles SET claimed_until = NOW() + make_interval(secs => $2), attempts = $3, updated_at = NOW()
 WHERE script_id = $1`, scriptID, hold.Seconds(), attempts)
	if err != nil {
		return fmt.Errorf("holding a script tile back: %w", err)
	}
	return nil
}

// Orphans lists up to limit tile rows whose script was deleted.
func (s *Store) Orphans(ctx context.Context, limit int) ([]Orphan, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.script_id, t.s3_key FROM script_tiles t
 WHERE NOT EXISTS (SELECT 1 FROM scripts s WHERE s.id = t.script_id)
 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing deleted scripts' tiles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Orphan{}
	for rows.Next() {
		var o Orphan
		if err := rows.Scan(&o.ScriptID, &o.Key); err != nil {
			return nil, fmt.Errorf("reading a deleted script's tile: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err() //nolint:wrapcheck // the iteration error of the query above
}

// Forget removes a deleted script's tile row, once its objects are gone.
func (s *Store) Forget(ctx context.Context, scriptID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM script_tiles WHERE script_id = $1`, scriptID); err != nil {
		return fmt.Errorf("forgetting a deleted script's tile: %w", err)
	}
	return nil
}

// ErrNoTile is a script with no tile to serve: never drawn, or its current
// version is recorded as not drawable.
var ErrNoTile = errors.New("this script has no tile")

// CurrentKey is the stored light tile of a script, unless its current version
// is recorded as not drawable: that script is shown by the placeholder, not by
// an older version's diagram. current is false while the stored tile is an
// older version's, the one a script shows until the worker draws the version
// it is at. The id is compared as text, so an id that is not a script's is no
// tile rather than a malformed query.
func (s *Store) CurrentKey(ctx context.Context, scriptID string) (key string, current bool, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT t.s3_key, t.version = s.version FROM script_tiles t JOIN scripts s ON s.id = t.script_id
 WHERE t.script_id::text = $1 AND t.s3_key <> '' AND t.failed_version < s.version`, scriptID).Scan(&key, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNoTile
	}
	if err != nil {
		return "", false, fmt.Errorf("reading a script's tile: %w", err)
	}
	return key, current, nil
}

// ObjectGetter reads one stored object.
type ObjectGetter interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
}

// Reader serves a script's stored tile.
type Reader struct {
	store  *Store
	blobs  ObjectGetter
	bucket string
}

// NewReader builds a reader over the store and the bucket tiles are stored in.
func NewReader(store *Store, blobs ObjectGetter, bucket string) *Reader {
	return &Reader{store: store, blobs: blobs, bucket: bucket}
}

// Tile is one variant of a script's tile, or ErrNoTile, and whether it was
// drawn from the version the script is at (see CurrentKey).
func (r *Reader) Tile(ctx context.Context, scriptID, variant string) (data []byte, current bool, err error) {
	key, current, err := r.store.CurrentKey(ctx, scriptID)
	if err != nil {
		return nil, false, err
	}
	if variant == VariantDark {
		key = DarkKey(key)
	}
	data, _, err = r.blobs.GetObject(ctx, r.bucket, key)
	if err != nil {
		return nil, false, fmt.Errorf("reading a script's tile: %w", err)
	}
	return data, current, nil
}
