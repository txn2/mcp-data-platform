package sitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// RenameEnvironment renames an environment between dev and prod (#2630),
// moving every reference to its name in one transaction under the site's row
// lock: the environment, its domains, its live history, its jobs, the main
// address, each grant's environment list, the application parts and pins
// written "<site>/<env>", the target of pending approvals, and its tile, which
// still shows what it serves (#2751; a tile row already under the new name,
// left by an earlier removal, is deleted first). The rename is recorded in the
// environment's history.
func (s *Store) RenameEnvironment(ctx context.Context, siteID, from, to, actor string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var siteName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM sites WHERE id = $1 FOR UPDATE`, siteID).Scan(&siteName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return site.ErrNotFound
			}
			return fmt.Errorf("lock site: %w", err)
		}
		var release int
		err := tx.QueryRowContext(ctx, `
			UPDATE site_environments SET name = $3, updated_by = $4, updated_at = NOW()
			 WHERE site_id = $1 AND name = $2 RETURNING COALESCE(release_number, 0)`, siteID, from, to, actor).Scan(&release)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return site.ErrNoEnvironment
		case isUnique(err):
			return &site.InUseError{Msg: fmt.Sprintf("the site already has an environment named %q", to)}
		case err != nil:
			return fmt.Errorf("rename environment: %w", err)
		}
		oldPart, newPart := siteName+"/"+from, siteName+"/"+to
		for _, stmt := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM site_tiles WHERE site_id = $1 AND environment = $3 AND $2 <> $3`, []any{siteID, from, to}},
			{`UPDATE site_tiles SET environment = $3 WHERE site_id = $1 AND environment = $2`, []any{siteID, from, to}},
			{`UPDATE site_domains SET environment = $3 WHERE site_id = $1 AND environment = $2`, []any{siteID, from, to}},
			{`UPDATE site_deployments SET environment = $3 WHERE site_id = $1 AND environment = $2 AND removed_at IS NULL`, []any{siteID, from, to}},
			{`UPDATE site_jobs SET environment = $3 WHERE site_id = $1 AND environment = $2`, []any{siteID, from, to}},
			{`UPDATE sites SET main_environment = $3, updated_at = NOW() WHERE id = $1 AND main_environment = $2`, []any{siteID, from, to}},
			{`UPDATE site_grants SET environments = array_replace(environments, $2, $3) WHERE site_id = $1 AND $2 = ANY(environments)`, []any{siteID, from, to}},
			{`UPDATE application_parts SET part_id = $2 WHERE kind = 'site' AND part_id = $1`, []any{oldPart, newPart}},
			{`UPDATE application_release_pins SET part_id = $2 WHERE kind = 'site' AND part_id = $1`, []any{oldPart, newPart}},
			{`UPDATE approval_requests SET target = $3
			   WHERE status = 'pending' AND target = $2
			     AND (site_id = $1 OR site_release_id IN (SELECT id FROM site_releases WHERE site_id = $1))
			     AND purpose IN ('site_promote', 'site_redirect')`, []any{siteID, from, to}},
			{`INSERT INTO site_deployments (site_id, environment, action, from_release, to_release, actor, detail)
			  VALUES ($1, $2, 'rename', $3, $3, $4, $5)`, []any{siteID, to, release, actor, from}},
		} {
			if _, err := tx.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
				return fmt.Errorf("rename environment: %w", err)
			}
		}
		return nil
	})
}

// RemoveEnvironment removes an environment between dev and prod (#2630) in one
// transaction under the site's row lock, refusing one that still has a domain,
// holds the main address, is a part of an application, or has a job running.
// It withdraws pending approvals that target it, drops it from each grant's
// environment list and deletes a grant that named only it (an empty list means
// every private environment, so emptying one would widen the grant), marks its
// history removed, deletes its tile so the tile's object is reclaimed (#2751),
// and numbers the remaining environments again.
func (s *Store) RemoveEnvironment(ctx context.Context, siteID, name string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		held, err := lockForRemoval(ctx, tx, siteID, name)
		if err != nil {
			return err
		}
		if err := attached(ctx, tx, held); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM site_environments WHERE site_id = $1 AND name = $2`, siteID, name)
		if err != nil {
			return fmt.Errorf("remove environment: %w", err)
		}
		if err := oneRow(res, site.ErrNoEnvironment); err != nil {
			return err
		}
		return afterRemoval(ctx, tx, siteID, name)
	})
}

// removal is the environment a removal is about, with its site's name and
// main address read under the site's lock.
type removal struct {
	siteID, siteName, name, main string
}

func lockForRemoval(ctx context.Context, tx *sql.Tx, siteID, name string) (removal, error) {
	r := removal{siteID: siteID, name: name}
	err := tx.QueryRowContext(ctx, `SELECT name, COALESCE(main_environment, '') FROM sites WHERE id = $1 FOR UPDATE`, siteID).Scan(&r.siteName, &r.main)
	if errors.Is(err, sql.ErrNoRows) {
		return r, site.ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("lock site: %w", err)
	}
	return r, nil
}

// afterRemoval carries the rest of a removal out: pending approvals
// withdrawn, grants narrowed (one naming only it deleted), its history marked
// removed, its tile deleted, and the order numbered again. Nothing else reads
// a tile row whose environment is gone, and the storage reconcile counts its
// tile_key as a reference, so a row left behind would keep the object forever.
func afterRemoval(ctx context.Context, tx *sql.Tx, siteID, name string) error {
	for _, stmt := range []string{
		`UPDATE approval_requests SET status = 'withdrawn'
		  WHERE status = 'pending' AND target = $2
		    AND (site_id = $1 OR site_release_id IN (SELECT id FROM site_releases WHERE site_id = $1))
		    AND purpose IN ('site_promote', 'site_redirect')`,
		`DELETE FROM site_grants WHERE site_id = $1 AND environments = ARRAY[$2]::text[]`,
		`UPDATE site_grants SET environments = array_remove(environments, $2) WHERE site_id = $1 AND $2 = ANY(environments)`,
		`UPDATE site_deployments SET removed_at = NOW() WHERE site_id = $1 AND environment = $2 AND removed_at IS NULL`,
		`DELETE FROM site_tiles WHERE site_id = $1 AND environment = $2`,
		`UPDATE site_environments e SET position = o.n - 1
		   FROM (SELECT name, ROW_NUMBER() OVER (ORDER BY position, name) AS n FROM site_environments WHERE site_id = $1) o
		  WHERE e.site_id = $1 AND e.name = o.name AND $2 <> ''`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, siteID, name); err != nil {
			return fmt.Errorf("remove environment: %w", err)
		}
	}
	return nil
}

// attached refuses an environment that still has something attached, naming
// what to do first.
func attached(ctx context.Context, tx *sql.Tx, r removal) error {
	if r.name == r.main {
		return inUse("%s holds the main address; give it to another environment first (main-environment)", r.name)
	}
	var domains, parts, jobs int
	if err := tx.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM site_domains WHERE site_id = $1 AND environment = $2),
		       (SELECT COUNT(*) FROM application_parts WHERE kind = 'site' AND part_id = $3),
		       (SELECT COUNT(*) FROM site_jobs WHERE site_id = $1 AND environment = $2 AND status = 'running')`,
		r.siteID, r.name, r.siteName+"/"+r.name).Scan(&domains, &parts, &jobs); err != nil {
		return fmt.Errorf("read what the environment holds: %w", err)
	}
	switch {
	case domains > 0:
		return inUse("%s has %d domain(s); remove them first, so no hostname is freed as a side effect", r.name, domains)
	case parts > 0:
		return inUse("%s is a part of an application; remove the part from the application first", r.name)
	case jobs > 0:
		return inUse("a publish or copy job is running on %s; wait for it to finish or stop it first", r.name)
	}
	return nil
}

func inUse(format string, args ...any) error {
	return &site.InUseError{Msg: fmt.Sprintf(format, args...)}
}
