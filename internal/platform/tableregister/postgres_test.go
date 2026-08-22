package tableregister

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var registeredAt = time.Date(2026, 8, 20, 14, 12, 0, 0, time.UTC)

// registrationRows builds the result set the store scans, in the column order
// selectColumns declares.
func registrationRows(rows ...[]driver.Value) *sqlmock.Rows {
	out := sqlmock.NewRows([]string{
		"id", "source_kind", "source_id", "connection_name", "catalog_name",
		"schema_name", "table_name", "location", "columns", "registered_by", "registered_at",
	})
	for _, r := range rows {
		out.AddRow(r...)
	}
	return out
}

func assetRow(id, sourceID, table string) []driver.Value {
	return []driver.Value{
		id, KindAsset, sourceID, "scratch", "scratch", "uploads", table,
		"s3://portal-assets/artifacts/u1/" + sourceID + "/",
		[]byte(`[{"name":"store_id","type":"VARCHAR"}]`),
		"alice@example.com", registeredAt,
	}
}

func TestPostgresStore_InsertAndScan(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectExec("INSERT INTO table_registrations").
		WithArgs("reg_1", KindAsset, "asset_1", "scratch", "scratch", "uploads",
			"analyst_keys", "s3://b/d/", []byte(`[{"name":"id","type":"VARCHAR"}]`),
			"alice@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Insert(context.Background(), Registration{
		ID: "reg_1", SourceKind: KindAsset, SourceID: "asset_1",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_keys", Location: "s3://b/d/",
		Columns:      []Column{{Name: "id", Type: "VARCHAR"}},
		RegisteredBy: "alice@example.com",
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_InsertEncodesNoColumnsAsAnArray pins that an empty column
// list is written as [] rather than null: the column is NOT NULL with a []
// default, and null would be a value that default exists to avoid.
func TestPostgresStore_InsertEncodesNoColumnsAsAnArray(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectExec("INSERT INTO table_registrations").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			[]byte(`[]`), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, NewPostgresStore(db).Insert(context.Background(), Registration{ID: "reg_1"}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_InsertNameCollision pins the race between the registrar's
// "is this name free" check and this write. It must not surface as a bare
// constraint violation.
func TestPostgresStore_InsertNameCollision(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectExec("INSERT INTO table_registrations").
		WillReturnError(&pq.Error{Code: uniqueViolation})

	err = NewPostgresStore(db).Insert(context.Background(), Registration{ID: "reg_1"})
	assert.ErrorIs(t, err, ErrNameTaken)
}

func TestPostgresStore_Get(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectQuery("SELECT .* FROM table_registrations WHERE id = ").
		WithArgs("reg_1").
		WillReturnRows(registrationRows(assetRow("reg_1", "asset_1", "analyst_keys")))

	reg, err := store.Get(context.Background(), "reg_1")
	require.NoError(t, err)
	assert.Equal(t, "scratch.uploads.analyst_keys", reg.QualifiedName())
	assert.Equal(t, []Column{{Name: "store_id", Type: "VARCHAR"}}, reg.Columns)
	assert.Equal(t, registeredAt, reg.RegisteredAt.UTC())

	mock.ExpectQuery("SELECT .* FROM table_registrations WHERE id = ").
		WithArgs("missing").
		WillReturnRows(registrationRows())
	_, err = store.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestPostgresStore_ByNameFreeNameIsNotAnError: the caller is asking whether
// the name is taken, and "no" is an answer rather than a failure.
func TestPostgresStore_ByName(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectQuery("SELECT .* FROM table_registrations").
		WithArgs("scratch", "scratch", "uploads", "analyst_keys").
		WillReturnRows(registrationRows(assetRow("reg_1", "asset_1", "analyst_keys")))
	reg, err := store.ByName(context.Background(), "scratch", "scratch", "uploads", "analyst_keys")
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.Equal(t, "alice@example.com", reg.RegisteredBy)

	mock.ExpectQuery("SELECT .* FROM table_registrations").
		WillReturnRows(registrationRows())
	reg, err = store.ByName(context.Background(), "scratch", "scratch", "uploads", "free")
	require.NoError(t, err)
	assert.Nil(t, reg)
}

func TestPostgresStore_BySource(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("SELECT .* FROM table_registrations").
		WithArgs(KindAsset, "asset_1").
		WillReturnRows(registrationRows(
			assetRow("reg_1", "asset_1", "analyst_keys"),
			assetRow("reg_2", "asset_1", "analyst_keys_dev"),
		))

	regs, err := NewPostgresStore(db).BySource(context.Background(), KindAsset, "asset_1")
	require.NoError(t, err)
	assert.Len(t, regs, 2)
}

// TestPostgresStore_ForSources is the batch read a page of search hits uses:
// one query for the page, keyed back by source id.
func TestPostgresStore_ForSources(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectQuery("SELECT .* FROM table_registrations").
		WithArgs(KindAsset, pq.Array([]string{"asset_1", "asset_2"})).
		WillReturnRows(registrationRows(
			assetRow("reg_1", "asset_1", "analyst_a"),
			assetRow("reg_2", "asset_2", "analyst_b"),
			assetRow("reg_3", "asset_1", "analyst_c"),
		))

	got, err := store.ForSources(context.Background(), KindAsset, []string{"asset_1", "asset_2"})
	require.NoError(t, err)
	assert.Len(t, got["asset_1"], 2)
	assert.Len(t, got["asset_2"], 1)

	// No ids means no query at all, not a query matching everything.
	empty, err := store.ForSources(context.Background(), KindAsset, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Delete pins that a delete matching nothing is reported:
// the caller named a specific registration, and silence would read as success
// on an id that was never there.
func TestPostgresStore_Delete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	mock.ExpectExec("DELETE FROM table_registrations").
		WithArgs("reg_1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Delete(context.Background(), "reg_1"))

	mock.ExpectExec("DELETE FROM table_registrations").
		WithArgs("gone").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, store.Delete(context.Background(), "gone"), ErrNotFound)
}

func TestPostgresStore_ReadErrorsAreWrapped(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresStore(db)
	boom := errors.New("connection refused")

	mock.ExpectQuery("SELECT .* FROM table_registrations").WillReturnError(boom)
	_, err = store.Get(context.Background(), "reg_1")
	assert.ErrorIs(t, err, boom)

	mock.ExpectQuery("SELECT .* FROM table_registrations").WillReturnError(boom)
	_, err = store.BySource(context.Background(), KindAsset, "asset_1")
	assert.ErrorIs(t, err, boom)

	mock.ExpectExec("DELETE FROM table_registrations").WillReturnError(boom)
	assert.ErrorIs(t, store.Delete(context.Background(), "reg_1"), boom)
}
