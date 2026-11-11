package migrate

import (
	"context"
	"database/sql"
	"fmt"
)

// The migrations the check before 000239 reads the schema at (#2608).
const (
	// sitesVersion created the site tables.
	sitesVersion = 235
	// defaultVersion gave every site an environment named default.
	defaultVersion = 236
	// devRenameVersion gives the name dev to the working copy (default
	// until then) and renames an environment already named dev.
	devRenameVersion = 239
)

// DevRename is a site whose environment named dev has a domain, which
// migration 000239 is about to rename (#2608).
type DevRename struct {
	Site string
	// Domains is the environment's hostnames, comma-separated.
	Domains string
	// NewName is the name 000239 gives it.
	NewName string
}

// pendingDevRenamesSQL is each site 000239 renames dev on, as its first pass
// chooses them, with the domains that environment serves. $1 is true below
// 000236, which gives every site the default environment the pass requires.
const pendingDevRenamesSQL = `
SELECT s.name,
       string_agg(d.hostname, ', ' ORDER BY d.hostname),
       EXISTS (SELECT 1 FROM site_environments x WHERE x.site_id = s.id AND x.name = 'development')
  FROM sites s
  JOIN site_domains d ON d.site_id = s.id AND d.environment = 'dev'
 WHERE EXISTS (SELECT 1 FROM site_environments e WHERE e.site_id = s.id AND e.name = 'dev')
   AND ($1 OR EXISTS (SELECT 1 FROM site_environments e WHERE e.site_id = s.id AND e.name = 'default'))
 GROUP BY s.id, s.name
 ORDER BY s.name`

// PendingDevRenames is each site whose dev environment has a domain and that
// 000239 will rename, read before the migrations run so the operator is told
// the one time it can be. None when the schema's version cannot be read (a
// new database), is dirty, or is one 000239 does not apply to: before the
// site tables exist, or once it has run.
func PendingDevRenames(ctx context.Context, db *sql.DB) ([]DevRename, error) {
	version, dirty, err := Version(db)
	if err != nil || dirty {
		return nil, nil //nolint:nilerr // an unreadable version is a database with nothing to rename
	}
	return pendingDevRenamesAt(ctx, db, version)
}

func pendingDevRenamesAt(ctx context.Context, db *sql.DB, version uint) ([]DevRename, error) {
	if version < sitesVersion || version >= devRenameVersion {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, pendingDevRenamesSQL, version < defaultVersion)
	if err != nil {
		return nil, fmt.Errorf("reading the environments 000239 renames: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DevRename
	for rows.Next() {
		var r DevRename
		var developmentTaken bool
		if err := rows.Scan(&r.Site, &r.Domains, &developmentTaken); err != nil {
			return nil, fmt.Errorf("reading the environments 000239 renames: %w", err)
		}
		r.NewName = "development"
		if developmentTaken {
			r.NewName = "dev-previous"
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the environments 000239 renames: %w", err)
	}
	return out, nil
}
