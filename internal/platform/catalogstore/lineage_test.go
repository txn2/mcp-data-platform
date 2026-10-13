package catalogstore

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

func TestMarkRemoved(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("SET removed_at = NOW()").
		WithArgs("trino", "w", `{"table","view"}`, `{"mcp:dataset:(trino,w,a.b.c)"}`,
			`{"dead","my_cat"}`, `{"dead.%","my\\_cat.%"}`).
		WillReturnResult(sqlmock.NewResult(0, 3))
	n, err := s.MarkRemoved(context.Background(), catalog.Removal{
		ConnectionKind: "trino", Connection: "w",
		Types:  []catalog.DatasetType{catalog.DatasetTable, catalog.DatasetView},
		Keep:   []string{"mcp:dataset:(trino,w,a.b.c)"},
		Unread: []string{"dead", "my_cat"},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	mock.ExpectExec("SET removed_at").WithArgs("trino", "w", `{}`, `{}`, `{}`, `{}`).WillReturnError(errDB)
	_, err = s.MarkRemoved(context.Background(), catalog.Removal{ConnectionKind: "trino", Connection: "w"})
	assert.ErrorIs(t, err, errDB, "nil lists are sent as empty arrays")

	mock.ExpectExec("SET removed_at").WillReturnResult(sqlmock.NewErrorResult(errDB))
	_, err = s.MarkRemoved(context.Background(), catalog.Removal{ConnectionKind: "trino", Connection: "w"})
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddEdges(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO catalog_lineage").WithArgs("a", "b", "sql", "call-1", "ana").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err := s.AddEdges(context.Background(), []catalog.Edge{
		{Source: "a", Target: "a", WriterKind: catalog.WriterSQL},
		{Source: "a", Target: "b", WriterKind: catalog.WriterSQL, WriterRef: "call-1", WrittenBy: "ana"},
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO catalog_lineage").WillReturnError(errDB)
	mock.ExpectRollback()
	err = s.AddEdges(context.Background(), []catalog.Edge{{Source: "a", Target: "b"}})
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLineage(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	cols := []string{"source_ref", "target_ref", "writer_kind", "writer_ref", "written_by", "written_at", "depth"}
	mock.ExpectQuery("WHERE target_ref = \\$1").WithArgs("t", 2).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("s", "t", "script", "mcp:script:1", "ana", time.Now(), 1))
	hops, err := s.Lineage(ctx, "t", catalog.Upstream, 2)
	require.NoError(t, err)
	require.Len(t, hops, 1)
	assert.Equal(t, catalog.WriterScript, hops[0].WriterKind)

	mock.ExpectQuery("WHERE source_ref = \\$1").WithArgs("s", 1).WillReturnRows(sqlmock.NewRows(cols))
	hops, err = s.Lineage(ctx, "s", catalog.Downstream, 0)
	require.NoError(t, err)
	assert.NotNil(t, hops)
	assert.Empty(t, hops)

	_, err = s.Lineage(ctx, "s", "sideways", 1)
	assert.Error(t, err)

	mock.ExpectQuery("WITH RECURSIVE").WillReturnError(errDB)
	_, err = s.Lineage(ctx, "s", catalog.Upstream, 1)
	assert.ErrorIs(t, err, errDB)

	mock.ExpectQuery("WITH RECURSIVE").WillReturnRows(sqlmock.NewRows([]string{"source_ref"}).AddRow("s"))
	_, err = s.Lineage(ctx, "s", catalog.Upstream, 1)
	assert.Error(t, err)

	mock.ExpectQuery("WITH RECURSIVE").WillReturnRows(sqlmock.NewRows(cols).RowError(0, errDB).
		AddRow("s", "t", "sql", "", "", time.Now(), 1))
	_, err = s.Lineage(ctx, "s", catalog.Upstream, 1)
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkDatasetRemoved(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("SET removed_at = NOW\\(\\), updated_at = NOW\\(\\)\\s+WHERE kind = 'dataset' AND reference").
		WithArgs(dsRef.String()).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.MarkDatasetRemoved(context.Background(), dsRef))
	mock.ExpectExec("WHERE kind = 'dataset' AND reference").WillReturnError(errDB)
	assert.ErrorIs(t, s.MarkDatasetRemoved(context.Background(), dsRef), errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}
