package resource

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/grantsource"
)

// A site serves a managed-resource folder as its media (#2591): a file in a
// mounted folder is refused deletion unless forced, the way a referenced file
// is. The PostgreSQL store answers how many sites serve a file, once, for
// both the REST delete here and the manage_resource delete guard
// (internal/platform/resourceholds, through the store).

// CountMounting counts, up to limit, the sites whose mounted folder holds a
// file: the folder is the file's own or one of its ancestors, in the same
// library (site_media).
func (s *postgresStore) CountMounting(ctx context.Context, resourceID string, limit int) (int, error) { //nolint:revive // read through an interface
	var n int
	err := s.db.QueryRowContext(ctx, countMountingQuery(), resourceID, limit).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting the sites serving a file: %w", err)
	}
	return n, nil
}

// countMountingQuery counts the sites whose mounted folder holds file $1.
func countMountingQuery() string {
	// #nosec G202 -- the predicate is the fixed segment rule; values are bound.
	return `SELECT COUNT(*) FROM (
		SELECT DISTINCT sm.site_id FROM resources r
		JOIN site_media sm ON sm.scope = r.scope AND sm.scope_id = COALESCE(r.scope_id, '')
		 AND ` + grantsource.UnderSQL("r.path", "sm.folder") + `
		WHERE r.id = $1 LIMIT $2) mounting`
}
