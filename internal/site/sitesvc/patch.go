package sitesvc

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// PatchRequest is an anchored edit of one file.
type PatchRequest struct {
	Ref site.Ref
	// Base is the release the edit was made against, 0 for whichever the
	// environment serves now.
	Base    int
	Edits   []textpatch.Edit
	Summary string
	DryRun  bool
}

// Patched is what a patch did.
type Patched struct {
	Result      textpatch.Result
	Path        string
	ContentType string
	Release     int
	Address     string
	// Unchanged is true when the edits left the file as it was, so no release
	// was written.
	Unchanged bool
	// OutputsChanged is how many served files a dry run would change.
	OutputsChanged int
}

// PatchFile applies anchored edits to one file and writes the result as a new
// release derived from the current one, moving the environment to it. It is the
// one way an edit reaches a site: nothing is written unless every edit resolves.
func (s *Service) PatchFile(ctx context.Context, actor Actor, req PatchRequest) (*Patched, error) {
	st, err := s.Find(ctx, req.Ref.Site)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	name, err := filePath(req.Ref)
	if err != nil {
		return nil, err
	}
	tree, err := s.patchTarget(ctx, st, req)
	if err != nil {
		return nil, err
	}
	body, contentType, err := s.textOf(ctx, st.ID, tree.rel, tree.byPath, name)
	if err != nil {
		return nil, err
	}
	res, err := textpatch.Apply(body, req.Edits, textpatch.Options{
		Syntax: textpatch.SyntaxForContentType(contentType), MaxResultBytes: int(s.cfg().Limits().MaxFileBytes),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller reads the edit error's code
	}
	pub, err := s.Publish(ctx, actor, PublishRequest{
		Site: req.Ref.Site, Environment: tree.env.Name, Source: "patch " + name, Note: req.Summary, DryRun: req.DryRun,
		Base: tree.rel.Number, Files: []InputFile{{Path: name, Body: []byte(res.Body)}},
	})
	if err != nil {
		return nil, err
	}
	return &Patched{
		Result: res, Path: name, ContentType: contentType, Release: pub.Release.Number, Address: pub.Address,
		Unchanged: pub.Unchanged, OutputsChanged: pub.OutputsChanged,
	}, nil
}

// patchTarget is the tree an edit applies to, or why it cannot: only the working
// environment takes edits, and only at the release the editor read.
func (s *Service) patchTarget(ctx context.Context, st *site.Site, req PatchRequest) (*sourceTree, error) {
	env, err := s.publishTarget(st, PublishRequest{Environment: req.Ref.Environment, Note: req.Summary})
	if err != nil {
		return nil, err
	}
	if req.Base != 0 && req.Base != env.Release {
		return nil, textpatch.StaleBaseError(req.Base, env.Release)
	}
	return s.treeOf(ctx, st, env.Name, site.TreeSource)
}

// Diffed compares two releases: a unified diff of one file, or the paths that
// differ across a whole tree.
type Diffed struct {
	From, To   int
	Diff       string
	Comparison *Comparison
}

// Diff compares two releases of a file or a tree. Earlier releases can hold what
// a later one took out, so it is for the people who manage the site. to defaults
// to the environment's release and from to the release to was derived from.
func (s *Service) Diff(ctx context.Context, actor Actor, ref site.Ref, from, to int) (*Diffed, error) {
	st, err := s.Find(ctx, ref.Site)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	target, err := s.diffTarget(ctx, st, ref.Environment, to)
	if err != nil {
		return nil, err
	}
	if from == 0 {
		from = cmp.Or(target.Base, target.Number-1)
	}
	if from < 1 {
		return nil, refused("release %d has no earlier release to compare with", target.Number)
	}
	if ref.IsFile() {
		return s.diffFile(ctx, st, ref, from, target)
	}
	c, err := s.compareTree(ctx, st, comparePair{from, target.Number}, site.TreeSource)
	if err != nil {
		return nil, err
	}
	if ref.Path != "" {
		c.Changed, c.Added, c.Removed = inFolder(c.Changed, ref.Path), inFolder(c.Added, ref.Path), inFolder(c.Removed, ref.Path)
	}
	return &Diffed{From: from, To: target.Number, Comparison: c}, nil
}

// diffTarget is the release a diff compares to: the numbered one, or what the
// environment serves.
func (s *Service) diffTarget(ctx context.Context, st *site.Site, environment string, to int) (*site.Release, error) {
	tree, err := s.treeOf(ctx, st, environment, site.TreeSource)
	if err != nil {
		return nil, err
	}
	if to == 0 || to == tree.rel.Number {
		return tree.rel, nil
	}
	return s.comparable(ctx, st, to)
}

func inFolder(paths []string, prefix string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Service) diffFile(ctx context.Context, st *site.Site, ref site.Ref, from int, target *site.Release) (*Diffed, error) {
	name, err := filePath(ref)
	if err != nil {
		return nil, err
	}
	earlier, err := s.comparable(ctx, st, from)
	if err != nil {
		return nil, err
	}
	before, beforeOK, err := s.textIfPresent(ctx, st.ID, earlier, name)
	if err != nil {
		return nil, err
	}
	after, afterOK, err := s.textIfPresent(ctx, st.ID, target, name)
	if err != nil {
		return nil, err
	}
	if !beforeOK && !afterOK {
		return nil, refused("neither release %d nor %d has a file %q", from, target.Number, name)
	}
	return &Diffed{
		From: from, To: target.Number,
		Diff: textpatch.UnifiedDiffLabeled(before, after, fmt.Sprintf("r%d", from), fmt.Sprintf("r%d", target.Number), 0),
	}, nil
}

// textIfPresent is a file of a release, with false when the release has none.
func (s *Service) textIfPresent(ctx context.Context, siteID string, rel *site.Release, name string) (body string, present bool, err error) {
	files, err := s.treeFiles(ctx, rel, site.TreeSource)
	if err != nil {
		return "", false, err
	}
	index := pathIndex(files)
	if _, ok := index[name]; !ok && name != redirectsFile {
		return "", false, nil
	}
	body, _, err = s.textOf(ctx, siteID, rel, index, name)
	return body, err == nil, err
}
