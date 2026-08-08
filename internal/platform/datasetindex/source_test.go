package datasetindex

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// newMockStore returns a Store over sqlmock plus the mock and a cleanup.
func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return NewStore(db), mock, func() { _ = db.Close() }
}

// expectSyncOf sets the mock expectations for a Sync of n entries plus the
// sweep stamp, the write pair every successful LoadItems performs.
func expectSyncOf(mock sqlmock.Sqlmock, n int) {
	mock.ExpectBegin()
	for range n {
		mock.ExpectExec("INSERT INTO catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	mock.ExpectExec("INSERT INTO catalog_dataset_sync").WillReturnResult(sqlmock.NewResult(0, 1))
}

func result(urn, name, desc string) semantic.TableSearchResult {
	return semantic.TableSearchResult{URN: urn, Name: name, Description: desc}
}

func TestLoadItemsMirrorsAndComposesText(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	lister := &stubLister{pages: [][]semantic.TableSearchResult{{
		result("urn:a", "sales.orders", "Refunds are netted at close of month."),
		result("urn:b", "sales.refunds", ""),
	}}}
	expectSyncOf(mock, 2)

	items, err := NewSource(store, lister, 1000).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "urn:a", items[0].ItemID)
	assert.Equal(t, "sales.orders\nRefunds are netted at close of month.", items[0].Text,
		"the embedded text is the description, which is what a topical query has to match")
	assert.Equal(t, "sales.refunds", items[1].Text)
	assert.NoError(t, mock.ExpectationsWereMet())

	// The enumeration asks the catalog to list, not to rank.
	require.NotEmpty(t, lister.filters)
	assert.Equal(t, listQuery, lister.filters[0].Query)
	assert.Equal(t, 0, lister.filters[0].Offset)
}

// corpusLister serves a corpus by offset and clamps every request to clampAt
// rows, which is what the real lister does: the DataHub client clamps a search
// to its own MaxLimit (100 by default), below the pageSize this package asks
// for, so a page never comes back as long as it was requested.
type corpusLister struct {
	corpus  []semantic.TableSearchResult
	clampAt int
	filters []semantic.SearchFilter
}

func (c *corpusLister) SearchTables(_ context.Context, f semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	c.filters = append(c.filters, f)
	if f.Offset >= len(c.corpus) {
		return nil, nil
	}
	return c.corpus[f.Offset:min(f.Offset+min(f.Limit, c.clampAt), len(c.corpus))], nil
}

func corpusOf(n int) []semantic.TableSearchResult {
	out := make([]semantic.TableSearchResult, n)
	for i := range out {
		out[i] = result("urn:"+strconv.Itoa(i), "t", "d")
	}
	return out
}

func TestLoadItemsPagesUntilExhausted(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	// Pages are short of what was asked for, as the real lister's always are.
	lister := &corpusLister{corpus: corpusOf(5), clampAt: 2}
	expectSyncOf(mock, 5)

	items, err := NewSource(store, lister, 10_000).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	assert.Len(t, items, 5)
	require.Len(t, lister.filters, 4, "four calls: three that returned rows, one that found the end")
	assert.Equal(t, []int{0, 2, 4, 5}, []int{
		lister.filters[0].Offset, lister.filters[1].Offset,
		lister.filters[2].Offset, lister.filters[3].Offset,
	}, "each page resumes at the end of what the previous one actually returned")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLoadItemsEnumeratesPastAClampedPageSize is #1231: the DataHub client
// clamps every search to 100 rows while the loop asks for pageSize (200). Paging
// by the requested size read the first short page as the end of the corpus, so
// every deployment indexed exactly 100 datasets and reported full coverage.
func TestLoadItemsEnumeratesPastAClampedPageSize(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	const clamp = 100
	lister := &corpusLister{corpus: corpusOf(250), clampAt: clamp}
	expectSyncOf(mock, 250)

	items, err := NewSource(store, lister, 10_000).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	assert.Len(t, items, 250, "the whole corpus is indexed even though no page is ever as long as requested")
	require.NotEmpty(t, lister.filters)
	assert.Equal(t, pageSize, lister.filters[0].Limit, "the loop still asks for a full page; the lister is what clamps")
	assert.Equal(t, clamp, lister.filters[1].Offset, "the second page resumes at what the first returned, not at what it asked for")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// stuckLister ignores Offset and returns the same page forever, the failure mode
// that a length-advanced offset alone cannot terminate. It errors past maxCalls
// so a regression fails the test instead of hanging it.
type stuckLister struct {
	page     []semantic.TableSearchResult
	calls    int
	maxCalls int
}

func (s *stuckLister) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	s.calls++
	if s.calls > s.maxCalls {
		return nil, errors.New("lister called past the point enumeration should have stopped")
	}
	return s.page, nil
}

func TestLoadItemsFailsClosedOnAListerThatIgnoresOffset(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	lister := &stuckLister{
		page:     []semantic.TableSearchResult{result("urn:a", "a", ""), result("urn:b", "b", "")},
		maxCalls: 8,
	}

	_, err := NewSource(store, lister, 10_000).LoadItems(context.Background(), SourceID)
	require.ErrorContains(t, err, "stalled at offset 2",
		"a corpus that stopped growing is short through no decision of ours: reporting it as complete would let the Sink's replace prune the mirror down to it")
	assert.Equal(t, 2, lister.calls, "the second page adds nothing new, which is the stop condition")
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing is written when enumeration could not finish")
}

func TestLoadItemsStopsAtEntryCap(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	page := []semantic.TableSearchResult{result("urn:a", "a", ""), result("urn:b", "b", "")}
	lister := &stubLister{pages: [][]semantic.TableSearchResult{page, page}}
	expectSyncOf(mock, 2)

	items, err := NewSource(store, lister, 2).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	assert.Len(t, items, 2, "the cap bounds the corpus")
	require.Len(t, lister.filters, 1)
	assert.Equal(t, 2, lister.filters[0].Limit, "the last page asks only for the remaining budget")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestEntryCapTruncationIsLogged covers the one stop that reports a knowingly
// short corpus as a success. Coverage is computed against the mirror, so that
// corpus reads as full coverage on the Indexing dashboard and the log is the only
// signal that recall is bounded. Not parallel: it swaps the default logger.
func TestEntryCapTruncationIsLogged(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()
	expectSyncOf(mock, 1)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	lister := &corpusLister{corpus: corpusOf(5), clampAt: 2}
	_, err := NewSource(store, lister, 1).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "entry cap reached")
	assert.Contains(t, buf.String(), "indexed=1", "the log states how much was indexed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadItemsDeduplicatesRepeatedURNs(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	// A live catalog can repeat an entry across pages when the underlying set
	// shifts; a repeated item id would make the mirrored count disagree with
	// the item count the framework embeds. A repeat must be dropped without
	// ending the enumeration, so the last page's new URN has to survive it.
	dup := result("urn:dup", "t", "d")
	lister := &stubLister{pages: [][]semantic.TableSearchResult{
		{dup, dup},
		{dup, result("urn:new", "t", "d")},
	}}
	expectSyncOf(mock, 2)

	items, err := NewSource(store, lister, 10_000).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "urn:new", items[1].ItemID, "paging continued past the page that repeated an entry")
	assert.Equal(t, 3, lister.calls, "the third call is the empty page that ends the corpus")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadItemsSkipsEntriesWithNoURN(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	lister := &stubLister{pages: [][]semantic.TableSearchResult{{
		result("", "unidentifiable", "d"),
		result("urn:a", "a", "d"),
	}}}
	expectSyncOf(mock, 1)

	items, err := NewSource(store, lister, 100).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "urn:a", items[0].ItemID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadItemsFailsClosedOnEnumerationError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	lister := &stubLister{err: errors.New("datahub down")}
	_, err := NewSource(store, lister, 100).LoadItems(context.Background(), SourceID)

	require.Error(t, err, "a partial corpus must never reach the mirror: the replace that follows would prune real datasets")
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing is written when the catalog could not be read")
}

func TestLoadItemsSurfacesMirrorWriteFailures(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	lister := &stubLister{pages: [][]semantic.TableSearchResult{{result("urn:a", "a", "d")}}}
	mock.ExpectBegin().WillReturnError(errors.New("db down"))
	_, err := NewSource(store, lister, 100).LoadItems(context.Background(), SourceID)
	require.ErrorContains(t, err, "sync mirror")

	// A stamp failure is equally fatal: an unstamped sweep would re-run every
	// reconciler tick.
	lister2 := &stubLister{pages: [][]semantic.TableSearchResult{{result("urn:a", "a", "d")}}}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO catalog_datasets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("INSERT INTO catalog_dataset_sync").WillReturnError(errors.New("db down"))
	_, err = NewSource(store, lister2, 100).LoadItems(context.Background(), SourceID)
	require.ErrorContains(t, err, "stamp sync")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadItemsEmptyCatalog(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	lister := &stubLister{pages: [][]semantic.TableSearchResult{{}}}
	// No rows to upsert, but the sweep is still stamped: an empty catalog is a
	// completed enumeration, not an owed one.
	mock.ExpectExec("INSERT INTO catalog_dataset_sync").WillReturnResult(sqlmock.NewResult(0, 1))

	items, err := NewSource(store, lister, 100).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOnSucceededIsNoop(t *testing.T) {
	t.Parallel()
	NewSource(nil, nil, 1).OnSucceeded(SourceID)
}
