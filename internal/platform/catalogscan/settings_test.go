package catalogscan

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsInterval(t *testing.T) {
	assert.Equal(t, DefaultScanInterval, Defaults().Interval())
	assert.Equal(t, 90*time.Minute, Settings{ScanInterval: "90m"}.Interval())
	assert.Equal(t, DefaultScanInterval, Settings{ScanInterval: "soon"}.Interval())
	assert.Equal(t, DefaultScanInterval, Settings{ScanInterval: "-1h"}.Interval())
}

func TestSettingsValidate(t *testing.T) {
	assert.NoError(t, Defaults().Validate())
	assert.Error(t, Settings{ScanInterval: "soon"}.Validate())
	assert.Error(t, Settings{ScanInterval: "1m"}.Validate(), "shorter than the minimum")
	assert.Error(t, Settings{ScanInterval: "6h", Exclusions: []Exclusion{{Connection: "w"}}}.Validate())
}

func TestExclusionsFor(t *testing.T) {
	s := Settings{Exclusions: []Exclusion{
		{ConnectionKind: "trino", Connection: "w", Entries: []string{"a"}},
		{ConnectionKind: "trino", Connection: "w", Entries: []string{"b.c"}},
		{ConnectionKind: "trino", Connection: "other", Entries: []string{"z"}},
		{ConnectionKind: "api", Connection: "w", Entries: []string{"y"}},
	}}
	x := s.ExclusionsFor("trino", "w")
	assert.True(t, x.Excludes("a"))
	assert.True(t, x.Excludes("b", "c"))
	assert.False(t, x.Excludes("z"))
	assert.False(t, x.Excludes("y"))
}

func newStoreMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

func TestStoreGetAndSet(t *testing.T) {
	ctx := context.Background()
	s, mock := newStoreMock(t)
	mock.ExpectQuery("FROM platform_settings").WithArgs(SettingsSection).WillReturnRows(sqlmock.NewRows([]string{"value"}))
	got, err := s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, Defaults(), got, "a deployment that never saved settings reads the defaults")

	now := time.Now()
	mock.ExpectQuery("FROM platform_settings").WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
		AddRow([]byte(`{"scan_interval":"2h","exclusions":null}`), "ana", now))
	got, err = s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "2h", got.ScanInterval)
	assert.NotNil(t, got.Exclusions, "an empty list is [] not null")
	assert.Equal(t, "ana", got.UpdatedBy)

	mock.ExpectQuery("FROM platform_settings").WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
		AddRow([]byte(`{`), "ana", now))
	_, err = s.Get(ctx)
	assert.Error(t, err)

	mock.ExpectQuery("FROM platform_settings").WillReturnError(errBoom)
	_, err = s.Get(ctx)
	assert.ErrorIs(t, err, errBoom)

	in := Settings{ScanInterval: "3h", Exclusions: []Exclusion{}, UpdatedBy: "ignored"}
	mock.ExpectExec("INSERT INTO platform_settings").
		WithArgs(SettingsSection, []byte(`{"scan_interval":"3h","exclusions":[]}`), "ben").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Set(ctx, in, "ben"))

	assert.Error(t, s.Set(ctx, Settings{ScanInterval: "x"}, "ben"))

	mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errBoom)
	assert.ErrorIs(t, s.Set(ctx, Defaults(), "ben"), errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreOutcomes(t *testing.T) {
	ctx := context.Background()
	s, mock := newStoreMock(t)
	now := time.Now()
	o := Outcome{ConnectionKind: "trino", Connection: "w", StartedAt: now, FinishedAt: now, Datasets: 3, Removed: 1, Error: ""}
	mock.ExpectExec("INSERT INTO catalog_scans").WithArgs("trino", "w", now, now, 3, 1, "", []byte(`[]`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordOutcome(ctx, o), "an outcome with no unread parts is stored as []")
	withUnread := o
	withUnread.Unread = []Unread{{Path: "pg", Error: "down"}}
	mock.ExpectExec("INSERT INTO catalog_scans").
		WithArgs("trino", "w", now, now, 3, 1, "", []byte(`[{"path":"pg","error":"down"}]`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordOutcome(ctx, withUnread))
	mock.ExpectExec("INSERT INTO catalog_scans").WillReturnError(errBoom)
	assert.ErrorIs(t, s.RecordOutcome(ctx, o), errBoom)

	cols := []string{"connection_kind", "connection_name", "started_at", "finished_at", "datasets", "removed", "error", "unread"}
	mock.ExpectQuery("FROM catalog_scans").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("trino", "w", now, now, 3, 1, "", []byte(`[{"path":"pg","error":"down"}]`)).
		AddRow("trino", "v", now, now, 0, 0, "", []byte(`[]`)))
	list, err := s.Outcomes(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, 3, list[0].Datasets)
	assert.Equal(t, []Unread{{Path: "pg", Error: "down"}}, list[0].Unread)
	assert.Equal(t, []Unread{}, list[1].Unread)

	mock.ExpectQuery("FROM catalog_scans").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("trino", "w", now, now, 3, 1, "", []byte(`not json`)))
	_, err = s.Outcomes(ctx)
	assert.Error(t, err, "a stored unread list that does not decode is an error")

	mock.ExpectQuery("FROM catalog_scans").WillReturnRows(sqlmock.NewRows(cols))
	list, err = s.Outcomes(ctx)
	require.NoError(t, err)
	assert.NotNil(t, list)

	mock.ExpectQuery("FROM catalog_scans").WillReturnError(errBoom)
	_, err = s.Outcomes(ctx)
	assert.ErrorIs(t, err, errBoom)

	mock.ExpectQuery("FROM catalog_scans").WillReturnRows(sqlmock.NewRows([]string{"connection_kind"}).AddRow("x"))
	_, err = s.Outcomes(ctx)
	assert.Error(t, err)

	mock.ExpectQuery("FROM catalog_scans").WillReturnRows(sqlmock.NewRows(cols).RowError(0, errBoom).
		AddRow("trino", "w", now, now, 3, 1, "", []byte(`[]`)))
	_, err = s.Outcomes(ctx)
	assert.ErrorIs(t, err, errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}
