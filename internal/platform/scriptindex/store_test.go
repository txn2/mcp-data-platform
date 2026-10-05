package scriptindex

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMock returns a store over a mocked database plus the mock controller.
func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	return NewStore(db), mock
}

// textColumns is the projection GetIndexed reads: every field
// script.IndexCorpus composes, the source included (#2027).
var textColumns = []string{
	"display_name", "name", "description", "category", "tags", "params", "status", "superseded_by", "library", "source_code",
}

// pgVecLiteral renders a []float32 in the pgvector text format so sqlmock's
// driver value round-trips through the pgvector.Vector scanner.
func pgVecLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestGetIndexedReadsEveryFieldTheCorpusComposes(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("SELECT display_name, name, description, category, tags, params,\\s+status, superseded_by, library, source_code").
		WithArgs("scr-1").WillReturnRows(sqlmock.NewRows(textColumns).AddRow(
		"Daily Sales Report", "daily-sales", "Summarize yesterday's sales", "reporting",
		pq.Array([]string{"revenue"}), []byte(`[{"name":"report_date","type":"date","required":true}]`),
		"active", "", false, "rows = platform.query('SELECT 1')\n"))

	sc, err := store.GetIndexed(context.Background(), "scr-1")
	require.NoError(t, err)
	assert.True(t, sc.Enabled)
	corpus := Corpus(sc)
	assert.Contains(t, corpus, "Daily Sales Report")
	assert.Contains(t, corpus, "parameters: report_date (required)")
	assert.Contains(t, corpus, "reporting revenue")
	assert.Contains(t, corpus, "platform.query('SELECT 1')", "the source is indexed")
}

func TestGetIndexedTreatsAMissingOrDisabledScriptAsNothingToIndex(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("gone").WillReturnRows(sqlmock.NewRows(textColumns))
	_, err := store.GetIndexed(context.Background(), "gone")
	require.ErrorIs(t, err, errNotIndexable)
}

func TestGetIndexedSurfacesFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnError(errors.New("boom"))
	_, err := store.GetIndexed(context.Background(), "scr-1")
	require.Error(t, err)
	require.NotErrorIs(t, err, errNotIndexable)

	mock.ExpectQuery("FROM scripts").WillReturnRows(sqlmock.NewRows(textColumns).AddRow(
		"", "n", "", "", pq.Array([]string{}), []byte(`{not json`), "active", "", false, ""))
	_, err = store.GetIndexed(context.Background(), "scr-1")
	require.ErrorContains(t, err, "unmarshal script params")
}

func TestListVectorsSurfacesFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM script_embedding_chunks").WillReturnError(errors.New("boom"))
	_, err := store.ListVectors(context.Background(), "scr-1")
	require.Error(t, err)

	mock.ExpectQuery("FROM script_embedding_chunks").WillReturnRows(
		sqlmock.NewRows([]string{"chunk_index"}).AddRow(0))
	_, err = store.ListVectors(context.Background(), "scr-1")
	require.Error(t, err, "a row the scan cannot read is a failure, not an empty set")

	mock.ExpectQuery("FROM script_embedding_chunks").WillReturnRows(
		sqlmock.NewRows([]string{"chunk_index", "text_hash", "embedding", "model"}).
			AddRow(0, []byte("h"), pgVecLiteral([]float32{1}), "m").RowError(0, errors.New("boom")))
	_, err = store.ListVectors(context.Background(), "scr-1")
	require.Error(t, err)

	mock.ExpectQuery("FROM script_embedding_chunks").WillReturnRows(
		sqlmock.NewRows([]string{"chunk_index", "text_hash", "embedding", "model"}))
	got, err := store.ListVectors(context.Background(), "scr-1")
	require.NoError(t, err)
	assert.Empty(t, got, "a script with no chunks is embedded in full")
}

func TestFindGapsSurfacesFailures(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnError(errors.New("boom"))
	_, err := store.FindGaps(context.Background(), "m")
	require.Error(t, err)

	mock.ExpectQuery("FROM scripts").WillReturnRows(sqlmock.NewRows([]string{"id", "extra"}).AddRow("a", "b"))
	_, err = store.FindGaps(context.Background(), "m")
	require.Error(t, err)

	mock.ExpectQuery("FROM scripts").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow("a").RowError(0, errors.New("boom")))
	_, err = store.FindGaps(context.Background(), "m")
	require.Error(t, err)
}

// TestGetIndexedReadsTheLibraryFlag pins the field the card's execution note
// reads for a library: without it the worker would hash a different card from
// the one the save hashed, and a library would owe an embedding forever.
func TestGetIndexedReadsTheLibraryFlag(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnRows(sqlmock.NewRows(textColumns).AddRow(
		"", "helpers", "Shared helpers.", "", pq.Array([]string{}), []byte(`[]`), "active", "", true, "def double(x):\n    return x * 2\n"))
	sc, err := store.GetIndexed(context.Background(), "scr-1")
	require.NoError(t, err)
	assert.True(t, sc.Library)
	assert.NotContains(t, Corpus(sc), "Call run_script", "a library's card says nothing runs it")
}
