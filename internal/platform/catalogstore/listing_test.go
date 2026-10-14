package catalogstore

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var definitionCols = []string{"reference", "kind", "slug", "name", "description", "parent", "children"}

var resolveCols = []string{"id", "kind", "reference"}

func TestListDefinitions(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	_, err := s.ListDefinitions(ctx, catalog.DefinitionQuery{Kinds: []catalog.Kind{catalog.KindDataset}})
	require.ErrorIs(t, err, catalog.ErrInvalidRef, "a dataset is not a definition")
	_, err = s.ListDefinitions(ctx, catalog.DefinitionQuery{Parent: "not a reference"})
	require.ErrorIs(t, err, catalog.ErrInvalidRef)
	_, err = s.ListDefinitions(ctx, catalog.DefinitionQuery{Parent: "mcp:tag:pii"})
	require.ErrorIs(t, err, catalog.ErrInvalidRef, "only a glossary node has children")

	mock.ExpectQuery("FROM catalog_entities").WithArgs("glossary_node", "mcp:glossary_node:finance", "finance").
		WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("n1", "glossary_node", "mcp:glossary_node:n1"))
	mock.ExpectQuery("FROM catalog_entities e").
		WithArgs(`{"glossary_term"}`, "mcp:glossary_node:n1", false, `rev\_`, 5, 2).
		WillReturnRows(sqlmock.NewRows(definitionCols).
			AddRow("mcp:glossary_term:t1", "glossary_term", "revenue", "Revenue", "Money", "mcp:glossary_node:n1", 0))
	got, err := s.ListDefinitions(ctx, catalog.DefinitionQuery{
		Kinds: []catalog.Kind{catalog.KindGlossaryTerm}, Parent: "mcp:glossary_node:finance", Roots: true,
		Text: "rev_", Limit: 5, Offset: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, []catalog.DefinitionSummary{{
		Reference: "mcp:glossary_term:t1", Kind: catalog.KindGlossaryTerm, Slug: "revenue", Name: "Revenue",
		Description: "Money", Parent: "mcp:glossary_node:n1",
	}}, got, "a parent overrides roots")

	mock.ExpectQuery("FROM catalog_entities e").
		WithArgs(`{"glossary_term","glossary_node","tag","domain","data_product"}`, "", true, "", defaultSearchLimit, 0).
		WillReturnRows(sqlmock.NewRows(definitionCols))
	got, err = s.ListDefinitions(ctx, catalog.DefinitionQuery{Roots: true, Offset: -1})
	require.NoError(t, err)
	assert.Equal(t, []catalog.DefinitionSummary{}, got)

	mock.ExpectQuery("FROM catalog_entities e").WillReturnError(errDB)
	_, err = s.ListDefinitions(ctx, catalog.DefinitionQuery{})
	require.ErrorIs(t, err, errDB)
	mock.ExpectQuery("FROM catalog_entities e").WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow("x"))
	_, err = s.ListDefinitions(ctx, catalog.DefinitionQuery{})
	require.Error(t, err)
	mock.ExpectQuery("FROM catalog_entities e").WillReturnRows(sqlmock.NewRows(definitionCols).
		AddRow("r", "tag", "s", "n", "", "", 0).RowError(0, errDB))
	_, err = s.ListDefinitions(ctx, catalog.DefinitionQuery{})
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteDefinition(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	tag := catalog.KeyRef(catalog.KindTag, "pii")

	require.ErrorIs(t, s.DeleteDefinition(ctx, dsRef), catalog.ErrInvalidRef)

	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols))
	mock.ExpectRollback()
	require.ErrorIs(t, s.DeleteDefinition(ctx, tag), catalog.ErrNotFound)

	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("t1", "tag", "mcp:tag:pii"))
	mock.ExpectQuery("SELECT count").WithArgs("t1").WillReturnError(errDB)
	mock.ExpectRollback()
	require.ErrorIs(t, s.DeleteDefinition(ctx, tag), errDB)

	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("t1", "tag", "mcp:tag:pii"))
	mock.ExpectQuery("SELECT count").WithArgs("t1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT count").WithArgs("tags", []byte(`["pii"]`)).WillReturnError(errDB)
	mock.ExpectRollback()
	require.ErrorIs(t, s.DeleteDefinition(ctx, tag), errDB)

	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("t1", "tag", "mcp:tag:pii"))
	mock.ExpectQuery("SELECT count").WithArgs("t1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT count").WithArgs("tags", []byte(`["pii"]`)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectRollback()
	err := s.DeleteDefinition(ctx, tag)
	require.ErrorIs(t, err, catalog.ErrInUse)
	assert.Equal(t, "mcp:tag:pii is used by 3 datasets or columns; remove it from them first", err.Error())

	node := catalog.KeyRef(catalog.KindGlossaryNode, "finance")
	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("n1", "glossary_node", "mcp:glossary_node:n1"))
	mock.ExpectQuery("SELECT count").WithArgs("n1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectRollback()
	err = s.DeleteDefinition(ctx, node)
	require.ErrorIs(t, err, catalog.ErrInUse)
	assert.Equal(t, "mcp:glossary_node:n1 has 2 terms or nodes under it; move or delete them first", err.Error())

	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("n1", "glossary_node", "mcp:glossary_node:n1"))
	mock.ExpectQuery("SELECT count").WithArgs("n1").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM catalog_entities").WithArgs("n1").WillReturnError(errDB)
	mock.ExpectRollback()
	require.ErrorIs(t, s.DeleteDefinition(ctx, node), errDB, "a node is carried by nothing, so only its children are counted")

	term := catalog.KeyRef(catalog.KindGlossaryTerm, "revenue")
	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("t9", "glossary_term", "mcp:glossary_term:t9"))
	mock.ExpectQuery("SELECT count").WithArgs("t9").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT count").WithArgs("glossary_terms", []byte(`["mcp:glossary_term:t9"]`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("DELETE FROM catalog_entities").WithArgs("t9").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.DeleteDefinition(ctx, term))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUses(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	useCols := []string{"reference", "dataset", "name", "column_name", "connection_name"}
	domain := catalog.KeyRef(catalog.KindDomain, "sales")

	mock.ExpectQuery("FROM catalog_entities").WillReturnError(errDB)
	_, err := s.Uses(ctx, domain, 0)
	require.ErrorIs(t, err, errDB)

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("n1", "glossary_node", "mcp:glossary_node:n1"))
	got, err := s.Uses(ctx, catalog.KeyRef(catalog.KindGlossaryNode, "n1"), 0)
	require.NoError(t, err)
	assert.Equal(t, []catalog.Use{}, got, "a glossary node is carried by nothing")

	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("d1", "domain", "mcp:domain:d1"))
	mock.ExpectQuery("FROM catalog_aspects a").WithArgs("domain", []byte(`"mcp:domain:d1"`), defaultSearchLimit).
		WillReturnRows(sqlmock.NewRows(useCols).AddRow(dsRef.String(), dsRef.String(), "t", "", "w"))
	got, err = s.Uses(ctx, domain, 0)
	require.NoError(t, err)
	assert.Equal(t, []catalog.Use{{Reference: dsRef.String(), Dataset: dsRef.String(), Name: "t", Connection: "w"}}, got)

	for _, rows := range []*sqlmock.Rows{
		sqlmock.NewRows([]string{"reference"}).AddRow("x"),
		sqlmock.NewRows(useCols).AddRow("a", "b", "c", "", "w").RowError(0, errDB),
	} {
		mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("d1", "domain", "mcp:domain:d1"))
		mock.ExpectQuery("FROM catalog_aspects a").WillReturnRows(rows)
		_, err = s.Uses(ctx, domain, 3)
		require.Error(t, err)
	}
	mock.ExpectQuery("FROM catalog_entities").WillReturnRows(sqlmock.NewRows(resolveCols).AddRow("d1", "domain", "mcp:domain:d1"))
	mock.ExpectQuery("FROM catalog_aspects a").WillReturnError(errDB)
	_, err = s.Uses(ctx, domain, 3)
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBrowse(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	cols := []string{"segment", "leaf", "count", "reference", "dataset_type"}

	_, err := s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino"})
	require.ErrorIs(t, err, catalog.ErrInvalidRef)

	// The prefix's underscore is escaped for LIKE, and the path under it
	// starts after the prefix and its dot.
	mock.ExpectQuery("FROM catalog_entities").WithArgs("trino", "w", `c.s\_x.%`, len("c.s_x")+2).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(`"a.b"`, false, 3, "mcp:dataset:(trino,w,c.s_x.\"a.b\".t)", "table").
			AddRow("t", true, 1, "mcp:dataset:(trino,w,c.s_x.t)", "view"))
	got, err := s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "w", Prefix: []string{"c", "s_x"}})
	require.NoError(t, err)
	assert.Equal(t, []catalog.BrowseEntry{
		{Name: "a.b", Reference: `mcp:dataset:(trino,w,c.s_x."a.b")`, Datasets: 3},
		{Name: "t", Reference: "mcp:dataset:(trino,w,c.s_x.t)", Type: catalog.DatasetView},
	}, got, "a quoted segment is one group, named unquoted")

	mock.ExpectQuery("FROM catalog_entities").WithArgs("trino", "w", "", 1).WillReturnRows(sqlmock.NewRows(cols))
	got, err = s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "w"})
	require.NoError(t, err)
	assert.Equal(t, []catalog.BrowseEntry{}, got, "an empty level is [], never null")

	mock.ExpectQuery("FROM catalog_entities").WillReturnError(errDB)
	_, err = s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "w"})
	require.ErrorIs(t, err, errDB)
	for _, rows := range []*sqlmock.Rows{
		sqlmock.NewRows([]string{"segment"}).AddRow("x"),
		sqlmock.NewRows(cols).AddRow(`"unterminated`, true, 1, "r", "table"),
		sqlmock.NewRows(cols).AddRow("a", true, 1, "r", "table").RowError(0, errDB),
	} {
		mock.ExpectQuery("FROM catalog_entities").WillReturnRows(rows)
		_, err = s.Browse(ctx, catalog.BrowseQuery{ConnectionKind: "trino", Connection: "w"})
		require.Error(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
