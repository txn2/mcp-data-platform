package catalogstore

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var searchCols = []string{
	"reference", "connection_kind", "connection_name", "path", "name", "dataset_type",
	"description", "tags", "domain",
}

func TestDatasetsByPath(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	mock.ExpectQuery("FROM catalog_entities").WithArgs("trino", "c.s.t").
		WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow("mcp:dataset:(trino,a,c.s.t)").AddRow("mcp:dataset:(trino,b,c.s.t)"))
	refs, err := s.DatasetsByPath(ctx, "trino", "c.s.t")
	require.NoError(t, err)
	assert.Equal(t, []string{"mcp:dataset:(trino,a,c.s.t)", "mcp:dataset:(trino,b,c.s.t)"}, refs)

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows([]string{"reference"}))
	refs, err = s.DatasetsByPath(ctx, "trino", "x")
	require.NoError(t, err)
	assert.Equal(t, []string{}, refs, "no match is an empty list")

	mock.ExpectQuery("FROM catalog_entities").WillReturnError(errDB)
	_, err = s.DatasetsByPath(ctx, "trino", "x")
	require.ErrorIs(t, err, errDB)

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows([]string{"reference", "extra"}).AddRow("a", "b"))
	_, err = s.DatasetsByPath(ctx, "trino", "x")
	require.Error(t, err)

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow("a").RowError(0, errDB))
	_, err = s.DatasetsByPath(ctx, "trino", "x")
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSearchDatasets(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	mock.ExpectQuery("FROM catalog_entities").
		WithArgs(`order\_lines`, "trino", `{"pii"}`, 10, 5).
		WillReturnRows(sqlmock.NewRows(searchCols).
			AddRow("mcp:dataset:(trino,w,c.s.order_lines)", "trino", "w", "c.s.order_lines", "order_lines", "table",
				"Lines", []byte(`["pii"]`), "Sales"))
	got, err := s.SearchDatasets(ctx, catalog.DatasetQuery{
		Text: "order_lines", ConnectionKind: "trino", Tags: []string{"pii"}, Limit: 10, Offset: 5,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, catalog.DatasetSummary{
		Reference: "mcp:dataset:(trino,w,c.s.order_lines)", ConnectionKind: "trino", Connection: "w",
		Path: "c.s.order_lines", Name: "order_lines", Type: catalog.DatasetTable,
		Description: "Lines", Tags: []string{"pii"}, Domain: "Sales",
	}, got[0])

	mock.ExpectQuery("FROM catalog_entities").WithArgs("", "", `{}`, defaultSearchLimit, 0).
		WillReturnRows(sqlmock.NewRows(searchCols))
	got, err = s.SearchDatasets(ctx, catalog.DatasetQuery{Text: "*", Offset: -3})
	require.NoError(t, err)
	assert.Equal(t, []catalog.DatasetSummary{}, got, "* matches everything, and no match is an empty list")

	mock.ExpectQuery("FROM catalog_entities").WithArgs(`50\%`, "", `{}`, defaultSearchLimit, 0).
		WillReturnRows(sqlmock.NewRows(searchCols))
	_, err = s.SearchDatasets(ctx, catalog.DatasetQuery{Text: "50%"})
	require.NoError(t, err, "a percent sign is matched as written")

	mock.ExpectQuery("FROM catalog_entities").WillReturnError(errDB)
	_, err = s.SearchDatasets(ctx, catalog.DatasetQuery{})
	require.ErrorIs(t, err, errDB)

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow("a"))
	_, err = s.SearchDatasets(ctx, catalog.DatasetQuery{})
	require.Error(t, err)

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(searchCols).
		AddRow("r", "trino", "w", "p", "n", "table", "", []byte(`not json`), ""))
	_, err = s.SearchDatasets(ctx, catalog.DatasetQuery{})
	require.Error(t, err)

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(searchCols).
		AddRow("r", "trino", "w", "p", "n", "table", "", []byte(`[]`), "").RowError(0, errDB))
	_, err = s.SearchDatasets(ctx, catalog.DatasetQuery{})
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}
