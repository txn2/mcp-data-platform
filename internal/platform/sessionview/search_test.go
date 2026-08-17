package sessionview

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matchColumns lists the search SELECT columns in scan order.
var matchColumns = []string{
	"session_id", "started_at", "last_active_at", "call_count", "failure_count",
	"purposes", "asset_names", "score",
}

func matchRow() *sqlmock.Rows {
	return sqlmock.NewRows(matchColumns).AddRow(
		testSessionID, testTime(), testTime().Add(time.Minute), testCallCount, 1,
		"{\"Summing Q3 revenue by region.\",\"Adding the prior-year comparison.\"}",
		"{\"Q3 revenue by region\"}",
		0.42,
	)
}

func TestSearch_ProjectsMatch(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM rolled r").
		WithArgs("user-1", "q3 revenue", defaultSearchLimit).
		WillReturnRows(matchRow())

	got, err := store.Search(context.Background(), SearchQuery{Text: "q3 revenue", UserID: "user-1"})
	require.NoError(t, err)
	require.Len(t, got, 1)

	assert.Equal(t, testSessionID, got[0].SessionID)
	assert.Equal(t, KindAgent, got[0].Kind, "kind is derived from the id prefix")
	assert.Equal(t, testCallCount, got[0].CallCount)
	assert.Equal(t, 1, got[0].FailureCount)
	assert.Equal(t, []string{
		"Summing Q3 revenue by region.",
		"Adding the prior-year comparison.",
	}, got[0].Purposes)
	assert.Equal(t, []string{"Q3 revenue by region"}, got[0].AssetNames)
	assert.InDelta(t, 0.42, got[0].Score, 0.001)
}

// A search with no caller must not run at all: an unscoped statement here would
// rank every caller's sessions against the query.
func TestSearch_FailsClosedWithoutCaller(t *testing.T) {
	store, _ := newMock(t)

	got, err := store.Search(context.Background(), SearchQuery{Text: "revenue"})
	require.NoError(t, err)
	assert.Empty(t, got, "no caller, no query, no results")
}

// An empty query is not a request for every session: plainto_tsquery would
// match nothing anyway, and the roll-up behind it is not worth running.
func TestSearch_EmptyTextRunsNothing(t *testing.T) {
	store, _ := newMock(t)

	got, err := store.Search(context.Background(), SearchQuery{Text: "   ", UserID: "user-1"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSearch_LimitBounds(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"unstated", 0, defaultSearchLimit},
		{"negative", -5, defaultSearchLimit},
		{"stated", 25, 25},
		{"over the cap", MaxPerPage + 1, defaultSearchLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, mock := newMock(t)
			mock.ExpectQuery("FROM rolled r").
				WithArgs("user-1", "revenue", tt.want).
				WillReturnRows(sqlmock.NewRows(matchColumns))

			_, err := store.Search(context.Background(),
				SearchQuery{Text: "revenue", UserID: "user-1", Limit: tt.limit})
			require.NoError(t, err)
		})
	}
}

func TestSearch_QueryError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM rolled r").WillReturnError(errors.New("connection reset"))

	_, err := store.Search(context.Background(), SearchQuery{Text: "revenue", UserID: "user-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "searching sessions")
}

func TestSearch_ScanError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM rolled r").WillReturnRows(
		sqlmock.NewRows(matchColumns).AddRow(
			testSessionID, testTime(), testTime(), testCallCount, 0,
			"{}", "{}", "not-a-score",
		),
	)

	_, err := store.Search(context.Background(), SearchQuery{Text: "revenue", UserID: "user-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scanning session match")
}

func TestSearch_RowsError(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM rolled r").WillReturnRows(
		matchRow().RowError(0, errors.New("read failed")),
	)

	_, err := store.Search(context.Background(), SearchQuery{Text: "revenue", UserID: "user-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iterating session search")
}

// ReaderFor is the gate that keeps the session sources off a deployment with no
// database: a typed nil would satisfy every non-nil check its consumers make.
func TestReaderFor(t *testing.T) {
	assert.Nil(t, ReaderFor(nil), "no database, no reader")

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	reader := ReaderFor(db)
	require.NotNil(t, reader)
	var _ Store = reader
}
