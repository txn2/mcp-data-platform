package callrecord

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectCounts queues one count: the personas query, then the principals one.
func expectCounts(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("GROUP BY persona").WithArgs(topCallersLimit).
		WillReturnRows(sqlmock.NewRows([]string{"persona", "n", "total"}).
			AddRow("integration", 990, 1000).
			AddRow("analyst", 10, 1000))
	mock.ExpectQuery("GROUP BY user_id, persona").WithArgs(topCallersLimit).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "email", "persona", "n"}).
			AddRow("apikey:crm-sync", "", "integration", 990).
			AddRow("user-1", "analyst@example.com", "analyst", 10))
}

func TestTopCallersNamesTheLargestShareFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	store := NewPostgresStore(db, Config{
		ExcludePersonas: []string{"Analyst"},
		ServiceAccounts: fakeAccounts{"integration": true},
	})
	expectCounts(mock)

	got, err := store.TopCallers(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1000, got.Total)
	require.Len(t, got.Personas, 2)
	assert.Equal(t, PersonaShare{Persona: "integration", Records: 990, Share: 0.99, ServiceAccount: true}, got.Personas[0])
	assert.Equal(t, PersonaShare{Persona: "analyst", Records: 10, Share: 0.01, ExcludedByConfig: true}, got.Personas[1])

	require.Len(t, got.Principals, 2)
	assert.Equal(t, "apikey:crm-sync", got.Principals[0].UserID)
	assert.Equal(t, "integration", got.Principals[0].Persona)
	assert.InDelta(t, 0.99, got.Principals[0].Share, 1e-9)
	assert.True(t, got.Principals[0].ServiceAccount, "a caller carries its persona's mark")
	assert.Equal(t, "analyst@example.com", got.Principals[1].UserEmail)
}

func TestTopCallersAnswersFromTheCountUntilItExpires(t *testing.T) {
	store, mock := newMock(t)
	accounts := fakeAccounts{}
	store.excluded = store.excluded.WithServiceAccounts(accounts)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	store.top.now = func() time.Time { return now }

	expectCounts(mock)
	first, err := store.TopCallers(context.Background())
	require.NoError(t, err)
	assert.False(t, first.Personas[0].ServiceAccount)

	// Inside the window the count is not taken again, but the mark is read
	// again: a persona marked a moment ago shows as marked.
	accounts["integration"] = true
	now = now.Add(topCallersTTL - time.Second)
	second, err := store.TopCallers(context.Background())
	require.NoError(t, err)
	assert.True(t, second.Personas[0].ServiceAccount)
	assert.Equal(t, first.CountedAt, second.CountedAt)
	assert.False(t, first.Personas[0].ServiceAccount, "an earlier answer is not changed under its reader")

	// Past the window it is counted again.
	now = now.Add(time.Second)
	expectCounts(mock)
	third, err := store.TopCallers(context.Background())
	require.NoError(t, err)
	assert.Equal(t, now, third.CountedAt)
}

func TestTopCallersIsCountedAgainAfterASweepRemovedRecords(t *testing.T) {
	store, mock := newMock(t)

	expectCounts(mock)
	_, err := store.TopCallers(context.Background())
	require.NoError(t, err)

	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(3))
	_, err = store.Cleanup(context.Background())
	require.NoError(t, err)

	expectCounts(mock)
	_, err = store.TopCallers(context.Background())
	require.NoError(t, err)
}

func TestTopCallersOfAnEmptyCatalogAreEmptyLists(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("GROUP BY persona").
		WillReturnRows(sqlmock.NewRows([]string{"persona", "n", "total"}))
	mock.ExpectQuery("GROUP BY user_id, persona").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "email", "persona", "n"}))

	got, err := store.TopCallers(context.Background())
	require.NoError(t, err)
	assert.Zero(t, got.Total)
	assert.NotNil(t, got.Personas, "an empty list serializes as [], never null")
	assert.NotNil(t, got.Principals, "an empty list serializes as [], never null")
	assert.Empty(t, got.Personas)
	assert.Empty(t, got.Principals)
}

func TestTopCallersReportsAFailedCount(t *testing.T) {
	cases := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{"persona query", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("GROUP BY persona").WillReturnError(errors.New("db down"))
		}},
		{"persona scan", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("GROUP BY persona").
				WillReturnRows(sqlmock.NewRows([]string{"persona", "n", "total"}).AddRow("x", "not-a-number", 1))
		}},
		{"persona rows", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("GROUP BY persona").
				WillReturnRows(sqlmock.NewRows([]string{"persona", "n", "total"}).
					AddRow("x", 1, 1).RowError(0, errors.New("broken")))
		}},
		{"principal query", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("GROUP BY persona").
				WillReturnRows(sqlmock.NewRows([]string{"persona", "n", "total"}))
			m.ExpectQuery("GROUP BY user_id, persona").WillReturnError(errors.New("db down"))
		}},
		{"principal scan", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("GROUP BY persona").
				WillReturnRows(sqlmock.NewRows([]string{"persona", "n", "total"}))
			m.ExpectQuery("GROUP BY user_id, persona").
				WillReturnRows(sqlmock.NewRows([]string{"user_id", "email", "persona", "n"}).
					AddRow("u", "", "p", "not-a-number"))
		}},
		{"principal rows", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("GROUP BY persona").
				WillReturnRows(sqlmock.NewRows([]string{"persona", "n", "total"}))
			m.ExpectQuery("GROUP BY user_id, persona").
				WillReturnRows(sqlmock.NewRows([]string{"user_id", "email", "persona", "n"}).
					AddRow("u", "", "p", 1).RowError(0, errors.New("broken")))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, mock := newMock(t)
			c.setup(mock)
			_, err := store.TopCallers(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "call records by")
		})
	}
}

func TestShareIsZeroWithoutAWhole(t *testing.T) {
	t.Parallel()
	assert.Zero(t, share(5, 0))
	assert.InDelta(t, 0.25, share(1, 4), 1e-9)
}

// A count that was running when a sweep removed records is answered with but
// not kept: the next request counts again.
func TestTopCallersKeepsNoCountASweepOutdated(t *testing.T) {
	store, _ := newMock(t)
	_, generation := store.top.held()
	store.top.forget()
	store.top.keep(&TopCallers{CountedAt: store.top.clock()}, generation)
	held, _ := store.top.held()
	assert.Nil(t, held, "the outdated count was kept")

	_, generation = store.top.held()
	store.top.keep(&TopCallers{CountedAt: store.top.clock()}, generation)
	held, _ = store.top.held()
	assert.NotNil(t, held, "a count no sweep outdated is kept")
}
