// Package siteprune is release retention for sites (#2602): the sweep that
// prunes each site's releases past its environments' limits, deletes the
// objects only those releases named, and removes objects no release names
// once they are older than a grace. It runs on a schedule under an advisory
// lock, one replica at a time, and on an administrator's request, in delete
// mode or in report mode, which decides the same way and deletes nothing.
//
// A key carries a site's id and a file's path, so no key is logged or put in
// an error: a sweep reports counts, and release numbers per site.
package siteprune

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/advisorylock"
	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// LockKey is the advisory lock one sweep at a time holds ("sitepru").
const LockKey int64 = 0x73697465707275

const (
	// checkEvery is how often the schedule looks whether a sweep is due: a
	// sweep is due when the last scheduled one, on any replica, is older than
	// the interval, so a replica restarted daily still sweeps daily.
	checkEvery = time.Hour
	// parallelDeletes is how many objects are deleted at once.
	parallelDeletes = 8
	// breakerFloor is how many orphans a site may have before the
	// half-orphaned check applies: a site holding a handful of objects can be
	// mostly orphans honestly.
	breakerFloor = 100
	// sweepTimeout bounds one sweep of every site.
	sweepTimeout = time.Hour
	// deleteBatch is how many keys are deleted between renewals of the sweep's
	// hold on a site's stored files.
	deleteBatch = 512
)

// ErrBusy is a sweep asked for while another replica is sweeping.
var ErrBusy = errors.New("another sweep is running")

// errUploading is a site's orphans left alone because a publish held its
// stored files when the sweep came to delete them.
var errUploading = errors.New("a publish holds the site's stored files")

// Store is what a sweep reads and writes.
type Store interface {
	SiteRefs(ctx context.Context) ([]sitestore.SiteRef, error)
	GetByName(ctx context.Context, name string) (*site.Site, error)
	Prune(ctx context.Context, siteID string, apply bool) (*sitestore.PruneResult, error)
	// HoldSweep holds a site's stored objects against publishes while a sweep
	// decides and deletes; nil when a publish holds them or a job runs.
	HoldSweep(ctx context.Context, siteID string) (*sitestore.Hold, error)
	StoredFiles(ctx context.Context, siteID string) ([]site.File, error)
	RecordSweep(ctx context.Context, w *sitestore.Sweep) error
	Sweeps(ctx context.Context, limit int) ([]sitestore.Sweep, error)
}

// objects deletes from the bucket sites are stored in.
type objects interface {
	DeleteObject(ctx context.Context, bucket, key string) error
}

// walker lists the bucket; without one a sweep prunes and leaves orphans.
type walker interface {
	Walk(ctx context.Context, bucket, prefix string, fn func(s3adapter.WalkedObject) error) error
}

// Runner sweeps.
type Runner struct {
	// DB holds the advisory lock; nil runs without one (a test).
	DB    *sql.DB
	Store Store
	// Objects deletes from the bucket, Walker lists it for orphans; either
	// may be nil.
	Objects objects
	Walker  walker
	Bucket  string
	// Config is the configuration in force.
	Config func() siteconfig.Config
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Request is one sweep.
type Request struct {
	// Mode is siteconfig.ModeDelete or siteconfig.ModeReport.
	Mode string
	// Site, when set, sweeps that site alone, by name.
	Site string
	// RunBy is the administrator who asked, "" for the schedule.
	RunBy string
}

// SiteReport is what a sweep did to one site, or would do.
type SiteReport struct {
	Site        string `json:"site"`
	Releases    []int  `json:"releases"`
	Rewritten   int    `json:"rewritten"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Orphans     int    `json:"orphans"`
	OrphanBytes int64  `json:"orphan_bytes"`
	// Skipped is a site left for the next sweep because a job was running or
	// a publish held its stored files.
	Skipped bool `json:"skipped,omitempty"`
	// OrphansUnchecked is a site whose prune counts stand but whose orphans
	// were not checked: the bucket could not be listed, or a publish held
	// the site's stored files when the sweep came to delete them.
	OrphansUnchecked bool `json:"orphans_unchecked,omitempty"`
	// Refused is a site whose orphans were left alone because more than half
	// of what it holds read as orphans: a database restored apart from its
	// bucket would otherwise be emptied.
	Refused bool `json:"refused,omitempty"`
}

// Report is a sweep's counts and what it did per site.
type Report struct {
	sitestore.Sweep
	Sites []SiteReport `json:"sites"`
}

func (r *Runner) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

// Run sweeps the site the request names, or every site, under the advisory
// lock, and records what it did.
func (r *Runner) Run(ctx context.Context, req Request) (*Report, error) {
	if req.Mode != siteconfig.ModeDelete && req.Mode != siteconfig.ModeReport {
		return nil, fmt.Errorf("mode: %q or %q", siteconfig.ModeDelete, siteconfig.ModeReport)
	}
	release, err := r.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	targets, err := r.targets(ctx, req.Site)
	if err != nil {
		return nil, err
	}
	rep := &Report{Sites: []SiteReport{}}
	rep.Mode, rep.RunBy, rep.Scope, rep.StartedAt = req.Mode, req.RunBy, req.Site, r.now()
	for _, t := range targets {
		sr, err := r.sweepSite(ctx, t, req.Mode == siteconfig.ModeDelete, &rep.Sweep)
		if err != nil {
			slog.WarnContext(ctx, "sites: a release sweep of one site failed", "error", logsan.SanitizeForLog(err.Error()))
			rep.Failed++
			continue
		}
		rep.add(sr)
	}
	if err := r.Store.RecordSweep(ctx, &rep.Sweep); err != nil {
		return nil, err //nolint:wrapcheck // the store's own sentence
	}
	slog.InfoContext(ctx, "sites: release sweep", "mode", logsan.SanitizeForLog(rep.Mode), "sites", rep.Sweep.Sites, "releases", rep.Releases,
		"files", rep.Files, "bytes", rep.Bytes, "orphans", rep.Orphans, "skipped", rep.Skipped, "refused", rep.Refused, "failed", rep.Failed)
	return rep, nil
}

func (rep *Report) add(sr SiteReport) {
	rep.Sweep.Sites++
	rep.Releases += len(sr.Releases)
	rep.Rewritten += sr.Rewritten
	rep.Files += sr.Files
	rep.Bytes += sr.Bytes
	rep.Orphans += sr.Orphans
	rep.OrphanBytes += sr.OrphanBytes
	if sr.Skipped {
		rep.Skipped++
	}
	if sr.Refused {
		rep.Refused++
	}
	rep.Sites = append(rep.Sites, sr)
}

func (r *Runner) lock(ctx context.Context) (func(), error) {
	if r.DB == nil {
		return func() {}, nil
	}
	release, got, err := advisorylock.Try(ctx, r.DB, advisorylock.ID(LockKey))
	if err != nil {
		return nil, fmt.Errorf("taking the sweep lock: %w", err)
	}
	if !got {
		return nil, ErrBusy
	}
	return release, nil
}

func (r *Runner) targets(ctx context.Context, name string) ([]sitestore.SiteRef, error) {
	if name == "" {
		return r.Store.SiteRefs(ctx) //nolint:wrapcheck // the store's own sentence
	}
	st, err := r.Store.GetByName(ctx, name)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return []sitestore.SiteRef{{ID: st.ID, Name: st.Name}}, nil
}

// sweepSite prunes one site and then removes its orphans. In delete mode the
// objects go after the pruning commits, so a crash between leaves at worst an
// object no release names, which a later sweep removes as an orphan; and the
// site's stored files are held against publishes from before the sweep
// decides until its deletes finish (sitestore/holds.go), so no publish stores
// a file the sweep is about to delete. An error is the prune's alone: once a
// prune has committed, its counts are the site's report whatever the orphan
// check does.
func (r *Runner) sweepSite(ctx context.Context, t sitestore.SiteRef, apply bool, totals *sitestore.Sweep) (SiteReport, error) {
	sr := SiteReport{Site: t.Name, Releases: []int{}}
	hold, held, err := r.holdFor(ctx, t.ID, apply)
	if err != nil || !held {
		sr.Skipped = err == nil
		return sr, err
	}
	res, err := r.Store.Prune(ctx, t.ID, apply)
	if err != nil || res.Skipped {
		hold.Release(ctx)
		sr.Skipped = err == nil
		return sr, err //nolint:wrapcheck // the store's own sentence
	}
	sr.Releases, sr.Rewritten, sr.Files, sr.Bytes = res.Releases, res.Rewritten, len(res.Objects), res.Bytes
	if apply && r.Objects != nil {
		totals.Failed += r.deleteKeys(ctx, prunedKeys(t.ID, res.Objects), hold)
	}
	hold.Release(ctx)
	r.checkOrphans(ctx, t, pass{apply: apply, site: &sr, totals: totals})
	return sr, nil
}

// checkOrphans removes a site's orphans once its prune is done, and marks the
// site's report when they could not be checked; a publish holding the site's
// files is not worth a warning, since a later sweep checks them.
func (r *Runner) checkOrphans(ctx context.Context, t sitestore.SiteRef, p pass) {
	if r.Objects == nil || r.Walker == nil {
		return
	}
	if err := r.orphans(ctx, t, p); err != nil {
		p.site.OrphansUnchecked = true
		if !errors.Is(err, errUploading) {
			slog.WarnContext(ctx, "sites: a site's orphans were not checked", "error", logsan.SanitizeForLog(err.Error()))
		}
	}
}

// holdFor holds a site's stored files for a delete sweep; held is false when
// a publish holds them or a job runs. Report mode deletes nothing and takes
// no hold.
func (r *Runner) holdFor(ctx context.Context, siteID string, apply bool) (hold *sitestore.Hold, held bool, err error) {
	if !apply {
		return nil, true, nil
	}
	hold, err = r.Store.HoldSweep(ctx, siteID)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // the store's own sentence
	}
	return hold, hold != nil, nil
}

// pass is one site's sweep as the orphan steps see it: whether it deletes,
// what it reports for the site, and the sweep's totals.
type pass struct {
	apply  bool
	site   *SiteReport
	totals *sitestore.Sweep
}

// prunedKeys is every key a pruned object is stored under: the file and both
// compressed forms, whether or not a row recorded them, since deleting a key
// that is not there does nothing and a form stored for another type of the
// same bytes is otherwise left behind.
func prunedKeys(siteID string, objects []site.File) []string {
	keys := make([]string, 0, len(objects))
	for _, f := range objects {
		for _, enc := range []string{"", "gz", "br"} {
			keys = append(keys, sitesvc.ObjectKey(siteID, f, enc))
		}
	}
	return keys
}

// deleteKeys deletes keys a few at a time, renewing the sweep's hold between
// batches, and reports how many failed; a failure is logged as a count and
// left for a later sweep. A hold that ran out stops the deletes: a publish
// may be storing files again, and what is left is removed as orphans later.
func (r *Runner) deleteKeys(ctx context.Context, keys []string, hold renewer) int {
	n := 0
	for start := 0; start < len(keys); start += deleteBatch {
		if err := hold.Renew(ctx); err != nil {
			slog.WarnContext(ctx, "sites: a sweep's hold on a site's files ran out; its remaining deletes are left for a later sweep",
				"error", logsan.SanitizeForLog(err.Error()), "left", len(keys)-start)
			n += len(keys) - start
			break
		}
		n += r.deleteBatch(ctx, keys[start:min(start+deleteBatch, len(keys))])
	}
	if n > 0 {
		slog.WarnContext(ctx, "sites: deleting pruned objects failed; a later sweep removes them", "failed", n)
	}
	return n
}

// renewer is a hold a sweep keeps alive while it deletes.
type renewer interface {
	Renew(ctx context.Context) error
}

// deleteBatch deletes keys a few at a time and counts the failures.
func (r *Runner) deleteBatch(ctx context.Context, keys []string) int {
	var g errgroup.Group
	g.SetLimit(parallelDeletes)
	failed := make(chan struct{}, len(keys))
	for _, key := range keys {
		g.Go(func() error {
			if err := r.Objects.DeleteObject(context.WithoutCancel(ctx), r.Bucket, key); err != nil {
				failed <- struct{}{}
			}
			return nil
		})
	}
	_ = g.Wait()
	close(failed)
	return len(failed)
}

// Loop runs the scheduled sweep until stop closes: hourly it looks whether the
// last scheduled sweep is older than the interval, and sweeps when it is.
func (r *Runner) Loop(ctx context.Context, stop <-chan struct{}) {
	bgloop.Run(ctx, bgloop.Loop{
		Name: bgloop.NameSiteReleaseSweep, Every: checkEvery, Immediate: true, Stop: stop,
		Body: r.scheduled,
	})
}

// scheduled sweeps every site when a sweep is due.
func (r *Runner) scheduled(ctx context.Context) error {
	due, err := r.due(ctx)
	if err != nil || !due {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()
	_, err = r.Run(ctx, Request{Mode: r.Config().Releases.SweepMode()})
	if errors.Is(err, ErrBusy) {
		return bgloop.ErrSkipped
	}
	return err
}

// due reports whether the last scheduled sweep, on any replica, is older than
// the interval.
func (r *Runner) due(ctx context.Context) (bool, error) {
	recent, err := r.Store.Sweeps(ctx, 0)
	if err != nil {
		return false, err //nolint:wrapcheck // the store's own sentence
	}
	for _, w := range recent {
		if w.RunBy == "" && w.Scope == "" {
			return r.now().Sub(w.FinishedAt) >= r.Config().Releases.Interval(), nil
		}
	}
	return true, nil
}

// orphanSet is what one site's objects read as against the rows naming them.
type orphanSet struct {
	listed  int
	orphans []s3adapter.WalkedObject
}

// orphans removes the objects under a site that no release names and that are
// older than the grace. A key outside the layout the platform writes is never
// touched, and a site where more than half of what is listed reads as orphans
// is refused.
func (r *Runner) orphans(ctx context.Context, t sitestore.SiteRef, p pass) error {
	named, err := r.named(ctx, t.ID)
	if err != nil {
		return err
	}
	cutoff := r.now().Add(-r.Config().Releases.Grace())
	set := orphanSet{}
	prefix := "sites/" + t.ID + "/"
	err = r.Walker.Walk(ctx, r.Bucket, prefix, func(o s3adapter.WalkedObject) error {
		id, ok := identify(strings.TrimPrefix(o.Key, prefix))
		if !ok {
			return nil
		}
		set.listed++
		if !named[id] && !o.LastModified.IsZero() && o.LastModified.Before(cutoff) {
			set.orphans = append(set.orphans, o)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("listing a site's objects: %w", err)
	}
	if len(set.orphans) > breakerFloor && 2*len(set.orphans) > set.listed {
		slog.WarnContext(ctx, "sites: orphan removal refused: more than half of a site's objects read as orphans",
			"orphans", len(set.orphans), "objects", set.listed)
		p.site.Refused = true
		return nil
	}
	return r.removeOrphans(ctx, t.ID, set.orphans, p)
}

// removeOrphans reads the rows naming the site's objects again, so a release
// recorded while the listing ran keeps its objects, then counts and, in delete
// mode, deletes what is still unnamed. In delete mode it holds the site's
// stored files first, so no publish re-stores a candidate between the read
// and the delete; a publish holding them leaves the orphans for later.
func (r *Runner) removeOrphans(ctx context.Context, siteID string, candidates []s3adapter.WalkedObject, p pass) error {
	if len(candidates) == 0 {
		return nil
	}
	var hold *sitestore.Hold
	if p.apply {
		h, err := r.Store.HoldSweep(ctx, siteID)
		if err != nil {
			return err //nolint:wrapcheck // the store's own sentence
		}
		if h == nil {
			return errUploading
		}
		hold = h
		defer hold.Release(ctx)
	}
	named, err := r.named(ctx, siteID)
	if err != nil {
		return err
	}
	prefix := "sites/" + siteID + "/"
	keys := make([]string, 0, len(candidates))
	for _, o := range candidates {
		id, _ := identify(strings.TrimPrefix(o.Key, prefix))
		if named[id] {
			continue
		}
		p.site.Orphans++
		p.site.OrphanBytes += o.Size
		keys = append(keys, o.Key)
	}
	if p.apply {
		p.totals.Failed += r.deleteKeys(ctx, keys, hold)
	}
	return nil
}

// named is the identity of every object a release of the site names.
func (r *Runner) named(ctx context.Context, siteID string) (map[string]bool, error) {
	files, err := r.Store.StoredFiles(ctx, siteID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's own sentence
	}
	out := make(map[string]bool, len(files))
	for _, f := range files {
		if f.StoredIn == "" {
			out["d:"+f.SHA256] = true
		} else {
			out["r:"+f.StoredIn+"\x00"+f.Path] = true
		}
	}
	return out, nil
}

// identify reads a key under a site's prefix as the object it is: by digest
// (objects/<sha256>, .gz or .br), or under a release's own prefix
// (releases/<id>/files|gz|br/<path>). Anything else is not the platform's
// layout and is left alone.
func identify(rest string) (string, bool) {
	if name, ok := strings.CutPrefix(rest, "objects/"); ok {
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".br")
		if isDigest(name) {
			return "d:" + name, true
		}
		return "", false
	}
	if tail, ok := strings.CutPrefix(rest, "releases/"); ok {
		parts := strings.SplitN(tail, "/", releaseKeyParts)
		if len(parts) == releaseKeyParts && parts[0] != "" && parts[2] != "" && (parts[1] == "files" || parts[1] == "gz" || parts[1] == "br") {
			return "r:" + parts[0] + "\x00" + parts[2], true
		}
	}
	return "", false
}

// digestLen is a hex SHA-256's length, and releaseKeyParts the parts of a key
// under a release's own prefix: the release, the form, the path.
const (
	digestLen       = 64
	releaseKeyParts = 3
)

func isDigest(s string) bool {
	if len(s) != digestLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
