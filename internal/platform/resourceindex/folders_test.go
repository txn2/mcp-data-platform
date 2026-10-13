package resourceindex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

const nltvKey = "global::pipelines/nltv"

func TestAboutFiles(t *testing.T) {
	cases := map[int]string{
		1: "1 file", 7: "7 files", 12: "about 10 files", 4775: "about 5,000 files",
		1500: "about 2,000 files", 1449: "about 1,000 files", 23389: "about 20,000 files",
	}
	for n, want := range cases {
		assert.Equal(t, want, aboutFiles(n), "n=%d", n)
	}
}

// TestFolderSummaryIsStableAcrossSmallChanges is what keeps a backfill from
// re-embedding its folder on every file: one more file, written the same
// month, leaves the text as it was.
func TestFolderSummaryIsStableAcrossSmallChanges(t *testing.T) {
	oldest := time.Date(2026, 10, 12, 17, 0, 0, 0, time.UTC)
	sum := FolderSummary{
		Path: "pipelines/nltv", Description: "NLTV station daily archive",
		Files: 4775, MIMETypes: []string{"text/csv"}, Oldest: oldest, Newest: oldest.Add(3 * time.Hour),
		Samples: []string{"a.csv", "b.csv", "c.csv"},
	}
	next := sum
	next.Files++
	next.Newest = next.Newest.Add(20 * time.Minute)
	assert.Equal(t, sum.Text(), next.Text())

	text := sum.Text()
	for _, want := range []string{"Folder pipelines/nltv", "NLTV station daily archive", "about 5,000 files", "text/csv", "2026-10", "a.csv, b.csv, c.csv"} {
		assert.Contains(t, text, want)
	}
}

func expectFolderSummary(mock sqlmock.Sqlmock, files int) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\), MIN\\(created_at\\)").
		WithArgs("global", "", "pipelines/nltv").
		WillReturnRows(sqlmock.NewRows([]string{"count", "min", "max", "mimes"}).
			AddRow(files, now, now, pq.Array([]string{"text/csv"})))
}

func TestFolderSource_SummarizesAFolderAsOneItem(t *testing.T) {
	store, mock := newDB(t)
	expectFolderSummary(mock, 1500)
	mock.ExpectQuery("SELECT filename FROM resources").
		WithArgs("global", "", "pipelines/nltv").
		WillReturnRows(sqlmock.NewRows([]string{"filename"}).AddRow("2019-01-01.csv").AddRow("2019-01-02.csv"))
	mock.ExpectQuery("SELECT description FROM resource_folders").
		WithArgs("global", "", "pipelines/nltv").
		WillReturnRows(sqlmock.NewRows([]string{"description"}).AddRow("NLTV station daily archive"))
	mock.ExpectExec("INSERT INTO resource_folders").
		WithArgs("global", sqlmock.AnyArg(), "pipelines/nltv", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	items, err := NewFolderSource(store).LoadItems(context.Background(), nltvKey)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, nltvKey, items[0].ItemID)
	assert.True(t, strings.Contains(items[0].Text, "NLTV station daily archive") &&
		strings.Contains(items[0].Text, "2019-01-01.csv"), items[0].Text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFolderSource_AFolderWithNothingUnindexedYieldsNoItem(t *testing.T) {
	store, mock := newDB(t)
	expectFolderSummary(mock, 0)
	items, err := NewFolderSource(store).LoadItems(context.Background(), nltvKey)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFolderSource_ABadKeyIsGone(t *testing.T) {
	store, _ := newDB(t)
	_, err := NewFolderSource(store).LoadItems(context.Background(), "nonsense")
	assert.ErrorIs(t, err, indexjobs.ErrSourceGone)
}

func TestFolderSource_StoreErrorsSurface(t *testing.T) {
	store, mock := newDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db down"))
	_, err := NewFolderSource(store).LoadItems(context.Background(), nltvKey)
	assert.Error(t, err)
}

func TestFolderSink_WritesAndClearsTheEntry(t *testing.T) {
	store, mock := newDB(t)
	sink := NewFolderSink(store, "model-a")
	key := indexjobs.Key{SourceKind: FolderKind, SourceID: nltvKey}
	assert.Equal(t, FolderKind, sink.Kind())
	assert.Equal(t, FolderKind, NewFolderSource(store).Kind())

	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM resource_folders").
		WithArgs("global", "", "pipelines/nltv").WillReturnError(errNoRows())
	existing, err := sink.ListExisting(context.Background(), key)
	require.NoError(t, err)
	assert.Empty(t, existing)

	mock.ExpectExec("UPDATE resource_folders\\s+SET embedding = \\$4").
		WithArgs("global", "", "pipelines/nltv", sqlmock.AnyArg(), "model-a", []byte("h")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.Upsert(context.Background(), key, []indexjobs.Vector{{
		ItemID: nltvKey, Embedding: []float32{0.1}, Model: "model-a", TextHash: []byte("h"),
	}}))

	mock.ExpectExec("UPDATE resource_folders\\s+SET embedding = NULL").
		WithArgs("global", "", "pipelines/nltv").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.Upsert(context.Background(), key, nil))
	require.NoError(t, sink.UpsertBatch(context.Background(), key, nil))
	mock.ExpectExec("UPDATE resource_folders\\s+SET embedding = \\$4").
		WithArgs("global", "", "pipelines/nltv", sqlmock.AnyArg(), "model-a", []byte("h")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.UpsertBatch(context.Background(), key, []indexjobs.Vector{{
		ItemID: nltvKey, Embedding: []float32{0.1}, Model: "model-a", TextHash: []byte("h"),
	}}))
	require.NoError(t, sink.StampExpected(context.Background(), key, 1))
	NewFolderSource(store).OnSucceeded(nltvKey)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFolderSink_GapsAndCoverage(t *testing.T) {
	store, mock := newDB(t)
	sink := NewFolderSink(store, "model-a")

	mock.ExpectQuery("SELECT scope, COALESCE\\(scope_id, ''\\), path FROM resources WHERE index_policy = 'none'").
		WithArgs("model-a").
		WillReturnRows(sqlmock.NewRows([]string{"scope", "scope_id", "path"}).
			AddRow("global", "", "pipelines/nltv").AddRow("user", "a@example.com", "x"))
	gaps, err := sink.FindGaps(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{nltvKey, resource.FolderKey(resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: "a@example.com"}, "x")}, gaps)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FILTER").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(2, 3))
	cov, err := sink.Coverage(context.Background())
	require.NoError(t, err)
	assert.Equal(t, indexjobs.Coverage{Indexed: 2, Expected: 3, ExpectedKnown: true}, cov)

	mock.ExpectQuery("SELECT scope").WillReturnError(errors.New("db down"))
	_, err = sink.FindGaps(context.Background())
	assert.Error(t, err)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db down"))
	_, err = sink.Coverage(context.Background())
	assert.Error(t, err)
}

func TestFolderSink_ExistingEntryIsReadBack(t *testing.T) {
	store, mock := newDB(t)
	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM resource_folders").
		WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow("[0.5,0.25]", "model-a", []byte("h")))
	got, err := NewFolderSink(store, "model-a").ListExisting(context.Background(), indexjobs.Key{SourceID: nltvKey})
	require.NoError(t, err)
	assert.Equal(t, []float32{0.5, 0.25}, got[nltvKey].Embedding)
	assert.Equal(t, "model-a", got[nltvKey].Model)
}
