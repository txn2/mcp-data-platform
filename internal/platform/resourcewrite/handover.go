package resourcewrite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// errHandoverMissing is returned when a handover names a resource with no row.
var errHandoverMissing = errors.New("resource not found")

// HandOverAddresses makes every address resource from answers to resolve to
// resource to instead: its own URI and each address already aliased to it. It
// is the step before deleting one of two copies of a file, so a citation of the
// copy keeps resolving to the file that stays, as a move keeps a citation of
// the address it vacates (#2842).
//
// One transaction. The aliases are re-pointed first, so the delete that
// follows, which takes a resource's aliases with it (ON DELETE CASCADE), finds
// none left. The copy's own URI stays live until the copy is deleted, and a
// live address always wins over an alias of the same URI (GetByURI), so
// nothing resolves differently until then.
func HandOverAddresses(ctx context.Context, db *sql.DB, from, to string) (err error) {
	if from == to {
		return fmt.Errorf("handing %s's addresses to itself", from)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning the address handover: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var present int
	err = tx.QueryRowContext(ctx,
		`SELECT count(*) FROM resources WHERE id IN ($1, $2)`, from, to).Scan(&present)
	if err != nil {
		return fmt.Errorf("reading the resources of the address handover: %w", err)
	}
	if present != 2 { //nolint:mnd // the two resources the handover names
		return fmt.Errorf("handing %s's addresses to %s: %w", from, to, errHandoverMissing)
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE resource_uri_aliases SET resource_id = $2 WHERE resource_id = $1`, from, to); err != nil {
		return fmt.Errorf("re-pointing the aliases of %s: %w", from, err)
	}
	const own = `
		INSERT INTO resource_uri_aliases (uri, resource_id)
		SELECT uri, $2 FROM resources WHERE id = $1
		ON CONFLICT (uri) DO UPDATE SET resource_id = EXCLUDED.resource_id, created_at = NOW()`
	if _, err = tx.ExecContext(ctx, own, from, to); err != nil {
		return fmt.Errorf("aliasing the address of %s: %w", from, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing the address handover: %w", err)
	}
	return nil
}
