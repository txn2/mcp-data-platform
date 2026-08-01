package datasetindex

import (
	"context"
	"errors"
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

func TestLoadItemsPagesUntilExhausted(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	full := make([]semantic.TableSearchResult, pageSize)
	for i := range full {
		full[i] = result("urn:"+strconv.Itoa(i), "t", "d")
	}
	lister := &stubLister{pages: [][]semantic.TableSearchResult{full, {result("urn:last", "t", "d")}}}
	expectSyncOf(mock, pageSize+1)

	items, err := NewSource(store, lister, 10_000).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	assert.Len(t, items, pageSize+1)
	require.Len(t, lister.filters, 2)
	assert.Equal(t, pageSize, lister.filters[1].Offset, "the second page continues where the first ended")
	assert.NoError(t, mock.ExpectationsWereMet())
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

func TestLoadItemsDeduplicatesRepeatedURNs(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()

	// A live catalog can repeat an entry across pages when the underlying set
	// shifts; a repeated item id would make the mirrored count disagree with
	// the item count the framework embeds.
	first := make([]semantic.TableSearchResult, pageSize)
	for i := range first {
		first[i] = result("urn:dup", "t", "d")
	}
	lister := &stubLister{pages: [][]semantic.TableSearchResult{first, {result("urn:dup", "t", "d")}}}
	expectSyncOf(mock, 1)

	items, err := NewSource(store, lister, 10_000).LoadItems(context.Background(), SourceID)
	require.NoError(t, err)
	assert.Len(t, items, 1)
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
