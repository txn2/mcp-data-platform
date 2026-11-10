// Package sitestore is the PostgreSQL store of sites, releases and domains
// (#2518).
package sitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/site"
)

const (
	uniqueViolation = "23505"
	defaultListing  = 100
)

// Store is the PostgreSQL store.
type Store struct{ db *sql.DB }

// New builds the store over db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// NewSiteID is a site's id.
func NewSiteID() string { return "site_" + uuid.NewString() }

// NewReleaseID is a release's id.
func NewReleaseID() string { return "srel_" + uuid.NewString() }

func isUnique(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == uniqueViolation
}

func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Create writes a site and the environments it names.
func (s *Store) Create(ctx context.Context, st *site.Site) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
			INSERT INTO sites (id, name, description, owner_email, created_by) VALUES ($1, $2, $3, $4, $5)
			RETURNING created_at, updated_at`, st.ID, st.Name, st.Description, st.OwnerEmail, st.CreatedBy,
		).Scan(&st.CreatedAt, &st.UpdatedAt)
		if isUnique(err) {
			return site.ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("create site: %w", err)
		}
		for _, env := range st.Environments {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO site_environments (site_id, name, requires_approval, updated_by) VALUES ($1, $2, $3, $4)`,
				st.ID, env.Name, env.RequiresApproval, st.CreatedBy); err != nil {
				return fmt.Errorf("create environment %s: %w", env.Name, err)
			}
		}
		return nil
	})
}

const siteColumns = `id, name, description, owner_email, internal_view, created_by, created_at, updated_at`

func scanSite(row interface{ Scan(...any) error }) (*site.Site, error) {
	st := &site.Site{Environments: []site.Environment{}}
	err := row.Scan(&st.ID, &st.Name, &st.Description, &st.OwnerEmail, &st.InternalView, &st.CreatedBy, &st.CreatedAt, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, site.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read site: %w", err)
	}
	return st, nil
}

// Get reads a site by id.
func (s *Store) Get(ctx context.Context, id string) (*site.Site, error) {
	return s.one(ctx, `SELECT `+siteColumns+` FROM sites WHERE id = $1`, id)
}

// GetByName reads a site by name.
func (s *Store) GetByName(ctx context.Context, name string) (*site.Site, error) {
	return s.one(ctx, `SELECT `+siteColumns+` FROM sites WHERE name = $1`, name)
}

func (s *Store) one(ctx context.Context, query, arg string) (*site.Site, error) {
	st, err := scanSite(s.db.QueryRowContext(ctx, query, arg))
	if err != nil {
		return nil, err
	}
	if st.Environments, err = s.environments(ctx, st.ID); err != nil {
		return nil, err
	}
	return st, nil
}

func (s *Store) environments(ctx context.Context, siteID string) ([]site.Environment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, COALESCE(release_number, 0), requires_approval, updated_by, updated_at
		  FROM site_environments WHERE site_id = $1 ORDER BY name`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Environment{}
	for rows.Next() {
		var e site.Environment
		if err := rows.Scan(&e.Name, &e.Release, &e.RequiresApproval, &e.UpdatedBy, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read environment: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	return out, nil
}

// List reads every site with its environments.
func (s *Store) List(ctx context.Context) ([]site.Site, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+siteColumns+` FROM sites ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Site{}
	for rows.Next() {
		st, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	for i := range out {
		if out[i].Environments, err = s.environments(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Update writes the description, the owner and whether everyone internal may
// view the site.
func (s *Store) Update(ctx context.Context, st *site.Site) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sites SET description = $2, owner_email = $3, internal_view = $4, updated_at = NOW() WHERE id = $1`,
		st.ID, st.Description, st.OwnerEmail, st.InternalView)
	if err != nil {
		return fmt.Errorf("update site: %w", err)
	}
	return oneRow(res, site.ErrNotFound)
}

func oneRow(res sql.Result, none error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return none
	}
	return nil
}

// Delete removes a site, its releases, environments and domains.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete site: %w", err)
	}
	return oneRow(res, site.ErrNotFound)
}

// SetEnvironment creates an environment or changes its approval rule.
func (s *Store) SetEnvironment(ctx context.Context, siteID, name string, requiresApproval bool, actor string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO site_environments (site_id, name, requires_approval, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (site_id, name) DO UPDATE
		   SET requires_approval = EXCLUDED.requires_approval, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		siteID, name, requiresApproval, actor)
	if err != nil {
		return fmt.Errorf("set environment: %w", err)
	}
	return nil
}

// RecordRelease writes a release and its files, numbering it next under the
// site's row lock so two publishes get two numbers. A move points the
// environment at the release in the same transaction.
func (s *Store) RecordRelease(ctx context.Context, r *site.Release, move *site.Pointer) error {
	redirects, err := json.Marshal(r.Redirects)
	if err != nil {
		return fmt.Errorf("encode redirects: %w", err)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := writeRelease(ctx, tx, r, redirects); err != nil {
			return err
		}
		if err := insertFiles(ctx, tx, r); err != nil {
			return err
		}
		if move == nil {
			return nil
		}
		move.To = r.Number
		return point(ctx, tx, *move)
	})
}

// writeRelease numbers a release under the site's row lock and inserts it.
func writeRelease(ctx context.Context, tx *sql.Tx, r *site.Release, redirects []byte) error {
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM sites WHERE id = $1 FOR UPDATE`, r.SiteID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site.ErrNotFound
		}
		return fmt.Errorf("lock site: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM site_releases WHERE site_id = $1`, r.SiteID).Scan(&r.Number); err != nil {
		return fmt.Errorf("number release: %w", err)
	}
	err := tx.QueryRowContext(ctx, `
		INSERT INTO site_releases (id, site_id, number, source, note, file_count, total_bytes, manifest_sha256, redirects, created_by,
		                           base_number, manifest_parent, manifest_depth, rendered)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, ''), $13, $14) RETURNING created_at`,
		r.ID, r.SiteID, r.Number, r.Source, r.Note, r.FileCount, r.TotalBytes, r.ManifestSHA256, redirects, r.CreatedBy,
		r.Base, r.ManifestParent, r.ManifestDepth, r.Rendered,
	).Scan(&r.CreatedAt)
	if isUnique(err) {
		return site.ErrStale
	}
	if err != nil {
		return fmt.Errorf("record release: %w", err)
	}
	return nil
}

// fileRows are the columns of site_release_files, one slice each.
type fileRows struct {
	trees, paths, digests, types, stored []string
	sizes, gzips, brotlis                []int64
	removed                              []bool
}

func (w *fileRows) add(tree string, f site.File, removed bool) {
	w.trees, w.paths, w.digests = append(w.trees, tree), append(w.paths, f.Path), append(w.digests, f.SHA256)
	w.types, w.stored = append(w.types, f.ContentType), append(w.stored, f.StoredIn)
	w.sizes, w.gzips, w.brotlis = append(w.sizes, f.Size), append(w.gzips, f.GzipSize), append(w.brotlis, f.BrotliSize)
	w.removed = append(w.removed, removed)
}

func (w *fileRows) addTree(tree string, files []site.File, gone []string) {
	for _, f := range files {
		w.add(tree, f, false)
	}
	for _, p := range gone {
		w.add(tree, site.File{Path: p}, true)
	}
}

// insertFiles writes a release's rows in one statement, whatever their number:
// a whole file set can be 20,000 of them.
func insertFiles(ctx context.Context, tx *sql.Tx, r *site.Release) error {
	rows := &fileRows{}
	rows.addTree(site.TreeOutput, r.Files, r.Removed)
	rows.addTree(site.TreeSource, r.SourceFiles, r.SourceRemoved)
	if r.RenderIndex != nil {
		rows.add(site.TreeRender, *r.RenderIndex, false)
	}
	if len(rows.paths) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO site_release_files (release_id, tree, path, sha256, content_type, size, gzip_size, brotli_size, stored_in, removed)
		SELECT $1, f.* FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::bigint[], $7::bigint[], $8::bigint[], $9::text[], $10::boolean[]) AS f`,
		r.ID, pq.Array(rows.trees), pq.Array(rows.paths), pq.Array(rows.digests), pq.Array(rows.types), pq.Array(rows.sizes),
		pq.Array(rows.gzips), pq.Array(rows.brotlis), pq.Array(rows.stored), pq.Array(rows.removed),
	); err != nil {
		return fmt.Errorf("record files: %w", err)
	}
	return nil
}

const releaseColumns = `id, site_id, number, source, note, file_count, total_bytes, manifest_sha256, redirects, created_by, created_at, base_number, COALESCE(manifest_parent, ''), manifest_depth, rendered`

func scanRelease(row interface{ Scan(...any) error }) (*site.Release, error) {
	r := &site.Release{}
	var redirects []byte
	err := row.Scan(&r.ID, &r.SiteID, &r.Number, &r.Source, &r.Note, &r.FileCount, &r.TotalBytes, &r.ManifestSHA256,
		&redirects, &r.CreatedBy, &r.CreatedAt, &r.Base, &r.ManifestParent, &r.ManifestDepth, &r.Rendered)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, site.ErrNoRelease
	}
	if err != nil {
		return nil, fmt.Errorf("read release: %w", err)
	}
	r.Redirects = []site.Redirect{}
	if err := json.Unmarshal(redirects, &r.Redirects); err != nil {
		return nil, fmt.Errorf("decode redirects: %w", err)
	}
	return r, nil
}

// Release reads a release by its number.
func (s *Store) Release(ctx context.Context, siteID string, number int) (*site.Release, error) {
	return scanRelease(s.db.QueryRowContext(ctx,
		`SELECT `+releaseColumns+` FROM site_releases WHERE site_id = $1 AND number = $2`, siteID, number))
}

// ReleaseByID reads a release by its id.
func (s *Store) ReleaseByID(ctx context.Context, id string) (*site.Release, error) {
	return scanRelease(s.db.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM site_releases WHERE id = $1`, id))
}

// Releases lists a site's releases, newest first.
func (s *Store) Releases(ctx context.Context, siteID string) ([]site.Release, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+releaseColumns+` FROM site_releases WHERE site_id = $1 ORDER BY number DESC`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Release{}
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	return out, nil
}

// Files is what a release serves, in path order.
func (s *Store) Files(ctx context.Context, releaseID string) ([]site.File, error) {
	return s.Tree(ctx, releaseID, site.TreeOutput)
}

// Tree is one tree of a release in path order: for each path, the row of the
// nearest release in its chain, unless that row removes the path.
func (s *Store) Tree(ctx context.Context, releaseID, tree string) ([]site.File, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id, manifest_parent, 0 AS depth FROM site_releases WHERE id = $1
			UNION ALL
			SELECT r.id, r.manifest_parent, c.depth + 1
			  FROM site_releases r JOIN chain c ON r.id = c.manifest_parent
		)
		SELECT path, sha256, content_type, size, gzip_size, brotli_size, stored_in FROM (
			SELECT DISTINCT ON (f.path) f.path, f.sha256, f.content_type, f.size, f.gzip_size, f.brotli_size, f.stored_in, f.removed
			  FROM site_release_files f JOIN chain c ON f.release_id = c.id
			 WHERE f.tree = $2
			 ORDER BY f.path, c.depth
		) nearest
		WHERE NOT removed ORDER BY path`, releaseID, tree)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	return scanFiles(rows)
}

// StoredFiles lists what a site holds in object storage, each object once: a
// digest shared by a thousand releases is one object, and a file stored under
// a release of its own is one object per path.
func (s *Store) StoredFiles(ctx context.Context, siteID string) ([]site.File, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT CASE WHEN f.stored_in = '' THEN '' ELSE f.path END, f.sha256, '', 0::bigint, f.gzip_size, f.brotli_size, f.stored_in
		  FROM site_release_files f JOIN site_releases r ON r.id = f.release_id
		 WHERE r.site_id = $1 AND NOT f.removed`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list stored files: %w", err)
	}
	return scanFiles(rows)
}

func scanFiles(rows *sql.Rows) ([]site.File, error) {
	defer func() { _ = rows.Close() }()
	out := []site.File{}
	for rows.Next() {
		var f site.File
		if err := rows.Scan(&f.Path, &f.SHA256, &f.ContentType, &f.Size, &f.GzipSize, &f.BrotliSize, &f.StoredIn); err != nil {
			return nil, fmt.Errorf("read file: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	return out, nil
}

// Point moves an environment's pointer and records the deployment, in one
// transaction under a compare-and-set on the release the move was decided
// against.
func (s *Store) Point(ctx context.Context, p site.Pointer) error {
	return s.withTx(ctx, func(tx *sql.Tx) error { return point(ctx, tx, p) })
}

func point(ctx context.Context, tx *sql.Tx, p site.Pointer) error {
	var current sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT release_number FROM site_environments WHERE site_id = $1 AND name = $2 FOR UPDATE`,
		p.SiteID, p.Environment).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return site.ErrNoEnvironment
	}
	if err != nil {
		return fmt.Errorf("lock environment: %w", err)
	}
	if int(current.Int64) != p.After {
		return site.ErrStale
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE site_environments SET release_number = $3, updated_by = $4, updated_at = NOW()
		 WHERE site_id = $1 AND name = $2`, p.SiteID, p.Environment, p.To, p.Actor); err != nil {
		return fmt.Errorf("move environment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO site_deployments (site_id, environment, action, from_release, to_release, actor)
		VALUES ($1, $2, $3, $4, $5, $6)`, p.SiteID, p.Environment, p.Action, p.After, p.To, p.Actor); err != nil {
		return fmt.Errorf("record deployment: %w", err)
	}
	return nil
}

// Deployments lists an environment's moves, newest first; limit 0 is the
// default bound.
func (s *Store) Deployments(ctx context.Context, siteID, environment string, limit int) ([]site.Deployment, error) {
	if limit <= 0 {
		limit = defaultListing
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT environment, action, from_release, to_release, actor, created_at FROM site_deployments
		 WHERE site_id = $1 AND ($2 = '' OR environment = $2) ORDER BY id DESC LIMIT $3`, siteID, environment, limit)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Deployment{}
	for rows.Next() {
		var d site.Deployment
		if err := rows.Scan(&d.Environment, &d.Action, &d.From, &d.To, &d.Actor, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("read deployment: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	return out, nil
}

// AddDomain records a hostname, which routes at once.
func (s *Store) AddDomain(ctx context.Context, d *site.Domain) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO site_domains (hostname, site_id, environment, application_id, created_by)
		VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, ''), $5) RETURNING created_at`,
		d.Hostname, d.SiteID, d.Environment, d.ApplicationID, d.CreatedBy).Scan(&d.CreatedAt)
	if isUnique(err) {
		return site.ErrDomainTaken
	}
	if err != nil {
		return fmt.Errorf("add domain: %w", err)
	}
	return nil
}

const domainSelect = `
	SELECT d.hostname, COALESCE(d.site_id, ''), COALESCE(s.name, ''), d.environment, COALESCE(d.application_id, ''),
	       COALESCE(a.name, ''), d.created_by, d.created_at
	  FROM site_domains d
	  LEFT JOIN sites s ON s.id = d.site_id
	  LEFT JOIN applications a ON a.id = d.application_id`

func scanDomain(row interface{ Scan(...any) error }) (*site.Domain, error) {
	d := &site.Domain{}
	err := row.Scan(&d.Hostname, &d.SiteID, &d.Site, &d.Environment, &d.ApplicationID, &d.Application, &d.CreatedBy, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, site.ErrNoDomain
	}
	if err != nil {
		return nil, fmt.Errorf("read domain: %w", err)
	}
	return d, nil
}

// Domain reads one hostname's mapping.
func (s *Store) Domain(ctx context.Context, hostname string) (*site.Domain, error) {
	return scanDomain(s.db.QueryRowContext(ctx, domainSelect+` WHERE d.hostname = $1`, hostname))
}

// Domains lists every mapping.
func (s *Store) Domains(ctx context.Context) ([]site.Domain, error) {
	return s.listDomains(ctx, domainSelect+` ORDER BY d.hostname`)
}

// SiteDomains lists the hostnames added to one site.
func (s *Store) SiteDomains(ctx context.Context, siteID string) ([]site.Domain, error) {
	return s.listDomains(ctx, domainSelect+` WHERE d.site_id = $1 ORDER BY d.hostname`, siteID)
}

func (s *Store) listDomains(ctx context.Context, query string, args ...any) ([]site.Domain, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []site.Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	return out, nil
}

// RemoveDomain deletes a mapping.
func (s *Store) RemoveDomain(ctx context.Context, hostname string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM site_domains WHERE hostname = $1`, hostname)
	if err != nil {
		return fmt.Errorf("remove domain: %w", err)
	}
	return oneRow(res, site.ErrNoDomain)
}

// routeSelect reads an environment and the release it serves.
const routeSelect = `
	SELECT s.id, s.name, e.name, COALESCE(e.release_number, 0)
	  FROM site_environments e JOIN sites s ON s.id = e.site_id`

// RouteHost is what an added hostname serves.
func (s *Store) RouteHost(ctx context.Context, hostname string) (*site.Route, error) {
	d, err := s.Domain(ctx, hostname)
	if err != nil {
		return nil, err
	}
	if d.ApplicationID != "" {
		return &site.Route{Application: d.Application, Hostname: d.Hostname}, nil
	}
	return s.routeOf(ctx, routeSelect+` WHERE s.id = $1 AND e.name = $2`, d.SiteID, d.Environment)
}

// RouteOwn is what a site's own address serves: its default environment, to
// the people who may open it.
func (s *Store) RouteOwn(ctx context.Context, siteName string) (*site.Route, error) {
	r, err := s.routeOf(ctx, routeSelect+` WHERE s.name = $1 AND e.name = $2`, siteName, site.EnvDefault)
	if err != nil {
		return nil, err
	}
	r.Private = true
	return r, nil
}

func (s *Store) routeOf(ctx context.Context, query, a, b string) (*site.Route, error) {
	r := &site.Route{}
	var number int
	err := s.db.QueryRowContext(ctx, query, a, b).Scan(&r.SiteID, &r.Site, &r.Environment, &number)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, site.ErrNoDomain
	}
	if err != nil {
		return nil, fmt.Errorf("read route: %w", err)
	}
	if number > 0 {
		if r.Release, err = s.Release(ctx, r.SiteID, number); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Hosts lists every hostname that routes.
func (s *Store) Hosts(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hostname FROM site_domains`)
	if err != nil {
		return nil, fmt.Errorf("list hostnames: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("read hostname: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hostnames: %w", err)
	}
	return out, nil
}

// ApplicationHosts lists the hostnames of the site environments an
// application holds as parts: each added domain, and the site's own address
// under pagesDomain where the part is its default environment.
func (s *Store) ApplicationHosts(ctx context.Context, applicationID, pagesDomain string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.hostname
		  FROM application_parts p
		  JOIN sites st ON st.name = split_part(p.part_id, '/', 1)
		  JOIN site_domains d ON d.site_id = st.id AND d.environment = split_part(p.part_id, '/', 2)
		 WHERE p.application_id = $1 AND p.kind = 'site'
		UNION ALL
		SELECT split_part(p.part_id, '/', 1) || '.' || $2
		  FROM application_parts p
		 WHERE p.application_id = $1 AND p.kind = 'site' AND split_part(p.part_id, '/', 2) = $3 AND $2 <> ''`, applicationID, pagesDomain, site.EnvDefault)
	if err != nil {
		return nil, fmt.Errorf("list application hosts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("read hostname: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list application hosts: %w", err)
	}
	return out, nil
}

// ExpireHandoffs removes the used codes past their expiry.
func (s *Store) ExpireHandoffs(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM page_handoffs WHERE expires_at < $1`, now); err != nil {
		return fmt.Errorf("expire handoffs: %w", err)
	}
	return nil
}

// UseHandoff records a handoff code's hash, reporting false when it was used
// before.
func (s *Store) UseHandoff(ctx context.Context, codeHash string, expires time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO page_handoffs (code_hash, expires_at) VALUES ($1, $2) ON CONFLICT (code_hash) DO NOTHING`, codeHash, expires)
	if err != nil {
		return false, fmt.Errorf("record handoff: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record handoff: %w", err)
	}
	return n == 1, nil
}
