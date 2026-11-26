package siterender

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild/pkgmap"
)

// packageImports fills in the imports a project's package.json and lockfile
// give a build file that asks for them, and refuses a map that would load two
// copies of a package only one copy of which may load (#2785).
func (r *run) packageImports() error {
	cfg := r.build
	var lock *pkgmap.Lock
	if cfg.ImportsFrom != "" {
		var err error
		if lock, err = r.generateImports(); err != nil {
			return err
		}
	}
	if cfg.Remote != sitebuild.RemoteExternal {
		return nil
	}
	if lock == nil {
		var err error
		if lock, err = pkgmap.ReadLock(r.src); err != nil {
			lock = &pkgmap.Lock{Packages: map[string]pkgmap.Package{}}
		}
	}
	msgs, err := pkgmap.CheckSingletons(r.ctx, cfg.Imports, cfg.Singletons, lock, r.peers())
	if err != nil {
		return fmt.Errorf("checking for copies of a singleton: %w", err)
	}
	for _, m := range msgs {
		r.report(Failure{File: sitebuild.ControlFile, Message: m})
	}
	return nil
}

// importsFromErr is how a failure of imports_from is worded.
const importsFromErr = "imports_from: %w"

// generateImports fills the build file's imports from package.json and the
// lockfile, returning the lockfile it read.
func (r *run) generateImports() (*pkgmap.Lock, error) {
	specs, err := sitebuild.Specifiers(r.ctx, r.src, r.build)
	if err != nil {
		return nil, fmt.Errorf(importsFromErr, err)
	}
	lock, err := pkgmap.ReadLock(r.src)
	if err != nil {
		return nil, fmt.Errorf(importsFromErr, err)
	}
	if lock.File == "" {
		return nil, errors.New("imports_from reads package.json and its lockfile, and the release has no package-lock.json, pnpm-lock.yaml or yarn.lock")
	}
	gen, err := pkgmap.Generate(r.ctx, specs, lock, r.peers())
	if err != nil {
		return nil, fmt.Errorf(importsFromErr, err)
	}
	if missing := r.build.Unmapped(gen.Missing); len(missing) > 0 {
		return nil, errors.New(lock.File + " locks no version of " + strings.Join(missing, ", ") + "; add them to package.json, or map them in imports")
	}
	if err := r.build.Merge(gen.Imports); err != nil {
		return nil, fmt.Errorf(importsFromErr, err)
	}
	return lock, nil
}

// peers reads what a package peers on from the registry, through the
// release's fetcher; a deployment that fetches nothing, or a registry that
// does not answer, leaves the peers unknown rather than failing the publish.
func (r *run) peers() pkgmap.Peers {
	if r.fetch == nil {
		return nil
	}
	return lenientPeers{pkgmap.Registry{Fetch: r.fetch.Fetch}}
}

// lenientPeers is a registry whose failure leaves a package's peers unknown.
type lenientPeers struct{ inner pkgmap.Peers }

// PeersOf is the registry's answer, nil when it has none.
func (l lenientPeers) PeersOf(ctx context.Context, pkg, version string) ([]string, error) {
	peers, err := l.inner.PeersOf(ctx, pkg, version)
	if err != nil {
		return nil, nil //nolint:nilerr // unknown peers are not judged
	}
	return peers, nil
}
