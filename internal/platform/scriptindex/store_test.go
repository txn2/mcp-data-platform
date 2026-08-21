package scriptindex

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
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

// textColumns is the projection GetIndexText reads. source_code is deliberately
// absent, and this list is where that stays visible.
var textColumns = []string{
	"display_name", "name", "description", "category", "tags", "params", "status", "superseded_by",
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

func TestGetIndexTextComposesTheDescriptionCard(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows(textColumns).AddRow(
			"Daily Sales Report", "daily-sales", "Summarize yesterday's sales by region",
			"reporting", pq.Array([]string{"revenue"}),
			[]byte(`[{"name":"report_date","type":"date","required":true}]`),
			"active", ""),
	)

	got, err := store.GetIndexText(context.Background(), "scr-1")
	require.NoError(t, err)
	// The category is in the card beside the tags: the worker must hash the
	// same document the write path hashed, and the write path composes it from
	// the whole record.
	assert.Equal(t, "Daily Sales Report\nSummarize yesterday's sales by region\n"+
		"parameters: report_date (required)\nreporting revenue\n"+
		"Call run_script to execute it.", got)
}

// TestGetIndexTextReportsTheExecutionStateOfARetiredScript pins the one line
// that distinguishes a script to run from a dead end. The status reaches the
// card through the same RefuseRun answer the run gate gives, so a search hit
// never claims runnability run_script would then refuse.
func TestGetIndexTextReportsTheExecutionStateOfARetiredScript(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows(textColumns).AddRow(
			"", "daily-sales", "", "", pq.Array([]string{}), []byte(`[]`), "deprecated", ""),
	)

	got, err := store.GetIndexText(context.Background(), "scr-1")
	require.NoError(t, err)
	assert.Equal(t, "daily-sales\nNothing will execute this script: "+
		"the script is deprecated and must not be executed.", got)
}

// TestGetIndexTextTreatsAMissingOrDisabledScriptAsNothingToIndex covers the
// row a write deleted or disabled between enqueue and claim: the Source must
// turn it into a clean completion, not a failed job that retries forever.
func TestGetIndexTextTreatsAMissingOrDisabledScriptAsNothingToIndex(t *testing.T) {
	store, mock := newMock(t)
	for range 2 {
		mock.ExpectQuery("FROM scripts").WithArgs("gone").WillReturnError(sql.ErrNoRows)
	}

	_, err := store.GetIndexText(context.Background(), "gone")
	assert.ErrorIs(t, err, errNotIndexable)

	items, err := NewSource(store).LoadItems(context.Background(), "gone")
	assert.NoError(t, err)
	assert.Empty(t, items)
}

func TestGetIndexTextSurfacesAQueryFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnError(errors.New("boom"))

	_, err := store.GetIndexText(context.Background(), "scr-1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, errNotIndexable, "a failed read is not an empty unit")
}

func TestGetIndexTextSurfacesUnreadableParams(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows(textColumns).AddRow("D", "n", "", "", pq.Array([]string{}), []byte(`not json`), "active", ""),
	)

	_, err := store.GetIndexText(context.Background(), "scr-1")
	require.Error(t, err)
}

func TestListVectorsReturnsThePersistedVector(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnRows(
		sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgVecLiteral([]float32{0.1, 0.2, 0.3}), "nomic-embed-text", []byte("hash")),
	)

	got, err := store.ListVectors(context.Background(), "scr-1")
	require.NoError(t, err)
	require.Contains(t, got, "scr-1")
	assert.Equal(t, "nomic-embed-text", got["scr-1"].Model)
	assert.Equal(t, 3, got["scr-1"].Dim)
	assert.Equal(t, []byte("hash"), got["scr-1"].TextHash)
}

func TestListVectorsTreatsAnUnembeddedScriptAsEmpty(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnError(sql.ErrNoRows)

	got, err := store.ListVectors(context.Background(), "scr-1")
	require.NoError(t, err)
	assert.Empty(t, got, "no vector means the worker embeds it, not that the read failed")
}

func TestListVectorsSurfacesAQueryFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("scr-1").WillReturnError(errors.New("boom"))

	_, err := store.ListVectors(context.Background(), "scr-1")
	require.Error(t, err)
}

func TestUpsertVectorsWritesTheVectorWithoutTouchingUpdatedAt(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectExec("UPDATE scripts").
		WithArgs("scr-1", sqlmock.AnyArg(), "nomic-embed-text", []byte("hash")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.UpsertVectors(context.Background(), "scr-1", []indexjobs.Vector{
		{ItemID: "scr-1", Embedding: []float32{0.1, 0.2}, Model: "nomic-embed-text", TextHash: []byte("hash")},
	}))
}

func TestUpsertVectorsIsANoOpForAnEmptyRowSet(t *testing.T) {
	store, _ := newMock(t)
	// No DB call is expected: the mock would fail the test if one were made.
	require.NoError(t, store.UpsertVectors(context.Background(), "scr-1", nil))
}

func TestUpsertVectorsSurfacesAWriteFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectExec("UPDATE scripts").WillReturnError(errors.New("boom"))

	err := store.UpsertVectors(context.Background(), "scr-1", []indexjobs.Vector{
		{ItemID: "scr-1", Embedding: []float32{0.1}, Model: "m", TextHash: []byte("h")},
	})
	require.Error(t, err)
}

func TestFindGapsReturnsUnembeddedAndModelSwappedScripts(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("nomic-embed-text").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow("scr-1").AddRow("scr-2"),
	)

	ids, err := store.FindGaps(context.Background(), "nomic-embed-text")
	require.NoError(t, err)
	assert.Equal(t, []string{"scr-1", "scr-2"}, ids)
}

func TestFindGapsSurfacesAQueryFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("m").WillReturnError(errors.New("boom"))

	_, err := store.FindGaps(context.Background(), "m")
	require.Error(t, err)
}

func TestFindGapsSurfacesARowFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WithArgs("m").WillReturnRows(
		sqlmock.NewRows([]string{"id"}).AddRow("scr-1").RowError(0, errors.New("boom")),
	)

	_, err := store.FindGaps(context.Background(), "m")
	require.Error(t, err)
}

func TestCoverageCountsEnabledScripts(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnRows(
		sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(3, 5),
	)

	indexed, expected, err := store.Coverage(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, indexed)
	assert.Equal(t, 5, expected)
}

func TestCoverageSurfacesAQueryFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnError(errors.New("boom"))

	_, _, err := store.Coverage(context.Background())
	require.Error(t, err)
}
