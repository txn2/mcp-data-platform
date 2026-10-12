package resource

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/thumbdue"
	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
)

// ThumbnailWork is what the renderer asks of the resource store (#1787): the
// files it owes a tile, and where it records what it drew or why it could not.
// The PostgreSQL store implements it; a deployment without a database renders
// nothing.
type ThumbnailWork interface {
	SweepThumbnailWork(ctx context.Context, renderer int) (int64, error)
	ClearThumbnailWork(ctx context.Context, renderer int, limit int) (int64, error)
	ClaimThumbnailWork(ctx context.Context, renderer int, lease time.Duration, limit int) ([]Resource, error)
	HoldThumbnailWork(ctx context.Context, id string, hold time.Duration, attempts int) error
	RecordThumbnailFailure(ctx context.Context, id, reason string, at time.Time) error
	SetThumbnail(ctx context.Context, id string, t ThumbnailCapture) error
}

var _ ThumbnailWork = (*postgresStore)(nil)

// buildThumbnailClaim renders the statement ClaimThumbnailWork runs: it leases
// up to limit due resources the renderer owes a tile and returns them.
//
// Only due rows are read (#2084): thumbnail_due_at is set by a trigger on
// every write the renderer did not make, and the partial index on it holds
// nothing else, so a library with nothing owed costs an empty index probe.
// The lease is written to thumbnail_due_at as well, so a row whose replica
// died mid-render comes due again when the lease passes.
//
// Owed is: a type the renderer draws, within its family's source bound, not
// leased by another replica, not failed on the file as it stands, and carrying
// a tile that is missing, older than the file, or drawn by a renderer
// generation older than renderer. The dark variant is asked only of the types
// that carry one: an SVG, a PDF or a raster image stores a single image and
// serves it in both modes, and a type is judged by the first family it
// matches, so image/svg+xml is an SVG and not the XML its name also contains.
// Scope is not part of it -- the renderer is the platform, drawing every
// library's files, not a person reading one.
func buildThumbnailClaim(renderer int, lease time.Duration, limit int) (query string, args []any) {
	query = `
		UPDATE resources SET thumbnail_claimed_until = now() + make_interval(secs => $1),
		       thumbnail_due_at = now() + make_interval(secs => $1),
		       thumbnail_attempts = thumbnail_attempts + 1
		WHERE id IN (
			SELECT id FROM resources
			WHERE thumbnail_due_at <= now()
			  AND (thumbnail_claimed_until IS NULL OR thumbnail_claimed_until < now())
			  AND ` + thumbnailOwedWhere(owedParams{capturable: "$2", large: "$3", renderer: "$4", themeable: "$5", shadows: "$7"}) + `
			ORDER BY thumbnail_due_at DESC
			LIMIT $6
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + selectColumns + `, thumbnail_attempts`
	args = []any{
		lease.Seconds(),
		pq.Array(thumbtypes.Patterns(thumbtypes.Capturable)),
		pq.Array(thumbtypes.Patterns(thumbtypes.LargeSourceFamilies)),
		renderer,
		pq.Array(thumbtypes.Patterns(thumbtypes.Themeable)),
		limit,
		pq.Array(thumbtypes.Patterns(thumbtypes.ThemeableShadows())),
	}
	return query, args
}

// buildThumbnailClear renders the statement ClearThumbnailWork runs: up to
// limit due resources the rule says are owed nothing leave the due set. A row
// another statement holds is skipped, and read on the next pass.
func buildThumbnailClear(renderer, limit int) (query string, args []any) {
	query = `
		UPDATE resources SET thumbnail_due_at = NULL
		WHERE id IN (
			SELECT id FROM resources
			WHERE thumbnail_due_at <= now()
			  AND NOT COALESCE((` + thumbnailOwedWhere(owedParams{capturable: "$1", large: "$2", renderer: "$3", themeable: "$4", shadows: "$5"}) + `), FALSE)
			ORDER BY thumbnail_due_at
			LIMIT $6
			FOR UPDATE SKIP LOCKED
		)`
	args = append(owedArgs(renderer), limit)
	return query, args
}

// owedArgs are the values the owed predicate is bound to, numbered $1 to $5.
func owedArgs(renderer int) []any {
	return []any{
		pq.Array(thumbtypes.Patterns(thumbtypes.Capturable)),
		pq.Array(thumbtypes.Patterns(thumbtypes.LargeSourceFamilies)),
		renderer,
		pq.Array(thumbtypes.Patterns(thumbtypes.Themeable)),
		pq.Array(thumbtypes.Patterns(thumbtypes.ThemeableShadows())),
	}
}

// thumbnailRule is the fingerprint of the owed rule as it stands, which a
// sweep is recorded under.
func thumbnailRule(renderer int) string {
	return thumbdue.Rule(thumbnailOwedWhere(owedParams{capturable: "$1", large: "$2", renderer: "$3", themeable: "$4", shadows: "$5"}),
		owedArgs(renderer)...)
}

// owedParams names the placeholder each value of the owed predicate is bound
// to, so the claim and the backlog count number them each their own way.
type owedParams struct {
	capturable, large, renderer, themeable, shadows string
}

// thumbnailOwedWhere is when a resource is owed a tile, whoever holds it: the
// claim's predicate, without the lease.
func thumbnailOwedWhere(p owedParams) string {
	return `mime_type ILIKE ANY(` + p.capturable + `)
			  AND ` + thumbtypes.SourceLimitExpr("size_bytes", "mime_type", p.large) + `
			  AND (thumbnail_failed_at IS NULL OR thumbnail_failed_at < updated_at)
			  AND (
			        thumbnail_s3_key = ''
			     OR thumbnail_captured_at IS NULL
			     OR thumbnail_captured_at < updated_at
			     OR thumbnail_renderer < ` + p.renderer + `
			     OR (
			          mime_type ILIKE ANY(` + p.themeable + `)
			          AND NOT (mime_type ILIKE ANY(` + p.shadows + `))
			          AND (
			                thumbnail_dark_s3_key = ''
			             OR thumbnail_dark_captured_at IS NULL
			             OR thumbnail_dark_captured_at < updated_at
			          )
			        )
			  )`
}

// buildThumbnailBacklog renders the count of resources owed a tile, split by
// whether a replica holds the lease (#1897), with the claim's own predicate.
// Only due rows are counted: a row owed a tile is due until it is drawn.
func buildThumbnailBacklog(renderer int) (query string, args []any) {
	query = `SELECT
		COUNT(*) FILTER (WHERE thumbnail_claimed_until IS NULL OR thumbnail_claimed_until < now()),
		COUNT(*) FILTER (WHERE thumbnail_claimed_until >= now())
		FROM resources
		WHERE thumbnail_due_at IS NOT NULL
		  AND ` + thumbnailOwedWhere(owedParams{capturable: "$1", large: "$2", renderer: "$3", themeable: "$4", shadows: "$5"})
	return query, owedArgs(renderer)
}

// ThumbnailBacklog counts the resources the renderer owes a tile: the ones no
// replica holds, and the ones held now or held back after an attempt that did
// not finish.
func (s *postgresStore) ThumbnailBacklog(ctx context.Context, renderer int) (pending, waiting int64, err error) {
	query, args := buildThumbnailBacklog(renderer)
	// #nosec G701 -- a constant statement; every value is bound
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&pending, &waiting); err != nil {
		return 0, 0, fmt.Errorf("counting thumbnail work: %w", err)
	}
	return pending, waiting, nil
}

// SweepThumbnailWork marks every resource due once per owed rule (#2084):
// at the first start after the rule changed, so a file the old rule passed
// over is read under the new one. It returns how many it marked.
func (s *postgresStore) SweepThumbnailWork(ctx context.Context, renderer int) (int64, error) {
	return thumbdue.Sweep(ctx, s.db, thumbdue.Resources, thumbnailRule(renderer)) //nolint:wrapcheck // thumbdue names itself
}

// ClearThumbnailWork takes up to limit due resources the renderer owes
// nothing out of the due set and returns how many (#2084).
func (s *postgresStore) ClearThumbnailWork(ctx context.Context, renderer, limit int) (int64, error) {
	query, args := buildThumbnailClear(renderer, limit)
	res, err := s.db.ExecContext(ctx, query, args...) // #nosec G701 -- a constant statement; every value is bound
	if err != nil {
		return 0, fmt.Errorf("clearing thumbnail work: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ClaimThumbnailWork leases up to limit resources the renderer owes a tile
// for lease and returns them, each charged one attempt (#1868). Recording a
// failure, or ending the claim with HoldThumbnailWork, releases the lease; it
// lapses on its own if the replica that took it dies mid-render, with the
// attempt still charged.
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
		var r Resource
		var sc resourceScan
		if err := rows.Scan(append(sc.dest(&r), &r.ThumbnailAttempts)...); err != nil {
			return nil, fmt.Errorf("scanning claimed resource: %w", err)
		}
		sc.finish(&r)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading claimed resources: %w", err)
	}
	return out, nil
}

// holdThumbnailWorkQuery ends a claim: the file stays out of the claim until
// the hold passes, when it comes due again, with the attempts counted against
// it.
const holdThumbnailWorkQuery = `UPDATE resources
	SET thumbnail_claimed_until = now() + make_interval(secs => $1),
	    thumbnail_due_at = now() + make_interval(secs => $1),
	    thumbnail_attempts = $2
	WHERE id = $3`

// HoldThumbnailWork ends the renderer's claim on the file (#1868): it is not
// claimed again until hold has passed, and attempts is the count it carries
// into the next claim. A drawn file is released with no hold and no attempts;
// an attempt that could not finish is held back and keeps its count; a file
// claimed and never tried gets its attempt back.
func (s *postgresStore) HoldThumbnailWork(ctx context.Context, id string, hold time.Duration, attempts int) error {
	if _, err := s.db.ExecContext(ctx, holdThumbnailWorkQuery, hold.Seconds(), attempts, id); err != nil {
		return fmt.Errorf("holding thumbnail work: %w", err)
	}
	return nil
}

// recordThumbnailFailureQuery records a failure, ends the lease and clears
// the attempts that led to it.
const recordThumbnailFailureQuery = `UPDATE resources
	SET thumbnail_failure = $1, thumbnail_failed_at = $2, thumbnail_claimed_until = NULL,
	    thumbnail_attempts = 0
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
