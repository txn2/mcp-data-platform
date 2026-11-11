package sitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Release retention (#2602). A sweep of one site runs in one transaction under
// the site's row lock, the lock RecordRelease takes, so no release is written
// while the sweep decides. A release is kept while it is in any environment's
// window (its last N distinct releases), an environment points at it, an
// application release pins it, or a pending approval request names it; every
// other release is pruned. A pruned release keeps its row, marked by
// pruned_at, so the deployments and approval requests naming it stay as
// history (both reach it through ON DELETE CASCADE foreign keys); its file
// rows are deleted, and the objects no kept release names are returned for
// the caller to delete once the transaction has committed.

// jobFresh is how recently a running job must have reported to count as
// running: past it the replica running it stopped (sitesvc's jobStaleAfter).
const jobFresh = 15 * time.Minute

// treeKinds is every tree a release holds rows for.
var treeKinds = []string{site.TreeOutput, site.TreeSource, site.TreeRender}

// PruneResult is what a sweep of one site did, or in report mode would do.
type PruneResult struct {
	// Skipped is true when a publish or copy job was running or a publish
	// held the site's stored files, so the site was left for the next sweep.
	Skipped bool
	// Releases is the numbers of the releases pruned.
	Releases []int
	// Rewritten is how many kept releases were rewritten so that none still
	// reads its files through a pruned one.
	Rewritten int
	// Objects is each object no kept release names: one per digest for a file
	// stored by digest, one per path for a file stored under a release's own
	// prefix.
	Objects []site.File
	// Bytes is what the objects and their compressed forms hold.
	Bytes int64
}

// querier is a transaction or the pool.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// releaseLink is one release's place in the manifest chains.
type releaseLink struct {
	id     string
	number int
	parent string
	depth  int
	pruned bool
}

// rewrite is a kept release's rows replaced so it no longer reads through a
// pruned release: amended onto the nearest kept ancestor, or whole.
type rewrite struct {
	id     string
	parent string
	depth  int
	rows   *fileRows
	// trees is what the release resolves to, before and after.
	trees map[string][]site.File
}

// Prune sweeps one site: with apply it prunes, and without it only reports
// what pruning would do, deciding both the same way.
func (s *Store) Prune(ctx context.Context, siteID string, apply bool) (*PruneResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := s.pruneSite(ctx, tx, siteID, apply)
	if err != nil || !apply || res.Skipped || len(res.Releases) == 0 {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

func (s *Store) pruneSite(ctx context.Context, tx *sql.Tx, siteID string, apply bool) (*PruneResult, error) {
	res := &PruneResult{Releases: []int{}, Objects: []site.File{}}
	plan, skip, err := s.planPrune(ctx, tx, siteID, res)
	if err != nil || skip {
		return res, err
	}
	rewrites, err := planRewrites(ctx, tx, plan)
	if err != nil {
		return nil, err
	}
	if res.Objects, res.Bytes, err = unnamedObjects(ctx, tx, plan, rewrites); err != nil {
		return nil, err
	}
	res.Rewritten = len(rewrites)
	for _, l := range plan.pruned {
		res.Releases = append(res.Releases, l.number)
	}
	slices.Sort(res.Releases)
	if !apply {
		return res, nil
	}
	if err := applyRewrites(ctx, tx, rewrites); err != nil {
		return nil, err
	}
	return res, markPruned(ctx, tx, plan.prunedIDs())
}

// planPrune locks the site and splits its releases into kept and pruned; skip
// is true when there is nothing to do: a job running (res says so), every
// environment keeping every release, or nothing past the limits.
func (s *Store) planPrune(ctx context.Context, tx *sql.Tx, siteID string, res *PruneResult) (prunePlan, bool, error) {
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM sites WHERE id = $1 FOR UPDATE`, siteID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prunePlan{}, true, site.ErrNotFound
		}
		return prunePlan{}, true, fmt.Errorf("lock site: %w", err)
	}
	busy, keepsAll, err := s.leaveAlone(ctx, tx, siteID)
	if err != nil || keepsAll || busy {
		res.Skipped = busy && err == nil && !keepsAll
		return prunePlan{}, true, err
	}
	links, err := releaseLinks(ctx, tx, siteID)
	if err != nil {
		return prunePlan{}, true, err
	}
	kept, err := s.keptNumbers(ctx, tx, siteID)
	if err != nil {
		return prunePlan{}, true, err
	}
	plan := splitReleases(links, kept)
	return plan, len(plan.pruned) == 0, nil
}

// leaveAlone reports a site the sweep must not touch now: one with a job
// running or a publish holding its stored files (holds.go), or one where
// every environment keeps every release, which is what a site was before
// limits existed.
func (s *Store) leaveAlone(ctx context.Context, tx *sql.Tx, siteID string) (busy, keepsAll bool, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM site_jobs WHERE site_id = $1 AND status = 'running' AND updated_at > NOW() - make_interval(secs => $3))
		    OR EXISTS (SELECT 1 FROM site_object_holds WHERE site_id = $1 AND kind = 'upload' AND expires_at > clock_timestamp()),
		       COALESCE(bool_and(COALESCE(e.keep_releases, $2) = 0), TRUE)
		  FROM site_environments e WHERE e.site_id = $1`,
		siteID, s.keepDefault(), jobFresh.Seconds()).Scan(&busy, &keepsAll)
	if err != nil {
		return false, false, fmt.Errorf("read the site's jobs and limits: %w", err)
	}
	return busy, keepsAll, nil
}

func releaseLinks(ctx context.Context, q querier, siteID string) ([]releaseLink, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, number, COALESCE(manifest_parent, ''), manifest_depth, pruned_at IS NOT NULL
		  FROM site_releases WHERE site_id = $1 ORDER BY number`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []releaseLink{}
	for rows.Next() {
		var l releaseLink
		if err := rows.Scan(&l.id, &l.number, &l.parent, &l.depth, &l.pruned); err != nil {
			return nil, fmt.Errorf("read release: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	return out, nil
}

// keptNumbers is every release number the site must keep: each environment's
// window and the release it serves, each release an application release pins,
// and each release a pending approval request names.
func (s *Store) keptNumbers(ctx context.Context, q querier, siteID string) (map[int]bool, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT w.to_release FROM site_environments e, LATERAL (`+windowOf+`) w WHERE e.site_id = $1
		UNION SELECT release_number FROM site_environments WHERE site_id = $1 AND release_number IS NOT NULL
		UNION SELECT p.version FROM application_release_pins p JOIN sites st ON st.id = $1
		       WHERE p.kind = 'site' AND split_part(p.part_id, '/', 1) = st.name
		UNION SELECT r.number FROM approval_requests a JOIN site_releases r ON r.id = a.site_release_id
		       WHERE r.site_id = $1 AND a.status = 'pending'`, siteID, s.keepDefault())
	if err != nil {
		return nil, fmt.Errorf("read the releases to keep: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]bool{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("read a release to keep: %w", err)
		}
		out[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the releases to keep: %w", err)
	}
	return out, nil
}

// prunePlan is a site's releases split into those kept and those pruned now.
type prunePlan struct {
	byID   map[string]releaseLink
	kept   []releaseLink
	pruned []releaseLink
	isGone map[string]bool
}

func (p prunePlan) prunedIDs() []string {
	ids := make([]string, 0, len(p.pruned))
	for _, l := range p.pruned {
		ids = append(ids, l.id)
	}
	return ids
}

func (p prunePlan) keptIDs(except map[string]bool) []string {
	ids := make([]string, 0, len(p.kept))
	for _, l := range p.kept {
		if !except[l.id] {
			ids = append(ids, l.id)
		}
	}
	return ids
}

// splitReleases sorts the releases not pruned before into kept and pruned, the
// kept in ascending number so a parent is rewritten before its children.
func splitReleases(links []releaseLink, kept map[int]bool) prunePlan {
	p := prunePlan{byID: make(map[string]releaseLink, len(links)), isGone: map[string]bool{}}
	for _, l := range links {
		p.byID[l.id] = l
		switch {
		case l.pruned:
			p.isGone[l.id] = true
		case kept[l.number]:
			p.kept = append(p.kept, l)
		default:
			p.pruned = append(p.pruned, l)
			p.isGone[l.id] = true
		}
	}
	return p
}

// planRewrites is every kept release whose parent is pruned now, each amended
// onto its nearest kept ancestor (which only shortens its chain), or written
// whole when it has none.
func planRewrites(ctx context.Context, q querier, p prunePlan) ([]rewrite, error) {
	out := []rewrite{}
	depths := map[string]int{}
	for _, l := range p.kept {
		if l.parent == "" || !p.isGone[l.parent] {
			continue
		}
		ancestor := l.parent
		for ancestor != "" && p.isGone[ancestor] {
			ancestor = p.byID[ancestor].parent
		}
		rw, err := planRewrite(ctx, q, l, ancestor)
		if err != nil {
			return nil, err
		}
		if ancestor != "" {
			d, ok := depths[ancestor]
			if !ok {
				d = p.byID[ancestor].depth
			}
			rw.depth = d + 1
		}
		depths[l.id] = rw.depth
		out = append(out, rw)
	}
	return out, nil
}

func planRewrite(ctx context.Context, q querier, l releaseLink, ancestor string) (rewrite, error) {
	rw := rewrite{id: l.id, parent: ancestor, rows: &fileRows{}, trees: map[string][]site.File{}}
	for _, kind := range treeKinds {
		own, err := treeOn(ctx, q, l.id, kind)
		if err != nil {
			return rw, err
		}
		rw.trees[kind] = own
		if ancestor == "" {
			for _, f := range own {
				rw.rows.add(kind, f, false)
			}
			continue
		}
		from, err := treeOn(ctx, q, ancestor, kind)
		if err != nil {
			return rw, err
		}
		amend(rw.rows, kind, from, own)
	}
	return rw, nil
}

// amend adds the rows that turn the from file set into own: each path own
// holds differently or newly, and a removal for each path it no longer holds.
func amend(rows *fileRows, kind string, from, own []site.File) {
	before := make(map[string]site.File, len(from))
	for _, f := range from {
		before[f.Path] = f
	}
	for _, f := range own {
		if prev, ok := before[f.Path]; !ok || prev != f {
			rows.add(kind, f, false)
		}
		delete(before, f.Path)
	}
	gone := make([]string, 0, len(before))
	for p := range before {
		gone = append(gone, p)
	}
	slices.Sort(gone)
	for _, p := range gone {
		rows.add(kind, site.File{Path: p}, true)
	}
}

// objectKey is a stored object's identity: its digest when stored by digest,
// its release prefix and path when stored under a release's own prefix.
func objectKey(f site.File) string {
	if f.StoredIn == "" {
		return "d:" + f.SHA256
	}
	return "r:" + f.StoredIn + "\x00" + f.Path
}

// unnamedObjects is each object a pruned release's rows name that no kept
// release names once the rewrites are made, with what it holds.
func unnamedObjects(ctx context.Context, q querier, p prunePlan, rewrites []rewrite) ([]site.File, int64, error) {
	rewritten := make(map[string]bool, len(rewrites))
	named := map[string]bool{}
	for _, rw := range rewrites {
		rewritten[rw.id] = true
		for i, gone := range rw.rows.removed {
			if !gone {
				named[objectKey(site.File{Path: rw.rows.paths[i], SHA256: rw.rows.digests[i], StoredIn: rw.rows.stored[i]})] = true
			}
		}
	}
	keptRows, err := storedObjects(ctx, q, p.keptIDs(rewritten))
	if err != nil {
		return nil, 0, err
	}
	for _, f := range keptRows {
		named[objectKey(f)] = true
	}
	candidates, err := storedObjects(ctx, q, p.prunedIDs())
	if err != nil {
		return nil, 0, err
	}
	out := []site.File{}
	var bytes int64
	for _, f := range candidates {
		if named[objectKey(f)] {
			continue
		}
		out = append(out, f)
		bytes += f.Size + f.GzipSize + f.BrotliSize
	}
	return out, bytes, nil
}

// storedObjects is each object the releases' rows name, once, with the largest
// size any row records for it.
func storedObjects(ctx context.Context, q querier, releaseIDs []string) ([]site.File, error) {
	if len(releaseIDs) == 0 {
		return []site.File{}, nil
	}
	rows, err := q.QueryContext(ctx, `
		SELECT CASE WHEN f.stored_in = '' THEN '' ELSE f.path END, f.sha256, '', MAX(f.size), MAX(f.gzip_size), MAX(f.brotli_size), f.stored_in
		  FROM site_release_files f
		 WHERE f.release_id = ANY($1) AND NOT f.removed
		 GROUP BY 1, 2, f.stored_in`, pq.Array(releaseIDs))
	if err != nil {
		return nil, fmt.Errorf("list stored files: %w", err)
	}
	return scanFiles(rows)
}

// applyRewrites replaces each rewritten release's rows and link, then reads
// every one back: a release that would no longer resolve to the files it held
// fails the sweep, and the transaction rolls back.
func applyRewrites(ctx context.Context, tx *sql.Tx, rewrites []rewrite) error {
	for _, rw := range rewrites {
		if err := writeRewrite(ctx, tx, rw); err != nil {
			return err
		}
	}
	for _, rw := range rewrites {
		if err := verifyRewrite(ctx, tx, rw); err != nil {
			return err
		}
	}
	return nil
}

func writeRewrite(ctx context.Context, tx *sql.Tx, rw rewrite) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_release_files WHERE release_id = $1`, rw.id); err != nil {
		return fmt.Errorf("clear a rewritten release's files: %w", err)
	}
	if err := insertRows(ctx, tx, rw.id, rw.rows); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE site_releases SET manifest_parent = NULLIF($2, ''), manifest_depth = $3 WHERE id = $1`,
		rw.id, rw.parent, rw.depth); err != nil {
		return fmt.Errorf("relink a rewritten release: %w", err)
	}
	return nil
}

// verifyRewrite reads a rewritten release's trees back through the
// transaction and compares them with what it held before.
func verifyRewrite(ctx context.Context, tx *sql.Tx, rw rewrite) error {
	for _, kind := range treeKinds {
		now, err := treeOn(ctx, tx, rw.id, kind)
		if err != nil {
			return err
		}
		if !slices.Equal(now, rw.trees[kind]) {
			return fmt.Errorf("release %s would no longer serve the files it held (%s tree): nothing was pruned", rw.id, kind)
		}
	}
	return nil
}

// markPruned deletes the pruned releases' rows and marks them, keeping each
// release record.
func markPruned(ctx context.Context, tx *sql.Tx, ids []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_release_files WHERE release_id = ANY($1)`, pq.Array(ids)); err != nil {
		return fmt.Errorf("delete pruned files: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE site_releases SET pruned_at = NOW() WHERE id = ANY($1)`, pq.Array(ids)); err != nil {
		return fmt.Errorf("mark releases pruned: %w", err)
	}
	return nil
}

// SiteRef names a site for a sweep.
type SiteRef struct {
	ID   string
	Name string
}

// SiteRefs lists every site, by name.
func (s *Store) SiteRefs(ctx context.Context) ([]SiteRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM sites ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []SiteRef{}
	for rows.Next() {
		var r SiteRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, fmt.Errorf("read site: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	return out, nil
}

// Sweep is one recorded sweep: counts only, never a path or a key.
type Sweep struct {
	Mode        string    `json:"mode" example:"delete"`
	RunBy       string    `json:"run_by,omitempty"`
	Scope       string    `json:"scope,omitempty"`
	Sites       int       `json:"sites_swept"`
	Releases    int       `json:"releases"`
	Rewritten   int       `json:"rewritten"`
	Files       int       `json:"files"`
	Bytes       int64     `json:"bytes"`
	Orphans     int       `json:"orphans"`
	OrphanBytes int64     `json:"orphan_bytes"`
	Skipped     int       `json:"skipped"`
	Refused     int       `json:"refused"`
	Failed      int       `json:"failed"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
}

// keptSweeps is how many sweep records the table holds.
const keptSweeps = 100

// RecordSweep stores a sweep's counts and drops the oldest past keptSweeps.
func (s *Store) RecordSweep(ctx context.Context, w *Sweep) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO site_retention_sweeps (mode, run_by, scope, sites, releases, rewritten, files, bytes, orphans, orphan_bytes,
			                                   skipped, refused, failed, started_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING finished_at`,
			w.Mode, w.RunBy, w.Scope, w.Sites, w.Releases, w.Rewritten, w.Files, w.Bytes, w.Orphans, w.OrphanBytes,
			w.Skipped, w.Refused, w.Failed, w.StartedAt).Scan(&w.FinishedAt); err != nil {
			return fmt.Errorf("record sweep: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM site_retention_sweeps WHERE id NOT IN (SELECT id FROM site_retention_sweeps ORDER BY id DESC LIMIT $1)`,
			keptSweeps); err != nil {
			return fmt.Errorf("trim sweeps: %w", err)
		}
		return nil
	})
}

// Sweeps lists the most recent sweeps, newest first.
func (s *Store) Sweeps(ctx context.Context, limit int) ([]Sweep, error) {
	if limit <= 0 || limit > keptSweeps {
		limit = keptSweeps
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT mode, run_by, scope, sites, releases, rewritten, files, bytes, orphans, orphan_bytes, skipped, refused, failed, started_at, finished_at
		  FROM site_retention_sweeps ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list sweeps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Sweep{}
	for rows.Next() {
		var w Sweep
		if err := rows.Scan(&w.Mode, &w.RunBy, &w.Scope, &w.Sites, &w.Releases, &w.Rewritten, &w.Files, &w.Bytes, &w.Orphans,
			&w.OrphanBytes, &w.Skipped, &w.Refused, &w.Failed, &w.StartedAt, &w.FinishedAt); err != nil {
			return nil, fmt.Errorf("read sweep: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sweeps: %w", err)
	}
	return out, nil
}

// KeptBy is, for each release number the site keeps, what keeps it: the
// environments whose window holds it, each application release pinning it
// ("application <name>"), and a pending approval request ("approval"). A
// release with no entry is pruned at the next sweep, or was pruned.
func (s *Store) KeptBy(ctx context.Context, siteID string) (map[int][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT n, why FROM (
			SELECT w.to_release AS n, e.name AS why FROM site_environments e, LATERAL (`+windowOf+`) w WHERE e.site_id = $1
			UNION ALL SELECT release_number, name FROM site_environments WHERE site_id = $1 AND release_number IS NOT NULL
			UNION ALL SELECT p.version, 'application ' || a.name FROM application_release_pins p
			          JOIN applications a ON a.id = p.application_id JOIN sites st ON st.id = $1
			         WHERE p.kind = 'site' AND split_part(p.part_id, '/', 1) = st.name
			UNION ALL SELECT r.number, 'approval' FROM approval_requests ar JOIN site_releases r ON r.id = ar.site_release_id
			         WHERE r.site_id = $1 AND ar.status = 'pending'
		) k ORDER BY n, why`, siteID, s.keepDefault())
	if err != nil {
		return nil, fmt.Errorf("read what keeps each release: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int][]string{}
	for rows.Next() {
		var n int
		var why string
		if err := rows.Scan(&n, &why); err != nil {
			return nil, fmt.Errorf("read what keeps a release: %w", err)
		}
		out[n] = append(out[n], why)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read what keeps each release: %w", err)
	}
	return out, nil
}
