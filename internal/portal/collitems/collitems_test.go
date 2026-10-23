package collitems

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// These hold the order of the append's statements and its refusals; what the
// stored rows look like afterwards is collection_items_realdb_integration_test
// in portalstore, against a real database.

func newMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

var errDB = errors.New("db down")

func lockOK(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM portal_collections WHERE id = \\$1 AND deleted_at IS NULL FOR UPDATE").
		WithArgs("c1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("c1"))
}

func itemsRow(count, next int, present bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"count", "next", "present"}).AddRow(count, next, present)
}

func TestAppendItem_NamedSection(t *testing.T) {
	db, mock := newMock(t)
	lockOK(mock)
	mock.ExpectQuery("SELECT id, title FROM portal_collection_sections WHERE id = \\$1 AND collection_id = \\$2").
		WithArgs("s1", "c1").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s1", "Summary"))
	mock.ExpectQuery("FROM portal_collection_items WHERE section_id").WithArgs("s1", "a1").WillReturnRows(itemsRow(2, 2, false))
	mock.ExpectExec("INSERT INTO portal_collection_items").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE portal_collections SET updated_at").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	got, err := AppendItem(context.Background(), db, "c1", "s1", "a1")
	require.NoError(t, err)
	assert.Equal(t, "Summary", got.SectionTitle)
	assert.Equal(t, 2, got.Item.Position)
	assert.Equal(t, "a1", got.Item.AssetID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendItem_LastSectionOrANewOne(t *testing.T) {
	db, mock := newMock(t)
	lockOK(mock)
	mock.ExpectQuery("ORDER BY position DESC").WithArgs("c1").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s9", "Appendix"))
	mock.ExpectQuery("FROM portal_collection_items").WillReturnRows(itemsRow(0, 0, false))
	mock.ExpectExec("INSERT INTO portal_collection_items").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE portal_collections").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	got, err := AppendItem(context.Background(), db, "c1", "", "a1")
	require.NoError(t, err)
	assert.Equal(t, "s9", got.SectionID)

	db2, mock2 := newMock(t)
	lockOK(mock2)
	mock2.ExpectQuery("ORDER BY position DESC").WillReturnError(sql.ErrNoRows)
	mock2.ExpectExec("INSERT INTO portal_collection_sections").WillReturnResult(sqlmock.NewResult(1, 1))
	mock2.ExpectQuery("FROM portal_collection_items").WillReturnRows(itemsRow(0, 0, false))
	mock2.ExpectExec("INSERT INTO portal_collection_items").WillReturnResult(sqlmock.NewResult(1, 1))
	mock2.ExpectExec("UPDATE portal_collections").WillReturnResult(sqlmock.NewResult(0, 1))
	mock2.ExpectCommit()
	got, err = AppendItem(context.Background(), db2, "c1", "", "a1")
	require.NoError(t, err)
	assert.NotEmpty(t, got.SectionID)
	assert.Empty(t, got.SectionTitle)
	require.NoError(t, mock2.ExpectationsWereMet())
}

func TestAppendItem_Refusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
		want  error
		text  string
	}{
		{"begin fails", func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(errDB) }, nil, "beginning transaction"},
		{"no collection", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("FOR UPDATE").WillReturnError(sql.ErrNoRows)
		}, ErrCollectionNotFound, ""},
		{"lock fails", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("FOR UPDATE").WillReturnError(errDB)
		}, nil, "locking collection"},
		{"section gone", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnError(sql.ErrNoRows)
		}, portaldomain.ErrSectionNotFound, ""},
		{"section read fails", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnError(errDB)
		}, nil, "reading section"},
		{"duplicate", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s1", "S"))
			m.ExpectQuery("FROM portal_collection_items").WillReturnRows(itemsRow(1, 1, true))
		}, portaldomain.ErrItemAlreadyInSection, ""},
		{"full", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s1", "S"))
			m.ExpectQuery("FROM portal_collection_items").WillReturnRows(itemsRow(portaldomain.MaxItemsPerSection, 100, false))
		}, portaldomain.ErrSectionFull, ""},
		{"items read fails", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s1", "S"))
			m.ExpectQuery("FROM portal_collection_items").WillReturnError(errDB)
		}, nil, "reading section items"},
		{"insert fails", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s1", "S"))
			m.ExpectQuery("FROM portal_collection_items").WillReturnRows(itemsRow(0, 0, false))
			m.ExpectExec("INSERT INTO portal_collection_items").WillReturnError(errDB)
		}, nil, "inserting item"},
		{"touch fails", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s1", "S"))
			m.ExpectQuery("FROM portal_collection_items").WillReturnRows(itemsRow(0, 0, false))
			m.ExpectExec("INSERT INTO portal_collection_items").WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectExec("UPDATE portal_collections").WillReturnError(errDB)
		}, nil, "touching collection"},
		{"commit fails", func(m sqlmock.Sqlmock) {
			lockOK(m)
			m.ExpectQuery("WHERE id = \\$1 AND collection_id").WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).AddRow("s1", "S"))
			m.ExpectQuery("FROM portal_collection_items").WillReturnRows(itemsRow(0, 0, false))
			m.ExpectExec("INSERT INTO portal_collection_items").WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectExec("UPDATE portal_collections").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectCommit().WillReturnError(errDB)
		}, nil, "committing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newMock(t)
			tc.setup(mock)
			_, err := AppendItem(ctx, db, "c1", "s1", "a1")
			require.Error(t, err)
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
			} else {
				assert.ErrorContains(t, err, tc.text)
			}
		})
	}

	db, mock := newMock(t)
	lockOK(mock)
	mock.ExpectQuery("ORDER BY position DESC").WillReturnError(errDB)
	_, err := AppendItem(ctx, db, "c1", "", "a1")
	assert.ErrorContains(t, err, "reading last section")

	db, mock = newMock(t)
	lockOK(mock)
	mock.ExpectQuery("ORDER BY position DESC").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO portal_collection_sections").WillReturnError(errDB)
	_, err = AppendItem(ctx, db, "c1", "", "a1")
	assert.ErrorContains(t, err, "inserting first section")
}

func TestSectionSummaries(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery("FROM portal_collection_sections s").
		WillReturnRows(sqlmock.NewRows([]string{"collection_id", "id", "title", "count", "has"}).
			AddRow("c1", "s1", "Summary", 2, true).AddRow("c1", "s2", "Appendix", 0, false).AddRow("c2", "s3", "", 1, false))
	got, err := SectionSummaries(context.Background(), db, []string{"c1", "c2"}, "a1")
	require.NoError(t, err)
	assert.Len(t, got["c1"], 2)
	assert.True(t, got["c1"][0].HasAsset)
	assert.Equal(t, 1, got["c2"][0].ItemCount)

	empty, err := SectionSummaries(context.Background(), db, nil, "a1")
	require.NoError(t, err)
	assert.Empty(t, empty)

	mock.ExpectQuery("FROM portal_collection_sections s").WillReturnError(errDB)
	_, err = SectionSummaries(context.Background(), db, []string{"c1"}, "a1")
	assert.ErrorContains(t, err, "querying section summaries")

	mock.ExpectQuery("FROM portal_collection_sections s").
		WillReturnRows(sqlmock.NewRows([]string{"collection_id"}).AddRow("c1"))
	_, err = SectionSummaries(context.Background(), db, []string{"c1"}, "a1")
	assert.ErrorContains(t, err, "scanning section summary")

	mock.ExpectQuery("FROM portal_collection_sections s").
		WillReturnRows(sqlmock.NewRows([]string{"collection_id", "id", "title", "count", "has"}).AddRow("c1", "s1", "S", 1, false).RowError(0, errDB))
	_, err = SectionSummaries(context.Background(), db, []string{"c1"}, "a1")
	assert.ErrorContains(t, err, "iterating section summaries")
}
