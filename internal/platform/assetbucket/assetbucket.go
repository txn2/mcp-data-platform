// Package assetbucket gives the asset rows written without a bucket the
// portal bucket (#1931).
//
// An early release stored asset and version rows with an empty s3_bucket.
// Every path that reads, rewrites, copies or deletes an asset's objects takes
// the bucket from the row, so such an asset could not be opened, a new version
// of it inherited the empty bucket, and the retention purge sent its deletes
// to bucket "" and kept the row on every sweep. Setting the bucket on the rows
// mends every one of those paths at once, where resolving it at each of them
// would leave the next path written to fail the same way.
//
// It is a pass rather than a migration because the bucket is the deployment's
// portal.s3_bucket, which a migration cannot read. It is idempotent: a row
// that names a bucket is never written, so a second replica, or a second boot,
// does no work.
package assetbucket

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

const (
	fillAssetsSQL   = `UPDATE portal_assets SET s3_bucket = $1 WHERE s3_bucket = ''`
	fillVersionsSQL = `UPDATE portal_asset_versions SET s3_bucket = $1 WHERE s3_bucket = ''`
)

// Run sets bucket on every asset and version row that names none, and reports
// how many of each it wrote. Nothing is written where bucket is empty, which
// is a deployment with no portal storage, where no row names an object.
func Run(ctx context.Context, db *sql.DB, bucket string) (assets, versions int64, err error) {
	if db == nil || bucket == "" {
		return 0, 0, nil
	}
	if assets, err = fill(ctx, db, fillAssetsSQL, bucket); err != nil {
		return 0, 0, fmt.Errorf("setting the portal bucket on assets that name none: %w", err)
	}
	if versions, err = fill(ctx, db, fillVersionsSQL, bucket); err != nil {
		return assets, 0, fmt.Errorf("setting the portal bucket on versions that name none: %w", err)
	}
	return assets, versions, nil
}

func fill(ctx context.Context, db *sql.DB, query, bucket string) (int64, error) {
	res, err := db.ExecContext(ctx, query, bucket)
	if err != nil {
		return 0, fmt.Errorf("updating rows: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RunLogged is Run for the platform's startup, which reports what it did and
// goes on: an asset left without a bucket is no reason to refuse to serve the
// rest.
func RunLogged(ctx context.Context, db *sql.DB, bucket string) {
	assets, versions, err := Run(ctx, db, bucket)
	if err != nil {
		slog.WarnContext(ctx, "asset bucket: rows naming no bucket were not given one",
			"error", logsan.SanitizeForLog(err.Error()))
		return
	}
	if assets+versions > 0 {
		slog.InfoContext(ctx, "asset bucket: rows naming no bucket now name the portal bucket",
			"bucket", logsan.SanitizeForLog(bucket), "assets", assets, "versions", versions)
	}
}
