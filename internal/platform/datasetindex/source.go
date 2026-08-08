package datasetindex

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Lister is the catalog capability the Source needs: a paged relevance search
// it can drive as an enumeration. It matches semantic.Provider; declared
// locally so the Source depends on the capability and tests can supply a fake.
type Lister interface {
	SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
}

// listQuery enumerates rather than searches: DataHub's search treats "*" as
// "every entity", the same query the context-document browse (#695) pages
// through, so results come back in the catalog's own order with no relevance
// threshold applied.
const listQuery = "*"

// pageSize is how many datasets one enumeration round trip asks for: large
// enough to keep a few-thousand-dataset catalog to tens of calls, small enough
// that one response stays a reasonable payload. It is a request, not a
// guarantee — a lister may serve fewer, and the DataHub client clamps every
// search to its own MaxLimit, 100 by default, which is what makes one sweep of
// the default 5000-entry cap up to 50 round trips — so enumerate pages by what
// came back rather than by this.
const pageSize = 200

// Source implements indexjobs.Source for the catalog-datasets kind. The unit is
// the whole corpus, so LoadItems both enumerates the catalog and materializes
// the mirror the ranking reads; see the package doc for why the corpus is one
// unit rather than one unit per dataset.
type Source struct {
	store      *Store
	lister     Lister
	maxEntries int
}

// NewSource returns a Source over the mirror store and the catalog lister.
// maxEntries caps how many datasets are mirrored.
func NewSource(store *Store, lister Lister, maxEntries int) *Source {
	return &Source{store: store, lister: lister, maxEntries: maxEntries}
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

// enumerate pages the catalog until it is exhausted or the entry cap is
// reached. Datasets are de-duplicated by URN because paging a live catalog can
// repeat an entry when the underlying set shifts between calls, and a repeated
// item id would make the mirrored count disagree with the item count.
//
// Paging advances by how many rows came back, never by how many were asked for,
// and only an empty page ends the enumeration. A lister is free to return fewer
// rows than requested — the DataHub client clamps every search to its own
// MaxLimit, which is below pageSize by default — so treating a short page as the
// end of the corpus truncates it at the first response (#1231). Advancing by the
// returned length is correct whatever any layer below clamps to.
//
// A page that carries no indexable URN the corpus does not already hold ends the
// enumeration as an error, not as a result: a lister that ignores Offset would
// otherwise return the same rows forever, and stopping successfully would hand
// LoadItems a corpus that is short through no decision of its own — which the
// Sink's atomic replace would then prune the rest of the mirror down to. Failing
// keeps the previous sweep's mirror intact and leaves the unit for the next
// reconciler sweep, which is the fail-closed contract LoadItems documents.
//
// Reaching the cap is a decision rather than a surprise, so it returns what it
// has and logs: coverage is reported against the mirror, so a truncated corpus
// would otherwise read as full coverage on the Indexing dashboard. The log fires
// whenever the cap stopped the paging, including the boundary case where the
// catalog holds exactly max_entries datasets and nothing was actually dropped —
// asking the catalog for one more page to tell those apart would cost a round
// trip on every sweep of every capped deployment.
func (s *Source) enumerate(ctx context.Context) ([]Entry, error) {
	var (
		out  []Entry
		seen = make(map[string]bool)
	)
	for offset := 0; offset < s.maxEntries; {
		results, err := s.lister.SearchTables(ctx, semantic.SearchFilter{
			Query:  listQuery,
			Limit:  min(pageSize, s.maxEntries-offset),
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("catalogSource: enumerate catalog at offset %d: %w", offset, err)
		}
		if len(results) == 0 {
			return out, nil
		}
		before := len(out)
		out = appendUnseen(out, seen, results)
		if len(out) == before {
			return nil, fmt.Errorf(
				"catalogSource: enumerate stalled at offset %d: a page of %d rows held no dataset that was not already indexed, so the catalog is repeating rows or returning them without a URN (indexed %d)",
				offset, len(results), len(out))
		}
		offset += len(results)
	}
	slog.Warn("catalog index: enumeration stopped: entry cap reached; any catalog datasets beyond it are not indexed",
		"max_entries", s.maxEntries, "indexed", len(out))
	return out, nil
}

// appendUnseen appends an entry for every result carrying a URN not already in
// seen, marking those URNs seen, and returns the extended slice. An unchanged
// length means the page held nothing the corpus does not already have, which is
// what ends the enumeration.
func appendUnseen(out []Entry, seen map[string]bool, results []semantic.TableSearchResult) []Entry {
	for i := range results {
		if results[i].URN == "" || seen[results[i].URN] {
			continue
		}
		seen[results[i].URN] = true
		out = append(out, Entry{
			URN:         results[i].URN,
			Name:        results[i].Name,
			Description: results[i].Description,
			Tags:        results[i].Tags,
			Domain:      results[i].Domain,
		})
	}
	return out
}

// OnSucceeded is a no-op: the ranked search reads catalog_datasets directly on
// every query, so there is no in-memory cache to refresh after a sweep.
func (*Source) OnSucceeded(string) {}
