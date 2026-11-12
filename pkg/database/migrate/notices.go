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

// publicDomainVersion makes public a setting of each domain: an environment
// public until then with no domain stops answering anyone who has not signed
// in (#2619). Its public column exists from devRenameVersion on.
const publicDomainVersion = 245

// PublicLoss is an environment public before 000245 with no domain, which
// 000245 makes answer only to someone signed in (#2619).
type PublicLoss struct {
	Site        string
	Environment string
}

// pendingPublicLossesSQL is each public environment with no domain. It reads
// public through the row's JSON rather than by name, so the one statement
// prepares against the schema before 000245 and after it, where the column is
// gone and reads as no public environment (the statement gate prepares every
// statement against the latest schema, #1512).
const pendingPublicLossesSQL = `
SELECT s.name, e.name
  FROM site_environments e JOIN sites s ON s.id = e.site_id
 WHERE COALESCE((to_jsonb(e) ->> 'public')::boolean, FALSE)
   AND NOT EXISTS (SELECT 1 FROM site_domains d WHERE d.site_id = e.site_id AND d.environment = e.name)
 ORDER BY s.name, e.name`

// PendingPublicLosses is each environment 000245 will stop answering
// anonymously, read before the migrations run so the operator is told the one
// time it can be. None when the schema's version cannot be read, is dirty, or
// is one 000245 does not apply to.
func PendingPublicLosses(ctx context.Context, db *sql.DB) ([]PublicLoss, error) {
	version, dirty, err := Version(db)
	if err != nil || dirty {
		return nil, nil //nolint:nilerr // an unreadable version is a database with nothing to change
	}
	return pendingPublicLossesAt(ctx, db, version)
}

func pendingPublicLossesAt(ctx context.Context, db *sql.DB, version uint) ([]PublicLoss, error) {
	if version < devRenameVersion || version >= publicDomainVersion {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, pendingPublicLossesSQL)
	if err != nil {
		return nil, fmt.Errorf("reading the environments 000245 makes private: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PublicLoss
	for rows.Next() {
		var l PublicLoss
		if err := rows.Scan(&l.Site, &l.Environment); err != nil {
			return nil, fmt.Errorf("reading the environments 000245 makes private: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the environments 000245 makes private: %w", err)
	}
	return out, nil
}
