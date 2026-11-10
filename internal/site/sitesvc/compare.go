package sitesvc

import (
	"context"
	"slices"

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
	return s.compareTree(ctx, st.ID, comparePair{from, to}, site.TreeOutput)
}

// comparePair is two release numbers, the first 0 for no release yet.
type comparePair struct{ from, to int }

func (s *Service) compareTree(ctx context.Context, siteID string, pair comparePair, tree string) (*Comparison, error) {
	from, to := pair.from, pair.to
	target, err := s.Store.Release(ctx, siteID, to)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	targetFiles, err := s.treeFiles(ctx, target, tree)
	if err != nil {
		return nil, err
	}
	base := &site.Release{Redirects: []site.Redirect{}}
	baseFiles := []site.File{}
	if from != 0 {
		if base, err = s.Store.Release(ctx, siteID, from); err != nil {
			return nil, err //nolint:wrapcheck // read by identity
		}
		if baseFiles, err = s.treeFiles(ctx, base, tree); err != nil {
			return nil, err
		}
	}
	out := compareFiles(baseFiles, targetFiles)
	out.From, out.To = from, to
	out.RedirectsAdded = missingFrom(target.Redirects, base.Redirects)
	out.RedirectsRemoved = missingFrom(base.Redirects, target.Redirects)
	return out, nil
}

func compareFiles(base, target []site.File) *Comparison {
	out := &Comparison{Changed: []string{}, Added: []string{}, Removed: []string{}}
	before := make(map[string]string, len(base))
	for _, f := range base {
		before[f.Path] = f.SHA256
	}
	for _, f := range target {
		digest, ok := before[f.Path]
		switch {
		case !ok:
			out.Added = append(out.Added, f.Path)
		case digest != f.SHA256:
			out.Changed = append(out.Changed, f.Path)
		}
		delete(before, f.Path)
	}
	for p := range before {
		out.Removed = append(out.Removed, p)
	}
	slices.Sort(out.Changed)
	slices.Sort(out.Added)
	slices.Sort(out.Removed)
	return out
}

// missingFrom is the rules of a that b does not hold.
func missingFrom(a, b []site.Redirect) []site.Redirect {
	out := []site.Redirect{}
	for _, r := range a {
		if !slices.Contains(b, r) {
			out = append(out, r)
		}
	}
	return out
}
