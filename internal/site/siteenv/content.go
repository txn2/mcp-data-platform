package siteenv

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// The files a release keeps as rendered text rather than an object.
const (
	redirectsFile = "_redirects"
	headersFile   = "_headers"
	renderedType  = "text/plain; charset=utf-8"
)

// FileBytes is where one site file's bytes are, for the content route the
// portal's viewer reads (#2629): the object to read by range, or, for the
// _redirects and _headers files, the text they are rendered as.
type FileBytes struct {
	Release     int
	Path        string
	ContentType string
	Key         string
	Size        int64
	// Text is a rendered file's body, in place of an object.
	Text []byte
}

// ContentOf is one file of dev, or of release when it is not 0, for the
// viewer. Without a release it reads what the Files tab reads: the source for
// the people who manage the site, what dev serves for someone dev is shared
// with. A release's file is read by the people who manage the site, and by
// anyone else only while an environment they can open serves that release. A
// path outside the tree is refused, and so is a pruned release.
func (o Ops) ContentOf(ctx context.Context, actor sitesvc.Actor, siteRef, path string, release int) (*FileBytes, error) {
	name, err := sitesvc.CleanPath(path)
	if err != nil {
		return nil, refusedBy(err)
	}
	read, err := o.filesOf(ctx, actor, siteRef, name, release)
	if err != nil {
		return nil, err
	}
	st, rel, files := read.st, read.rel, read.files
	switch name {
	case redirectsFile:
		return &FileBytes{Release: rel.Number, Path: name, ContentType: renderedType, Text: []byte(site.RenderRedirects(rel.Redirects))}, nil
	case headersFile:
		return &FileBytes{Release: rel.Number, Path: name, ContentType: renderedType, Text: []byte(site.RenderHeaders(rel.Headers))}, nil
	}
	for _, f := range files {
		if f.Path == name {
			return &FileBytes{Release: rel.Number, Path: name, ContentType: f.ContentType, Key: sitesvc.ObjectKey(st.ID, f, ""), Size: f.Size}, nil
		}
	}
	return nil, refused("release %d has no file %q", rel.Number, name)
}

// filesOf is the site, the release and the files a read of name may look
// at: dev's tree through the service's own reading rule, or a release's.
func (o Ops) filesOf(ctx context.Context, actor sitesvc.Actor, siteRef, name string, release int) (*fileRead, error) {
	st, err := o.Find(ctx, siteRef)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if release != 0 {
		rel, files, err := o.releaseFiles(ctx, actor, st, release)
		return &fileRead{st: st, rel: rel, files: files}, err
	}
	listing, err := o.Tree(ctx, actor, site.Ref{Site: siteRef, Environment: site.EnvDev, Path: name}, 0, sitesvc.DefaultTreeLimit)
	if err != nil {
		return nil, err //nolint:wrapcheck // the service's refusal
	}
	rel, err := o.Store.Release(ctx, st.ID, listing.Release)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return &fileRead{st: st, rel: rel, files: listing.Files}, nil
}

// fileRead is what a read of one file looks at.
type fileRead struct {
	st    *site.Site
	rel   *site.Release
	files []site.File
}

// releaseFiles is a release and the tree of it someone may read.
func (o Ops) releaseFiles(ctx context.Context, actor sitesvc.Actor, st *site.Site, number int) (*site.Release, []site.File, error) {
	tree, err := o.releaseTree(ctx, actor, st, number)
	if err != nil {
		return nil, nil, err
	}
	rel, err := o.Store.Release(ctx, st.ID, number)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	if rel.Pruned {
		return nil, nil, refused("release %d was removed by the release limit, so its files can no longer be read", number)
	}
	if tree == site.TreeSource && !rel.Rendered {
		tree = site.TreeOutput
	}
	files, err := o.Store.Tree(ctx, rel.ID, tree)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	return rel, files, nil
}

// releaseTree is the tree of a release someone may read: the source for the
// people who manage the site, what is served while an environment they can
// open serves it, and nothing otherwise.
func (o Ops) releaseTree(ctx context.Context, actor sitesvc.Actor, st *site.Site, number int) (string, error) {
	perms, err := o.PermissionsOf(ctx, actor, st)
	if err != nil {
		return "", err //nolint:wrapcheck // read by identity
	}
	if perms.Manage {
		return site.TreeSource, nil
	}
	envs, err := o.OpenableEnvironments(ctx, actor, st)
	if err != nil {
		return "", err //nolint:wrapcheck // read by identity
	}
	for _, e := range envs {
		if e.Release == number {
			return site.TreeOutput, nil
		}
	}
	return "", fmt.Errorf("site %q is read by the people who can open it: %w", st.Name, site.ErrNotAllowed)
}
