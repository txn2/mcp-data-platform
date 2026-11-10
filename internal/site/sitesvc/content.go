package sitesvc

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// redirectsType is what the _redirects file reads as.
const redirectsType = "text/plain; charset=utf-8"

// maxSearchBytes bounds a file a search across a tree reads: a tree can hold
// 20,000 files of 64 MiB, and anyone who can open the site can search it. A file
// past it is counted in files_skipped.
const maxSearchBytes = 16 << 20

// sourceTree is one environment's current release, read for the content
// commands.
type sourceTree struct {
	st     *site.Site
	env    *site.Environment
	rel    *site.Release
	files  []site.File
	byPath map[string]site.File
}

// readableTree is the tree someone may read: the source for people who manage
// the site, what is served for anyone else who can open it, and nothing for
// those who cannot. An email the provider has not verified matches no one.
func (s *Service) readableTree(ctx context.Context, actor Actor, st *site.Site) (string, error) {
	if actor.Admin || isOwner(actor, st) {
		return site.TreeSource, nil
	}
	tree, err := s.grantedTree(ctx, actor, st)
	if err != nil || tree != "" {
		return tree, err
	}
	return "", fmt.Errorf("site %q is read by the people who can open it: %w", st.Name, site.ErrNotAllowed)
}

// grantedTree is what someone other than the owner reads, "" for nothing.
func (s *Service) grantedTree(ctx context.Context, actor Actor, st *site.Site) (string, error) {
	if actor.Email == "" || actor.EmailUnverified {
		return "", nil
	}
	level, err := s.Store.GrantLevel(ctx, st.ID, site.NormalizeEmail(actor.Email))
	if err != nil {
		return "", err //nolint:wrapcheck // read by identity
	}
	switch {
	case level == site.LevelManage:
		return site.TreeSource, nil
	case level != "" || (st.InternalView && s.External != nil && !s.External(ctx, actor.Email)):
		return site.TreeOutput, nil
	}
	return "", nil
}

// treeFiles is one tree of a release. A release with no source tree of its own
// was published as it is served, so its files are its source.
func (s *Service) treeFiles(ctx context.Context, rel *site.Release, tree string) ([]site.File, error) {
	if tree == site.TreeSource && !rel.Rendered {
		tree = site.TreeOutput
	}
	files, err := s.Store.Tree(ctx, rel.ID, tree)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return files, nil
}

// treeOf reads an environment's tree. Access is decided before anything about
// the environment or its files is looked at, so a refusal says nothing about
// what the site holds.
func (s *Service) treeOf(ctx context.Context, st *site.Site, environment, tree string) (*sourceTree, error) {
	if s.Objects == nil || s.Bucket == "" {
		return nil, refused("this deployment serves no sites: it has no portal bucket, or sites.enabled is false")
	}
	env := st.Environment(environment)
	if env == nil {
		return nil, noEnvironment(environment)
	}
	if env.Release == 0 {
		return nil, refused("environment %q serves no release yet", environment)
	}
	rel, err := s.Store.Release(ctx, st.ID, env.Release)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	files, err := s.treeFiles(ctx, rel, tree)
	if err != nil {
		return nil, err
	}
	return &sourceTree{st: st, env: env, rel: rel, files: files, byPath: pathIndex(files)}, nil
}

// openTree is treeOf for what someone may read.
func (s *Service) openTree(ctx context.Context, actor Actor, ref site.Ref) (*sourceTree, error) {
	st, err := s.Find(ctx, ref.Site)
	if err != nil {
		return nil, err
	}
	tree, err := s.readableTree(ctx, actor, st)
	if err != nil {
		return nil, err
	}
	return s.treeOf(ctx, st, ref.Environment, tree)
}

func pathIndex(files []site.File) map[string]site.File {
	index := make(map[string]site.File, len(files))
	for _, f := range files {
		index[f.Path] = f
	}
	return index
}

func filePath(ref site.Ref) (string, error) {
	if !ref.IsFile() {
		return "", refused("this reference names a tree or a folder; name one file, mcp:site:%s/%s/<path>", ref.Site, ref.Environment)
	}
	name, err := CleanPath(ref.Path)
	if err != nil {
		return "", refused("%s", err.Error())
	}
	return name, nil
}

// textOf is a file of a release as text, the _redirects file rendered from the
// redirect map it became.
func (s *Service) textOf(ctx context.Context, siteID string, rel *site.Release, files map[string]site.File, name string) (body, contentType string, err error) {
	if name == redirectsFile {
		return site.RenderRedirects(rel.Redirects), redirectsType, nil
	}
	f, ok := files[name]
	if !ok {
		return "", "", refused("release %d has no file %q", rel.Number, name)
	}
	if !contenttype.IsTextual(f.ContentType) {
		return "", "", textpatch.NotTextError(f.ContentType)
	}
	data, _, err := s.Objects.GetObject(ctx, s.Bucket, ObjectKey(siteID, f, ""))
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", name, err)
	}
	return string(data), f.ContentType, nil
}

// FileText is one file as an environment serves it.
type FileText struct {
	Environment string
	Release     int
	Path        string
	ContentType string
	Body        string
}

// ReadFile is one text file of what an environment serves.
func (s *Service) ReadFile(ctx context.Context, actor Actor, ref site.Ref) (*FileText, error) {
	tree, err := s.openTree(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	name, err := filePath(ref)
	if err != nil {
		return nil, err
	}
	body, contentType, err := s.textOf(ctx, tree.st.ID, tree.rel, tree.byPath, name)
	if err != nil {
		return nil, err
	}
	return &FileText{Environment: tree.env.Name, Release: tree.rel.Number, Path: name, ContentType: contentType, Body: body}, nil
}

// TreeListing is the files under a reference, one page of them.
type TreeListing struct {
	Environment string      `json:"environment"`
	Release     int         `json:"version"`
	Files       []site.File `json:"files"`
	Total       int         `json:"total"`
	Truncated   bool        `json:"truncated"`
}

// DefaultTreeLimit is how many files a listing returns unless asked for fewer.
const DefaultTreeLimit = 1000

// Tree lists the files a tree or folder reference names, including the
// _redirects file when the release has a redirect map.
func (s *Service) Tree(ctx context.Context, actor Actor, ref site.Ref, offset, limit int) (*TreeListing, error) {
	tree, err := s.openTree(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	matched := under(tree, ref.Path)
	if limit <= 0 || limit > DefaultTreeLimit {
		limit = DefaultTreeLimit
	}
	offset = min(max(offset, 0), len(matched))
	end := min(offset+limit, len(matched))
	return &TreeListing{
		Environment: tree.env.Name, Release: tree.rel.Number, Files: matched[offset:end], Total: len(matched), Truncated: end < len(matched),
	}, nil
}

// under is the tree's files beneath a folder prefix, the whole tree for "".
func under(tree *sourceTree, prefix string) []site.File {
	out := make([]site.File, 0, len(tree.files))
	if len(tree.rel.Redirects) > 0 && strings.HasPrefix(redirectsFile, prefix) {
		text := site.RenderRedirects(tree.rel.Redirects)
		out = append(out, site.File{Path: redirectsFile, ContentType: redirectsType, Size: int64(len(text))})
	}
	for _, f := range tree.files {
		if strings.HasPrefix(f.Path, prefix) {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b site.File) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// FileMatches is what a search found in one file.
type FileMatches struct {
	Path      string            `json:"path"`
	Count     int               `json:"count"`
	Matches   []textpatch.Match `json:"matches"`
	Truncated bool              `json:"truncated,omitempty"`
}

// TreeMatches is a search across a tree. FilesSkipped counts what was not
// searched, so a search that could not reach a file does not read as a clean
// result.
type TreeMatches struct {
	Environment  string        `json:"environment"`
	Release      int           `json:"version"`
	Files        []FileMatches `json:"files"`
	Count        int           `json:"count"`
	FilesScanned int           `json:"files_scanned"`
	FilesSkipped int           `json:"files_skipped"`
	Truncated    bool          `json:"truncated"`
}

// LocateTree searches every text file a tree or folder reference names. It
// takes a literal or a pattern: a section or selector means nothing across
// files of different kinds.
func (s *Service) LocateTree(ctx context.Context, actor Actor, ref site.Ref, q textpatch.LocateQuery) (*TreeMatches, error) {
	if q.Section != "" || q.Selector != "" {
		return nil, refused("locate across a tree takes find or pattern; section and selector need one file")
	}
	tree, err := s.openTree(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	if _, err := textpatch.Locate("", q, textpatch.Options{}); err != nil {
		return nil, err //nolint:wrapcheck // the caller reads the edit error's code
	}
	files := under(tree, ref.Path)
	found := make([]*FileMatches, len(files))
	var scanned, skipped atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallelUploads)
	for i, f := range files {
		g.Go(func() error {
			match, read, err := s.searchFile(gctx, tree, f, q)
			if err != nil {
				return err
			}
			if !read {
				skipped.Add(1)
				return nil
			}
			scanned.Add(1)
			found[i] = match
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err //nolint:wrapcheck // says which file
	}
	return collectMatches(tree, found, q.Limit, int(scanned.Load()), int(skipped.Load())), nil
}

// searchFile searches one file. read is false for one it did not reach: not
// text, past the bound, or unreadable, none of which should lose the rest.
func (s *Service) searchFile(ctx context.Context, tree *sourceTree, f site.File, q textpatch.LocateQuery) (match *FileMatches, read bool, err error) {
	if !contenttype.IsTextual(f.ContentType) || f.Size > maxSearchBytes {
		return nil, false, nil
	}
	body, _, err := s.textOf(ctx, tree.st.ID, tree.rel, tree.byPath, f.Path)
	if err != nil {
		return nil, false, nil //nolint:nilerr // reported in files_skipped
	}
	res, err := textpatch.Locate(body, q, textpatch.Options{Syntax: textpatch.SyntaxForContentType(f.ContentType)})
	if err != nil {
		return nil, false, fmt.Errorf("searching %s: %w", f.Path, err)
	}
	if res.Count == 0 {
		return nil, true, nil
	}
	return &FileMatches{Path: f.Path, Count: res.Count, Matches: res.Matches, Truncated: res.Truncated}, true, nil
}

func collectMatches(tree *sourceTree, found []*FileMatches, limit, scanned, skipped int) *TreeMatches {
	if limit <= 0 {
		limit = textpatch.DefaultLocateLimit
	}
	out := &TreeMatches{Environment: tree.env.Name, Release: tree.rel.Number, Files: []FileMatches{}, FilesScanned: scanned, FilesSkipped: skipped}
	reported := 0
	for _, f := range found {
		if f == nil {
			continue
		}
		out.Count += f.Count
		out.Truncated = out.Truncated || f.Truncated
		if reported >= limit {
			out.Truncated = true
			continue
		}
		if room := limit - reported; len(f.Matches) > room {
			f.Matches, f.Truncated, out.Truncated = f.Matches[:room], true, true
		}
		reported += len(f.Matches)
		out.Files = append(out.Files, *f)
	}
	return out
}
