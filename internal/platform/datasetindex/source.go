package datasetindex

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Lister is the catalog capability the Source needs: a paged relevance search
// it can drive as an enumeration. It matches semantic.Provider; declared
// locally so the Source depends on the capability and tests can supply a fake.
type Lister interface {
	SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
}

// listQuery enumerates rather than searches: the catalog's search treats "*"
// as every dataset, so results come back in the catalog's own order with no
// relevance threshold applied.
const listQuery = "*"

// pageSize is how many datasets one enumeration round trip asks for: large
// enough to keep a few-thousand-dataset catalog to tens of calls, small enough
// that one response stays a reasonable payload. It is a request, not a
// guarantee: a lister may serve fewer, so enumerate pages by what came back
// rather than by this.
const pageSize = 200

// Source implements indexjobs.Source for the catalog-datasets kind. The unit is
// the whole corpus, so LoadItems both enumerates the catalog and materializes
// the mirror the ranking reads; see the package doc for why the corpus is one
// unit rather than one unit per dataset.
type Source struct {
	store      *Store
	lister     Lister
	maxEntries int
	excluded   []Connection
}

// NewSource returns a Source over the mirror store and the catalog lister.
// maxEntries caps how many datasets are mirrored.
func NewSource(store *Store, lister Lister, maxEntries int) *Source {
	return &Source{store: store, lister: lister, maxEntries: maxEntries}
}

// Excluding returns the source with the datasets of conns left out of the
// mirror (#2381).
func (s *Source) Excluding(conns ...Connection) *Source {
	s.excluded = append(s.excluded, conns...)
	return s
}

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the catalog-datasets source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems enumerates the catalog, writes the result into the mirror, stamps
// the sweep, and returns one embeddable item per dataset.
//
// It never reports a partial corpus: any enumeration error fails the job, so
// the atomic replace that follows a successful pass cannot prune datasets that
// merely went unread. A catalog that legitimately holds nothing yields an empty
// item set, which the framework treats as a clean completion — and the Sink's
// replace then clears a mirror left over from a catalog that has been emptied.
func (s *Source) LoadItems(ctx context.Context, _ string) ([]indexjobs.Item, error) {
	entries, err := s.enumerate(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.store.Sync(ctx, entries); err != nil {
		return nil, fmt.Errorf("catalogSource: sync mirror: %w", err)
	}
	if err := s.store.StampSync(ctx); err != nil {
		return nil, fmt.Errorf("catalogSource: stamp sync: %w", err)
	}
	items := make([]indexjobs.Item, 0, len(entries))
	for _, e := range entries {
		items = append(items, indexjobs.Item{ItemID: e.URN, Text: IndexText(e)})
	}
	return items, nil
}

// enumerate reads the catalog one kind at a time, in IndexedKinds order, until
// every kind is exhausted or the entry cap is reached, so the cap keeps the
// kinds listed first (#2381). Datasets are de-duplicated by URN because paging
// a live catalog can repeat an entry when the underlying set shifts between
// calls, and a repeated item id would make the mirrored count disagree with
// the item count.
//
// Reaching the cap is a decision rather than a surprise, so it returns what it
// has and logs. The Sink's coverage is held to the catalog's own total, so a
// truncated corpus reads short on the Indexing page rather than as complete.
// The log fires whenever the cap stopped the paging, including the boundary
// case where the catalog holds exactly max_entries datasets and nothing was
// dropped: telling those apart would cost a round trip on every sweep.
func (s *Source) enumerate(ctx context.Context) ([]Entry, error) {
	var (
		out  []Entry
		seen = make(map[string]bool)
		err  error
	)
	for _, kind := range IndexedKinds {
		if out, err = s.enumerateKind(ctx, kind, out, seen); err != nil {
			return nil, err
		}
		if len(out) >= s.maxEntries {
			slog.WarnContext(ctx, "catalog index: enumeration stopped: entry cap reached; any catalog datasets beyond it are not indexed",
				"max_entries", s.maxEntries, "indexed", s.maxEntries, "stopped_in_kind", kind)
			return out[:s.maxEntries], nil
		}
	}
	return out, nil
}

// enumerateKind pages one connection kind's datasets onto out until the kind is
// exhausted or out reaches the cap.
//
// Paging advances by how many rows came back, never by how many were asked for,
// and only an empty page ends the kind. A lister is free to return fewer rows
// than requested, so treating a short page as the end truncates the corpus at
// the first response (#1231).
//
// A page that carries no URN the enumeration has not already seen ends it as an
// error, not as a result: a lister that ignores Offset would otherwise return
// the same rows forever, and stopping successfully would hand LoadItems a corpus
// that is short through no decision of its own, which the Sink's atomic replace
// would then prune the rest of the mirror down to. An excluded dataset counts as
// seen, so a page made entirely of them is progress, not a stall.
func (s *Source) enumerateKind(ctx context.Context, kind string, out []Entry, seen map[string]bool) ([]Entry, error) {
	for offset := 0; len(out) < s.maxEntries; {
		results, err := s.lister.SearchTables(ctx, semantic.SearchFilter{
			Query: listQuery, Platform: kind, Limit: pageSize, Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("catalogSource: enumerate %s datasets at offset %d: %w", kind, offset, err)
		}
		if len(results) == 0 {
			return out, nil
		}
		var fresh int
		out, fresh = s.appendUnseen(out, seen, results)
		if fresh == 0 {
			return nil, fmt.Errorf(
				"catalogSource: enumerate stalled at %s offset %d: a page of %d rows held no dataset that was not already read, so the catalog is repeating rows or returning them without a URN (indexed %d)",
				kind, offset, len(results), len(out))
		}
		offset += len(results)
	}
	return out, nil
}

// appendUnseen appends an entry for every result carrying a URN not already in
// seen and not on an excluded connection, marks every new URN seen, and returns
// the extended slice with how many URNs were new. Zero new URNs means the page
// held nothing the enumeration had not read, which is what reports a stall.
func (s *Source) appendUnseen(out []Entry, seen map[string]bool, results []semantic.TableSearchResult) (extended []Entry, fresh int) {
	for i := range results {
		if results[i].URN == "" || seen[results[i].URN] {
			continue
		}
		seen[results[i].URN] = true
		fresh++
		if s.isExcluded(results[i].URN) {
			continue
		}
		out = append(out, Entry{
			URN:         results[i].URN,
			Name:        results[i].Name,
			Description: results[i].Description,
			Tags:        results[i].Tags,
			Domain:      results[i].Domain,
		})
	}
	return out, fresh
}

// isExcluded reports whether a dataset reference names a connection the index
// leaves out.
func (s *Source) isExcluded(urn string) bool {
	if len(s.excluded) == 0 {
		return false
	}
	ref, err := catalog.ParseRef(urn)
	if err != nil {
		return false
	}
	return slices.Contains(s.excluded, Connection{Kind: ref.ConnectionKind, Name: ref.Connection})
}

// OnSucceeded is a no-op: the ranked search reads catalog_datasets directly on
// every query, so there is no in-memory cache to refresh after a sweep.
func (*Source) OnSucceeded(string) {}
