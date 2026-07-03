package memory

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pairRec(id string, createdAt time.Time) Record {
	return Record{ID: id, CreatedAt: createdAt, Status: StatusActive}
}

func TestOrderPair(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)

	t.Run("orders by creation time", func(t *testing.T) {
		p := orderPair(pairRec("newer", t1), pairRec("older", t0), 0.95)
		assert.Equal(t, "older", p.Older.ID)
		assert.Equal(t, "newer", p.Newer.ID)
		assert.Equal(t, 0.95, p.Score)
	})

	t.Run("equal timestamps fall back to id order", func(t *testing.T) {
		p := orderPair(pairRec("b", t0), pairRec("a", t0), 0.9)
		assert.Equal(t, "a", p.Older.ID)
		assert.Equal(t, "b", p.Newer.ID)
	})

	t.Run("already ordered is preserved", func(t *testing.T) {
		p := orderPair(pairRec("older", t0), pairRec("newer", t1), 0.9)
		assert.Equal(t, "older", p.Older.ID)
		assert.Equal(t, "newer", p.Newer.ID)
	})
}

func TestDedupePairs(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	mk := func(older, newer string, score float64) SimilarPair {
		return SimilarPair{Older: pairRec(older, t0), Newer: pairRec(newer, t0.Add(time.Minute)), Score: score}
	}

	t.Run("drops mirror duplicates and preserves input order", func(t *testing.T) {
		// Input arrives pre-sorted (the SQL orders by score DESC).
		out := dedupePairs([]SimilarPair{
			mk("c", "d", 0.97),
			mk("a", "b", 0.95),
			mk("a", "b", 0.95), // the same pair seen from the other side
		}, 10)
		require.Len(t, out, 2)
		assert.Equal(t, "c", out[0].Older.ID)
		assert.Equal(t, "a", out[1].Older.ID)
	})

	t.Run("trims to limit", func(t *testing.T) {
		out := dedupePairs([]SimilarPair{mk("a", "b", 0.95), mk("c", "d", 0.9)}, 1)
		require.Len(t, out, 1)
		assert.Equal(t, "a", out[0].Older.ID)
	})

	t.Run("empty input yields empty output", func(t *testing.T) {
		assert.Empty(t, dedupePairs(nil, 5))
	})
}

// pairRowColumns is the 37-column projection SimilarActivePairs selects: both
// sides' record columns plus the score.
func pairRowColumns() []string {
	cols := make([]string, 0, 37)
	for _, alias := range []string{"a", "b"} {
		for _, c := range memorySelectColumns {
			cols = append(cols, alias+"_"+c)
		}
	}
	return append(cols, "score")
}

// pairRowValues renders one result row: record a, record b, score.
func pairRowValues(aID string, aCreated time.Time, bID string, bCreated time.Time, score float64) []driver.Value {
	recVals := func(id string, created time.Time) []driver.Value {
		return []driver.Value{
			id, created, created, "user@example.com", "analyst", DimensionKnowledge, SinkBusinessKnowledge,
			"content of " + id, CategoryBusinessCtx, ConfidenceMedium, SourceUser,
			[]byte(`[]`), []byte(`[]`), []byte(`{}`),
			StatusActive, nil, nil, nil,
		}
	}
	vals := append(recVals(aID, aCreated), recVals(bID, bCreated)...)
	return append(vals, score)
}

func TestPostgresStore_SimilarActivePairs(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)

	t.Run("returns ordered deduplicated pairs", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup

		rows := sqlmock.NewRows(pairRowColumns()).
			AddRow(pairRowValues("m2", t1, "m1", t0, 0.96)...).
			AddRow(pairRowValues("m1", t0, "m2", t1, 0.96)...) // mirror image from the other side
		mock.ExpectQuery("SELECT .* FROM memory_records a").
			WithArgs(0.75, "user@example.com").
			WillReturnRows(rows)

		store := NewPostgresStore(db)
		finder, ok := store.(DuplicateFinder)
		require.True(t, ok, "postgres store must implement DuplicateFinder")

		pairs, err := finder.SimilarActivePairs(context.Background(), "user@example.com", 0.75, 10)
		require.NoError(t, err)
		require.Len(t, pairs, 1, "mirror-image rows must collapse to one pair")
		assert.Equal(t, "m1", pairs[0].Older.ID, "pair must be ordered older first")
		assert.Equal(t, "m2", pairs[0].Newer.ID)
		assert.Equal(t, 0.96, pairs[0].Score)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup

		mock.ExpectQuery("SELECT .* FROM memory_records a").
			WillReturnError(errors.New("boom"))

		finder, ok := NewPostgresStore(db).(DuplicateFinder)
		require.True(t, ok)
		_, err = finder.SimilarActivePairs(context.Background(), "user@example.com", 0.75, 10)
		require.Error(t, err)
	})

	t.Run("missing owner scope is rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup

		finder, ok := NewPostgresStore(db).(DuplicateFinder)
		require.True(t, ok)
		_, err = finder.SimilarActivePairs(context.Background(), "", 0.75, 10)
		require.Error(t, err, "an unscoped pair listing would expose other users' records")
		require.NoError(t, mock.ExpectationsWereMet(), "no query must run without an owner scope")
	})

	t.Run("scan error propagates", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup

		rows := sqlmock.NewRows(pairRowColumns()).
			AddRow(pairRowValues("m2", t1, "m1", t0, 0.96)...).
			RowError(0, errors.New("row boom"))
		mock.ExpectQuery("SELECT .* FROM memory_records a").WillReturnRows(rows)

		finder, ok := NewPostgresStore(db).(DuplicateFinder)
		require.True(t, ok)
		_, err = finder.SimilarActivePairs(context.Background(), "user@example.com", 0.75, 10)
		require.Error(t, err)
	})
}
