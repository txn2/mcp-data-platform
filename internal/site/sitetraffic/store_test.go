package sitetraffic

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Store{DB: db}, mock
}

func q(s string) string { return regexp.QuoteMeta(s) }

var errDown = errors.New("down")

func countCols(lead ...string) []string {
	cols := append([]string{}, lead...)
	cols = append(cols, "c2xx", "c3xx", "c4xx", "c5xx", "cother")
	for i := range NumBuckets {
		cols = append(cols, "l"+string(rune('a'+i)))
	}
	return cols
}

func countVals(lead ...driver.Value) []driver.Value {
	out := append([]driver.Value{}, lead...)
	out = append(out, int64(50), int64(0), int64(10), int64(0), int64(0))
	for i := range NumBuckets {
		if i == 0 {
			out = append(out, int64(60))
			continue
		}
		out = append(out, int64(0))
	}
	return out
}

// TestAdd is #2647's criterion 11 at the store: a flush is two statements
// however many rows, in one transaction.
func TestAdd(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 14, 13, 5, 0, 0, time.UTC)
	rows := []Row{{Grain: GrainMinute, Bucket: at, Key: prod, Counts: Counts{Classes: [NumClasses]int64{1}}}}
	reps := []ReplicaRow{{Grain: GrainMinute, Bucket: at, Instance: "a", Counts: Counts{Classes: [NumClasses]int64{1}}}}
	t.Run("two statements", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectExec(q("INSERT INTO site_traffic (")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_traffic_replicas")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		unknown, err := s.Add(ctx, rows, reps)
		require.NoError(t, err)
		assert.False(t, unknown)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("a failure before the commit is known", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectExec(q("INSERT INTO site_traffic (")).WillReturnError(errDown)
		mock.ExpectRollback()
		unknown, err := s.Add(ctx, rows, reps)
		require.Error(t, err)
		assert.False(t, unknown)
	})
	t.Run("a replica failure before the commit is known", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectExec(q("INSERT INTO site_traffic_replicas")).WillReturnError(errDown)
		mock.ExpectRollback()
		unknown, err := s.Add(ctx, nil, reps)
		require.Error(t, err)
		assert.False(t, unknown)
	})
	t.Run("a failed commit is unknown", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectExec(q("INSERT INTO site_traffic (")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errDown)
		unknown, err := s.Add(ctx, rows, nil)
		require.Error(t, err)
		assert.True(t, unknown)
	})
	t.Run("a begin that fails", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin().WillReturnError(errDown)
		_, err := s.Add(ctx, rows, nil)
		require.Error(t, err)
	})
}

func TestReads(t *testing.T) {
	ctx := context.Background()
	since := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	s, mock := newMock(t)

	mock.ExpectQuery(q("FROM site_traffic\n\t WHERE kind = $1 AND subject = $2 AND ($3 = '' OR environment = $3)")).
		WithArgs(KindSite, "site_1", "", GrainMinute, "900 seconds", since).
		WillReturnRows(sqlmock.NewRows(countCols("at")).AddRow(countVals(since)...))
	series, err := s.Series(ctx, SeriesQuery{Kind: KindSite, Subject: "site_1", Range: Range{Grain: GrainMinute, Step: 15 * time.Minute}, Since: since})
	require.NoError(t, err)
	require.Len(t, series, 1)
	assert.Equal(t, int64(60), series[0].Counts.Total())

	mock.ExpectQuery(q("GROUP BY environment")).WithArgs(KindSite, "site_1", GrainMinute, since).
		WillReturnRows(sqlmock.NewRows(countCols("environment")).AddRow(countVals("prod")...))
	by, order, err := s.ByEnvironment(ctx, KindSite, "site_1", GrainMinute, since)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod"}, order)
	assert.Equal(t, int64(10), by["prod"].Classes[2])

	mock.ExpectQuery(q("GROUP BY subject, environment")).WithArgs(KindSite, since).
		WillReturnRows(sqlmock.NewRows(countCols("subject", "environment")).AddRow(countVals("site_1", "prod")...))
	subjects, err := s.BySubject(ctx, KindSite, since)
	require.NoError(t, err)
	assert.Equal(t, int64(60), subjects["site_1"]["prod"].Total())

	mock.ExpectQuery(q("FROM site_traffic_replicas")).WithArgs(GrainHour, since).
		WillReturnRows(sqlmock.NewRows(countCols("instance")).AddRow(countVals("a:8080")...))
	reps, names, err := s.Replicas(ctx, GrainHour, since)
	require.NoError(t, err)
	assert.Equal(t, []string{"a:8080"}, names)
	assert.Equal(t, int64(60), reps["a:8080"].Total())

	mock.ExpectQuery(q("FROM site_traffic")).WillReturnError(errDown)
	_, err = s.Series(ctx, SeriesQuery{Kind: KindSite, Subject: "site_1", Range: Range{Grain: GrainMinute, Step: time.Minute}, Since: since})
	require.Error(t, err)
	mock.ExpectQuery(q("GROUP BY environment")).WillReturnError(errDown)
	_, _, err = s.ByEnvironment(ctx, KindSite, "site_1", GrainMinute, since)
	require.Error(t, err)
	mock.ExpectQuery(q("GROUP BY subject")).WillReturnError(errDown)
	_, err = s.BySubject(ctx, KindSite, since)
	require.Error(t, err)
	mock.ExpectQuery(q("GROUP BY environment")).WillReturnRows(sqlmock.NewRows([]string{"environment"}).AddRow("prod"))
	_, _, err = s.ByEnvironment(ctx, KindSite, "site_1", GrainMinute, since)
	require.Error(t, err, "a row that does not scan")
}

func TestSweep(t *testing.T) {
	s, mock := newMock(t)
	m, h := time.Now().Add(-24*time.Hour), time.Now().Add(-90*24*time.Hour)
	mock.ExpectExec(q("DELETE FROM site_traffic WHERE")).WithArgs(m, h).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(q("DELETE FROM site_traffic_replicas")).WithArgs(m, h).WillReturnResult(sqlmock.NewResult(0, 2))
	n, err := s.Sweep(context.Background(), m, h)
	require.NoError(t, err)
	assert.Equal(t, int64(5), n)
	mock.ExpectExec(q("DELETE FROM site_traffic WHERE")).WillReturnError(errDown)
	_, err = s.Sweep(context.Background(), m, h)
	require.Error(t, err)
}
