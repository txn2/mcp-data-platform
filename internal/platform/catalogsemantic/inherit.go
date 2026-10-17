package catalogsemantic

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Lineage inheritance (#2130): a column with no description or no glossary
// terms of its own shows the same-named column's from the nearest upstream
// dataset that has them, along the catalog's own lineage, and says where
// they came from. Tags are not inherited: a tag carries sensitivity (pii),
// and a column's sensitivity is its own.

// inheritMaxHops is how far upstream a column's curation is looked for.
const inheritMaxHops = 2

// matchNameExact is the match a column inherited by name records. The
// catalog's lineage is between datasets, so a name match is the only one.
const matchNameExact = "name_exact"

// Reach reports whether the call in ctx may see a dataset reference. An
// upstream dataset it does not reach lends nothing.
type Reach func(ctx context.Context, reference string) bool

// Option configures a Provider.
type Option func(*Provider)

// WithInheritance turns lineage inheritance on. A nil reach lends from every
// upstream dataset.
func WithInheritance(reach Reach) Option {
	return func(p *Provider) {
		if reach == nil {
			reach = func(context.Context, string) bool { return true }
		}
		p.reach = reach
	}
}

// missing is what a column lacks that it could inherit.
type missing struct {
	description bool
	terms       bool
}

// inheritedColumn is what a column borrows, and from where.
type inheritedColumn struct {
	description string
	terms       []string
	from        *semantic.InheritedMetadata
}

// columnKey is how a column is matched across datasets: by name, ignoring
// case, since engines differ in how they case a column.
func columnKey(name string) string { return strings.ToLower(name) }

// inherited returns, by column key, what each of the dataset's columns that
// lacks curation borrows from upstream. Nil when inheritance is off or
// nothing lacks anything; a lineage read that fails borrows nothing.
func (p *Provider) inherited(ctx context.Context, d *catalog.Dataset) map[string]inheritedColumn {
	if p.reach == nil {
		return nil
	}
	need := make(map[string]missing)
	for _, c := range d.Columns {
		m := missing{description: c.Aspects.Description == "", terms: len(c.Aspects.GlossaryTerms) == 0}
		if m.description || m.terms {
			need[columnKey(c.Name)] = m
		}
	}
	if len(need) == 0 {
		return nil
	}
	hops, err := p.store.Lineage(ctx, d.Reference, catalog.Upstream, inheritMaxHops)
	if err != nil {
		slog.WarnContext(ctx, "catalog lineage inheritance: reading lineage failed", //nolint:gosec // G706: values sanitized via logsan
			"dataset", logsan.SanitizeForLog(d.Reference), "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	out := make(map[string]inheritedColumn)
	for _, src := range upstreamSources(hops, d.Reference) {
		if len(need) == 0 {
			break
		}
		if !p.reach(ctx, src.reference) {
			continue
		}
		p.borrowFrom(ctx, src, need, out)
	}
	return out
}

// borrowFrom takes, from one upstream dataset, the curation of each column
// that still needs what that dataset's same-named column has.
func (p *Provider) borrowFrom(ctx context.Context, src upstream, need map[string]missing, out map[string]inheritedColumn) {
	ref, err := catalog.ParseRef(src.reference)
	if err != nil {
		return
	}
	up, err := p.store.GetDataset(ctx, ref)
	if err != nil {
		return
	}
	for _, uc := range up.Columns {
		key := columnKey(uc.Name)
		m, ok := need[key]
		if !ok {
			continue
		}
		if in, lends := lend(m, uc, src); lends {
			out[key] = in
			delete(need, key)
		}
	}
}

// lend is what an upstream column gives a column that lacks m, and whether
// it has any of it to give.
func lend(m missing, uc catalog.Column, src upstream) (inheritedColumn, bool) {
	in := inheritedColumn{from: &semantic.InheritedMetadata{
		SourceURN: src.reference, SourceColumn: uc.Name, Hops: src.depth, MatchMethod: matchNameExact,
	}}
	if m.description {
		in.description = uc.Aspects.Description
	}
	if m.terms {
		in.terms = uc.Aspects.GlossaryTerms
	}
	return in, in.description != "" || len(in.terms) > 0
}

// upstream is a dataset a walk reached, at its shortest distance.
type upstream struct {
	reference string
	depth     int
}

// upstreamSources lists the datasets an upstream walk reached, nearest first
// and then by reference, each once and never the starting dataset.
func upstreamSources(hops []catalog.Hop, start string) []upstream {
	nearest := make(map[string]int)
	for _, h := range hops {
		if h.Source == start {
			continue
		}
		if d, seen := nearest[h.Source]; !seen || h.Depth < d {
			nearest[h.Source] = h.Depth
		}
	}
	out := make([]upstream, 0, len(nearest))
	for ref, depth := range nearest {
		out = append(out, upstream{reference: ref, depth: depth})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].depth != out[j].depth {
			return out[i].depth < out[j].depth
		}
		return out[i].reference < out[j].reference
	})
	return out
}
