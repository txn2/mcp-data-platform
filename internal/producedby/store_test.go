package producedby

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) (Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgres(db), mock
}

func TestNewPostgresNilDB(t *testing.T) {
	assert.Nil(t, NewPostgres(nil), "no database records nothing rather than failing writes")
}

func TestRecord(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectExec("INSERT INTO content_producers").
		WithArgs(TargetAsset, "asset-1", KindScript, "script-1", "daily-sales", true, 1, 3).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Record(context.Background(), Write{
		TargetKind: TargetAsset, TargetID: "asset-1",
		Producer: Producer{Kind: KindScript, ID: "script-1", Label: "daily-sales"},
		Created:  true, Version: 3,
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestRecordUncountedWriteBindsZero pins the one write that advances a
// producer's version without advancing its count: an asset's version 1, which
// the create has already counted.
func TestRecordUncountedWriteBindsZero(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectExec("INSERT INTO content_producers").
		WithArgs(TargetAsset, "asset-1", KindSession, "sess-1", "", false, 0, 1).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Record(context.Background(), Write{
		TargetKind: TargetAsset, TargetID: "asset-1",
		Producer:  Producer{Kind: KindSession, ID: "sess-1"},
		Version:   1,
		Uncounted: true,
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordError(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectExec("INSERT INTO content_producers").WillReturnError(errors.New("boom"))
	err := store.Record(context.Background(), Write{TargetKind: TargetAsset, TargetID: "a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recording producer")
}

// producerRows builds the projection both listings scan.
func producerRows(at time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"target_kind", "target_id", "producer_kind", "producer_id", "producer_label",
		"created", "first_write_at", "last_write_at", "write_count", "last_version",
	}).AddRow(TargetAsset, "asset-1", KindScript, "script-1", "daily-sales", true, at, at, 4, 4)
}

func TestListByTarget(t *testing.T) {
	store, mock := newStore(t)
	at := time.Now().UTC().Truncate(time.Second)
	mock.ExpectQuery("FROM content_producers").
		WithArgs(TargetAsset, "asset-1").
		WillReturnRows(producerRows(at))

	rows, err := store.ListByTarget(context.Background(), TargetAsset, "asset-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "script-1", rows[0].Producer.ID)
	assert.Equal(t, "daily-sales", rows[0].Producer.Label)
	assert.True(t, rows[0].Created)
	assert.Equal(t, 4, rows[0].WriteCount)
	assert.Equal(t, 4, rows[0].LastVersion)
	assert.Equal(t, at, rows[0].LastWriteAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByTargetError(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("FROM content_producers").WillReturnError(errors.New("boom"))
	_, err := store.ListByTarget(context.Background(), TargetAsset, "asset-1")
	assert.Error(t, err)
}

// TestListByProducerDefaultLimit pins that a caller naming no limit gets the
// package default rather than an unbounded page.
func TestListByProducerDefaultLimit(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("FROM content_producers").
		WithArgs(KindScript, "script-1", DefaultProducerLimit).
		WillReturnRows(producerRows(time.Now().UTC()))

	rows, err := store.ListByProducer(context.Background(), KindScript, "script-1", 0)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByProducerExplicitLimit(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("FROM content_producers").
		WithArgs(KindScript, "script-1", 7).
		WillReturnRows(producerRows(time.Now().UTC()))

	_, err := store.ListByProducer(context.Background(), KindScript, "script-1", 7)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByProducerError(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("FROM content_producers").WillReturnError(errors.New("boom"))
	_, err := store.ListByProducer(context.Background(), KindScript, "script-1", 0)
	assert.Error(t, err)
}

// TestScanRowsBadShape covers the scan failure path: a projection that does not
// match is reported rather than yielding a half-filled row.
func TestScanRowsBadShape(t *testing.T) {
	store, mock := newStore(t)
	mock.ExpectQuery("FROM content_producers").
		WillReturnRows(sqlmock.NewRows([]string{"target_kind"}).AddRow(TargetAsset))
	_, err := store.ListByTarget(context.Background(), TargetAsset, "asset-1")
	assert.Error(t, err)
}

func TestScanRowsIterationError(t *testing.T) {
	store, mock := newStore(t)
	rows := producerRows(time.Now().UTC()).RowError(0, errors.New("cursor died"))
	mock.ExpectQuery("FROM content_producers").WillReturnRows(rows)
	_, err := store.ListByTarget(context.Background(), TargetAsset, "asset-1")
	assert.Error(t, err)
}
