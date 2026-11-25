package sitesvc

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Comparison is what moving from one release to another changes, read from
// each release's file digests.
type Comparison struct {
	From             int             `json:"from" example:"9"`
	To               int             `json:"to" example:"11"`
	Changed          []string        `json:"changed"`
	Added            []string        `json:"added"`
	Removed          []string        `json:"removed"`
	RedirectsAdded   []site.Redirect `json:"redirects_added"`
	RedirectsRemoved []site.Redirect `json:"redirects_removed"`
}

// Compare says what serving release to changes for someone served release
// from; from 0 is an environment with no release yet.
func (s *Service) Compare(ctx context.Context, ref string, from, to int) (*Comparison, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s.compareTree(ctx, st, comparePair{from, to}, site.TreeOutput)
}

// comparePair is two release numbers, the first 0 for no release yet.
type comparePair struct{ from, to int }

func (s *Service) compareTree(ctx context.Context, st *site.Site, pair comparePair, tree string) (*Comparison, error) {
	from, to := pair.from, pair.to
	target, err := s.comparable(ctx, st, to)
	if err != nil {
		return nil, err
	}
	targetFiles, err := s.treeFiles(ctx, target, tree)
	if err != nil {
		return nil, err
	}
	base := &site.Release{Redirects: []site.Redirect{}}
	baseFiles := []site.File{}
	if from != 0 {
		if base, err = s.comparable(ctx, st, from); err != nil {
			return nil, err
		}
		if baseFiles, err = s.treeFiles(ctx, base, tree); err != nil {
			return nil, err
		}
	}
	out := compareFiles(baseFiles, targetFiles)
	out.From, out.To = from, to
	out.RedirectsAdded = site.MissingRedirects(target.Redirects, base.Redirects)
	out.RedirectsRemoved = site.MissingRedirects(base.Redirects, target.Redirects)
	return out, nil
}

// comparable reads a release a comparison or a diff reads, refusing a pruned
// one: its own file rows are gone, and its chain would read a kept ancestor's
// files as its own. The refusal names the release alone, since Compare
// answers anyone who can find the site and the site's environments and their
// limits are its managers' to know.
func (s *Service) comparable(ctx context.Context, st *site.Site, number int) (*site.Release, error) {
	rel, err := s.Store.Release(ctx, st.ID, number)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if rel.Pruned {
		return nil, &RefusedError{Err: site.ErrPruned, Msg: fmt.Sprintf("release %d was removed by the release limit, so it cannot be compared", number)}
	}
	return rel, nil
}

func compareFiles(base, target []site.File) *Comparison {
	changed, added, removed := site.DiffFiles(base, target)
	return &Comparison{Changed: changed, Added: added, Removed: removed}
}
