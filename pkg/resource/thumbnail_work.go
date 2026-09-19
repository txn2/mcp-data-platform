package resource

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
)

// ThumbnailWork is what the renderer asks of the resource store (#1787): the
// files it owes a tile, and where it records what it drew or why it could not.
// The PostgreSQL store implements it; a deployment without a database renders
// nothing.
type ThumbnailWork interface {
	ClaimThumbnailWork(ctx context.Context, renderer int, lease time.Duration, limit int) ([]Resource, error)
	RecordThumbnailFailure(ctx context.Context, id, reason string, at time.Time) error
	SetThumbnail(ctx context.Context, id string, t ThumbnailCapture) error
}

var _ ThumbnailWork = (*postgresStore)(nil)

// buildThumbnailClaim renders the statement ClaimThumbnailWork runs: it leases
// up to limit resources the renderer owes a tile and returns them.
//
// Owed is: a type the renderer draws, small enough to take, not leased by
// another replica, not failed on the file as it stands, and carrying a tile
// that is missing, older than the file, or drawn by a renderer generation
// older than renderer. The dark variant is asked only of the types that carry
// one: a file that brings its own colors stores a single image and serves it
// in both modes, and a type is judged by the first family it matches, so
// image/svg+xml is an SVG and not the XML its name also contains. Scope is not
// part of it -- the renderer is the platform, drawing every library's files,
// not a person reading one.
func buildThumbnailClaim(renderer int, lease time.Duration, limit int) (query string, args []any) {
	query = `
		UPDATE resources SET thumbnail_claimed_until = now() + make_interval(secs => $1)
		WHERE id IN (
			SELECT id FROM resources
			WHERE mime_type ILIKE ANY($2)
			  AND size_bytes <= $3
			  AND (thumbnail_claimed_until IS NULL OR thumbnail_claimed_until < now())
			  AND (thumbnail_failed_at IS NULL OR thumbnail_failed_at < updated_at)
			  AND (
			        thumbnail_s3_key = ''
			     OR thumbnail_captured_at IS NULL
			     OR thumbnail_captured_at < updated_at
			     OR thumbnail_renderer < $4
			     OR (
			          mime_type ILIKE ANY($5)
			          AND NOT (mime_type ILIKE ANY($7))
			          AND (
			                thumbnail_dark_s3_key = ''
			             OR thumbnail_dark_captured_at IS NULL
			             OR thumbnail_dark_captured_at < updated_at
			          )
			        )
			  )
			ORDER BY updated_at DESC
			LIMIT $6
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + selectColumns
	args = []any{
		lease.Seconds(),
		pq.Array(thumbtypes.ILikePatterns(thumbtypes.Capturable)),
		MaxThumbnailSourceBytes,
		renderer,
		pq.Array(thumbtypes.ILikePatterns(thumbtypes.Themeable)),
		limit,
		pq.Array(thumbtypes.ILikePatterns(thumbtypes.ThemeableShadows())),
	}
	return query, args
}

// ClaimThumbnailWork leases up to limit resources the renderer owes a tile
// for lease and returns them. Recording a tile or a failure releases the
// lease; it lapses on its own if the replica that took it dies mid-render.
func (s *postgresStore) ClaimThumbnailWork(ctx context.Context, renderer int, lease time.Duration, limit int) ([]Resource, error) {
	if limit <= 0 {
		return nil, nil
	}
	query, args := buildThumbnailClaim(renderer, lease, limit)
	rows, err := s.db.QueryContext(ctx, query, args...) // #nosec G701 -- a constant statement; every value is bound
	if err != nil {
		return nil, fmt.Errorf("claiming thumbnail work: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only cursor

	var out []Resource
	for rows.Next() {
		r, err := s.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading claimed resources: %w", err)
	}
	return out, nil
}

// recordThumbnailFailureQuery records a failure and ends the lease.
const recordThumbnailFailureQuery = `UPDATE resources
	SET thumbnail_failure = $1, thumbnail_failed_at = $2, thumbnail_claimed_until = NULL
	WHERE id = $3`

// RecordThumbnailFailure records that the renderer could not draw the file as
// it stood at at, and why, and ends the lease. It holds until the file changes
// or the tile is asked for again.
func (s *postgresStore) RecordThumbnailFailure(ctx context.Context, id, reason string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, recordThumbnailFailureQuery, reason, at, id)
	if err != nil {
		return fmt.Errorf("recording thumbnail failure: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("resource not found: %s", id)
	}
	return nil
}
