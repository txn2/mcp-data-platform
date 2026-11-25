package sitetypecheck

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// Releases is the site service as a release's check reads it: a release's
// source tree, and whether someone may publish to an environment.
type Releases interface {
	ReadBuild(ctx context.Context, actor sitesvc.Actor, ref, environment string, number int) (*sitesvc.ReleaseBuild, error)
	MayPublish(ctx context.Context, actor sitesvc.Actor, ref, environment string) error
}

// Enqueuer queues a release's check; the Queue is one.
type Enqueuer interface {
	Enqueue(ctx context.Context, req Request) (int64, error)
}

// platformActor reads a release's source for a check that runs in the
// background: no person reads it, because the check was asked for by the
// site's own _build.json or by someone who may publish to it.
var platformActor = sitesvc.Actor{Admin: true}

// ReleaseSource is a release's source tree, as the queue reads it.
func ReleaseSource(r Releases) Source {
	return func(ctx context.Context, siteID string, release int) (fs.FS, error) {
		b, err := r.ReadBuild(ctx, platformActor, siteID, "", release)
		if err != nil {
			return nil, err //nolint:wrapcheck // read by identity
		}
		return b.Source, nil
	}
}

// Asked is the type check a release's _build.json asks for, nil for none or a
// release with no build file.
func Asked(src fs.FS) *sitebuild.Typecheck {
	data, err := fs.ReadFile(src, sitebuild.ControlFile)
	if err != nil {
		return nil
	}
	cfg, err := sitebuild.Parse(data)
	if err != nil {
		return nil
	}
	return cfg.Typecheck
}

// Queued is what a request to check a release says of the check it queued.
type Queued struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// AfterPublish queues the check a just-published release asks for. A failure
// is logged and never fails the publish: the check is a check, not a gate.
func AfterPublish(q Enqueuer, r Releases) site.PublishHook {
	return func(ctx context.Context, rel *site.Release, environment, by string) {
		src, err := ReleaseSource(r)(ctx, rel.SiteID, rel.Number)
		if err != nil {
			slog.WarnContext(ctx, "site: reading a release for its type check failed", "site", rel.SiteID, "error", logsan.SanitizeForLog(err.Error()))
			return
		}
		tc := Asked(src)
		if tc == nil {
			return
		}
		if _, err := q.Enqueue(ctx, Request{SiteID: rel.SiteID, Release: rel.Number, Environment: environment, Tsconfig: tc.TsconfigPath(), By: by}); err != nil {
			slog.WarnContext(ctx, "site: queueing a type check failed", "site", rel.SiteID, "error", logsan.SanitizeForLog(err.Error()))
		}
	}
}

// RecheckRequest is a request to check a release again: who asks, the site
// (a name or id), the environment whose incremental state the check uses (dev
// when empty) and the release (0 for the one that environment serves).
type RecheckRequest struct {
	Actor       sitesvc.Actor
	Site        string
	Environment string
	Release     int
}

// Recheck queues a release's type check again, for someone who may publish to
// the site. The release's _build.json must ask for one.
func Recheck(ctx context.Context, q Enqueuer, r Releases, req RecheckRequest) (int, *Queued, error) {
	env := cmp.Or(req.Environment, site.EnvDev)
	if err := r.MayPublish(ctx, req.Actor, req.Site, env); err != nil {
		return 0, nil, err //nolint:wrapcheck // the caller's message
	}
	b, err := r.ReadBuild(ctx, req.Actor, req.Site, env, req.Release)
	if err != nil {
		return 0, nil, err //nolint:wrapcheck // the caller's message
	}
	tc := Asked(b.Source)
	if tc == nil {
		return 0, nil, &sitesvc.RefusedError{Msg: fmt.Sprintf(`release %d's %s asks for no type check; add "typecheck": {"tsconfig": "tsconfig.json"} to it`, b.Release.Number, sitebuild.ControlFile)}
	}
	id, err := q.Enqueue(ctx, Request{SiteID: b.Release.SiteID, Release: b.Release.Number, Environment: env, Tsconfig: tc.TsconfigPath(), By: req.Actor.Email})
	if err != nil {
		return 0, nil, err //nolint:wrapcheck // names the write
	}
	return b.Release.Number, &Queued{ID: id, Status: "queued"}, nil
}
