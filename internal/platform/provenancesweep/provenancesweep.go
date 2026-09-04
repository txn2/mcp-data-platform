// Package provenancesweep trims the provenance captures that outlived the
// versions they describe.
//
// A capture is appended to an asset on every content write, and until #1623
// nothing removed one. An asset whose history is capped at twelve versions
// carried captures for three hundred and thirty-three of them, describing
// versions the platform had already deleted. The prune now drops a version's
// capture with the version, which holds for every write from here on; this pass
// is what settles the rows written before it.
//
// It is a pass rather than a migration because the rule it applies depends on
// the deployment's portal.max_versions, which a migration cannot read. It is
// idempotent -- an asset with nothing left to trim is not written -- so a
// second replica, or a second boot, does no work.
package provenancesweep

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalversions"
)

// candidatesSQL names the assets holding captures for versions their retention
// cap has already removed, with the watermark to trim at and how many captures
// are below it.
//
// COALESCE(max_versions, $1) is the effective cap: the asset's own override
// where it has one, the deployment default otherwise. A cap of zero is
// unlimited and prunes nothing, so it is excluded rather than trimmed at
// watermark current_version, and so is anything a nonsensical negative value
// could produce.
const candidatesSQL = `
	SELECT id,
	       current_version - COALESCE(max_versions, $1) AS watermark,
	       (
	           SELECT COUNT(*)
	           FROM jsonb_array_elements(provenance -> 'captures') AS c
	           WHERE COALESCE(CASE WHEN jsonb_typeof(c -> 'version') = 'number'
	                   THEN (c ->> 'version')::int END, 0)
	                 BETWEEN 2 AND current_version - COALESCE(max_versions, $1)
	       ) AS trimmable
	FROM portal_assets
	WHERE jsonb_typeof(provenance -> 'captures') = 'array'
	  AND COALESCE(max_versions, $1) > 0
	  AND current_version - COALESCE(max_versions, $1) >= 2
`

// candidate is one asset the pass has work to do on.
type candidate struct {
	id        string
	watermark int
	trimmable int
}

// Run trims every asset whose captures describe versions its retention cap has
// removed. platformDefault is the deployment's configured portal.max_versions,
// nil where it set none; the cap the pass applies is what the prune applies,
// resolved the same way, and an unlimited cap leaves an asset with no override
// of its own alone.
//
// A failure to trim one asset is logged and the pass continues: the point is to
// settle a library, and one row the database refused is not a reason to leave
// the rest carrying history for versions that no longer exist.
func Run(ctx context.Context, db *sql.DB, platformDefault *int) {
	if db == nil {
		return
	}
	defaultCap := portaldomain.EffectiveMaxVersions(nil, platformDefault)
	candidates, err := findCandidates(ctx, db, defaultCap)
	if err != nil {
		slog.Warn("provenance sweep: could not list the assets to trim",
			"error", logsan.SanitizeForLog(err.Error()))
		return
	}
	if len(candidates) == 0 {
		return
	}

	var trimmed, failed int
	for _, c := range candidates {
		if err := portalversions.TrimProvenanceCaptures(ctx, db, c.id, c.watermark); err != nil {
			slog.Warn("provenance sweep: asset not trimmed",
				"asset_id", logsan.SanitizeForLog(c.id),
				"error", logsan.SanitizeForLog(err.Error()))
			failed++
			continue
		}
		trimmed++
		slog.Info("provenance sweep: captures for pruned versions removed",
			"asset_id", logsan.SanitizeForLog(c.id),
			"captures_removed", c.trimmable, "below_version", c.watermark+1)
	}
	slog.Info("provenance sweep complete", "assets_trimmed", trimmed, "assets_failed", failed)
}

func findCandidates(ctx context.Context, db *sql.DB, defaultCap int) ([]candidate, error) {
	rows, err := db.QueryContext(ctx, candidatesSQL, defaultCap)
	if err != nil {
		return nil, fmt.Errorf("listing assets with captures past their retention: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.watermark, &c.trimmable); err != nil {
			return nil, fmt.Errorf("scanning asset row: %w", err)
		}
		if c.trimmable > 0 {
			out = append(out, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating asset rows: %w", err)
	}
	return out, nil
}
