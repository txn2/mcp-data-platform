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

// pageSize is how many datasets one enumeration round trip asks for. Large
// enough that a few-thousand-dataset catalog is a handful of calls, small
// enough that one response stays a reasonable payload.
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
// Reaching the cap is logged rather than passed over: coverage is reported
// against the mirror, so a truncated corpus would otherwise read as full
// coverage on the Indexing dashboard. The log fires whenever the cap stopped
// the paging, including the boundary case where the catalog holds exactly
// max_entries datasets and nothing was actually dropped — asking the catalog
// for one more page to tell those apart would cost a round trip on every sweep
// of every capped deployment.
func (s *Source) enumerate(ctx context.Context) ([]Entry, error) {
	var (
		out  []Entry
		seen = make(map[string]bool)
	)
	for offset := 0; offset < s.maxEntries; offset += pageSize {
		limit := min(pageSize, s.maxEntries-offset)
		results, err := s.lister.SearchTables(ctx, semantic.SearchFilter{
			Query:  listQuery,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("catalogSource: enumerate catalog at offset %d: %w", offset, err)
		}
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
		if len(results) < limit {
			return out, nil
		}
	}
	slog.Warn("catalog index: entry cap reached; any catalog datasets beyond it are not indexed",
		"max_entries", s.maxEntries, "indexed", len(out))
	return out, nil
}

// OnSucceeded is a no-op: the ranked search reads catalog_datasets directly on
// every query, so there is no in-memory cache to refresh after a sweep.
func (*Source) OnSucceeded(string) {}
