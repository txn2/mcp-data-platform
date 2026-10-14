package catalogstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var errDB = errors.New("db down")

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

var dsRef = catalog.DatasetRef("trino", "w", "c", "s", "t")

func expectDatasetRead(mock sqlmock.Sqlmock) {
	now := time.Now()
	mock.ExpectQuery("FROM catalog_entities WHERE kind = 'dataset'").
		WithArgs(dsRef.String()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "dataset_type", "schema_resource_id", "schema_document", "removed_at", "created_at", "updated_at"}).
			AddRow("d1", "t", "table", "", "openapi.json", now, now, now))
	mock.ExpectQuery("kind = 'column' AND parent_id").WithArgs("d1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data_type", "ordinal", "nullable"}).
			AddRow("c1", "a", "varchar", 0, true).AddRow("c2", "b", "bigint", 1, false))
	mock.ExpectQuery("FROM catalog_aspects a JOIN").WithArgs("d1").
		WillReturnRows(sqlmock.NewRows([]string{"entity_id", "aspect", "value"}).
			AddRow("d1", "description", []byte(`"table desc"`)).
			AddRow("c2", "tags", []byte(`["pii"]`)))
}

func TestPutDataset(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO catalog_entities").
		WithArgs(dsRef.String(), "trino", "w", "c.s.t", "t", "table", "", "openapi.json").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("d1"))
	mock.ExpectExec("INSERT INTO catalog_entities").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO catalog_entities").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM catalog_entities").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	expectDatasetRead(mock)

	d, err := s.PutDataset(context.Background(), catalog.DatasetInput{
		Ref: dsRef, Type: catalog.DatasetTable, SchemaDocument: "openapi.json",
		Columns: []catalog.ColumnInput{{Name: "a", DataType: "varchar", Nullable: true}, {Name: "b", DataType: "bigint"}},
	})
	require.NoError(t, err)
	assert.Equal(t, `mcp:schema_document:(trino,w,"openapi.json")`, d.SchemaDocument,
		"the document is named under the dataset's own connection")
	assert.Equal(t, "table desc", d.Aspects.Description)
	require.NotNil(t, d.RemovedAt, "the removed time reads back")
	require.Len(t, d.Columns, 2)
	assert.Equal(t, []string{"pii"}, d.Columns[1].Aspects.Tags)
	assert.Empty(t, d.Columns[0].Aspects.Tags)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPutDatasetRefusesBadInput(t *testing.T) {
	s, _ := newMock(t)
	cases := map[string]catalog.DatasetInput{
		"column reference": {Ref: catalog.ColumnRef(dsRef, "a"), Type: catalog.DatasetTable},
		"empty reference":  {Ref: catalog.Ref{Kind: catalog.KindDataset}, Type: catalog.DatasetTable},
		"unknown type":     {Ref: dsRef, Type: "dashboard"},
		"unnamed column":   {Ref: dsRef, Type: catalog.DatasetTable, Columns: []catalog.ColumnInput{{}}},
		"column twice": {Ref: dsRef, Type: catalog.DatasetTable, Columns: []catalog.ColumnInput{
			{Name: "a"}, {Name: "a"},
		}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := s.PutDataset(context.Background(), in)
			assert.Error(t, err)
		})
	}
}

func TestPutDatasetFailures(t *testing.T) {
	in := catalog.DatasetInput{Ref: dsRef, Type: catalog.DatasetTable, Columns: []catalog.ColumnInput{{Name: "a"}}}
	steps := []func(sqlmock.Sqlmock){
		func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(errDB) },
		func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("INSERT INTO catalog_entities").WillReturnError(errDB)
			m.ExpectRollback()
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("INSERT INTO catalog_entities").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("d1"))
			m.ExpectExec("INSERT INTO catalog_entities").WillReturnError(errDB)
			m.ExpectRollback()
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("INSERT INTO catalog_entities").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("d1"))
			m.ExpectExec("INSERT INTO catalog_entities").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec("DELETE FROM catalog_entities").WillReturnError(errDB)
			m.ExpectRollback()
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("INSERT INTO catalog_entities").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("d1"))
			m.ExpectExec("INSERT INTO catalog_entities").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec("DELETE FROM catalog_entities").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectCommit().WillReturnError(errDB)
		},
	}
	for _, step := range steps {
		s, mock := newMock(t)
		step(mock)
		_, err := s.PutDataset(context.Background(), in)
		assert.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestGetDatasetFailures(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	_, err := s.GetDataset(ctx, catalog.KeyRef(catalog.KindTag, "x"))
	assert.ErrorIs(t, err, catalog.ErrInvalidRef)

	mock.ExpectQuery("kind = 'dataset'").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = s.GetDataset(ctx, dsRef)
	assert.ErrorIs(t, err, catalog.ErrNotFound)

	mock.ExpectQuery("kind = 'dataset'").WillReturnError(errDB)
	_, err = s.GetDataset(ctx, dsRef)
	assert.ErrorIs(t, err, errDB)

	now := time.Now()
	head := func() {
		mock.ExpectQuery("kind = 'dataset'").WillReturnRows(
			sqlmock.NewRows([]string{"id", "name", "dataset_type", "schema_resource_id", "schema_document", "removed_at", "created_at", "updated_at"}).
				AddRow("d1", "t", "table", "", "", nil, now, now))
	}
	head()
	mock.ExpectQuery("kind = 'column'").WillReturnError(errDB)
	_, err = s.GetDataset(ctx, dsRef)
	assert.ErrorIs(t, err, errDB)

	head()
	mock.ExpectQuery("kind = 'column'").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("c1"))
	_, err = s.GetDataset(ctx, dsRef)
	assert.Error(t, err, "a short column row fails to scan")

	head()
	mock.ExpectQuery("kind = 'column'").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "data_type", "ordinal", "nullable"}).RowError(0, errDB).
			AddRow("c1", "a", "", 0, true))
	_, err = s.GetDataset(ctx, dsRef)
	assert.ErrorIs(t, err, errDB)

	head()
	mock.ExpectQuery("kind = 'column'").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data_type", "ordinal", "nullable"}))
	mock.ExpectQuery("FROM catalog_aspects").WillReturnError(errDB)
	_, err = s.GetDataset(ctx, dsRef)
	assert.ErrorIs(t, err, errDB)

	head()
	mock.ExpectQuery("kind = 'column'").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data_type", "ordinal", "nullable"}))
	mock.ExpectQuery("FROM catalog_aspects").WillReturnRows(
		sqlmock.NewRows([]string{"entity_id", "aspect", "value"}).AddRow("d1", "tags", []byte(`"not a list"`)))
	_, err = s.GetDataset(ctx, dsRef)
	assert.ErrorIs(t, err, catalog.ErrInvalidAspect)

	head()
	mock.ExpectQuery("kind = 'column'").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data_type", "ordinal", "nullable"}))
	mock.ExpectQuery("FROM catalog_aspects").WillReturnRows(sqlmock.NewRows([]string{"entity_id"}).AddRow("d1"))
	_, err = s.GetDataset(ctx, dsRef)
	assert.Error(t, err)

	head()
	mock.ExpectQuery("kind = 'column'").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data_type", "ordinal", "nullable"}))
	mock.ExpectQuery("FROM catalog_aspects").WillReturnRows(
		sqlmock.NewRows([]string{"entity_id", "aspect", "value"}).RowError(0, errDB).AddRow("d1", "tags", []byte(`[]`)))
	_, err = s.GetDataset(ctx, dsRef)
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func definitionRows(id, ref, slug, name, parent string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{"id", "reference", "slug", "name", "parent", "created_at", "updated_at"}).
		AddRow(id, ref, slug, name, parent, now, now)
}

func TestPutDefinition(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	mock.ExpectQuery("SELECT id, kind, reference FROM catalog_entities").
		WithArgs("glossary_node", "mcp:glossary_node:n1", "n1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "kind", "reference"}).AddRow("n1", "glossary_node", "mcp:glossary_node:n1"))
	mock.ExpectQuery("INSERT INTO catalog_entities").
		WithArgs(sqlmock.AnyArg(), "glossary_term", sqlmock.AnyArg(), "net-revenue", "Net Revenue", "n1").
		WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow("mcp:glossary_term:t1"))
	mock.ExpectQuery("LEFT JOIN catalog_entities p").
		WithArgs("glossary_term", "mcp:glossary_term:net-revenue", "net-revenue").
		WillReturnRows(definitionRows("t1", "mcp:glossary_term:t1", "net-revenue", "Net Revenue", "mcp:glossary_node:n1"))
	mock.ExpectQuery("FROM catalog_aspects").WithArgs("t1").
		WillReturnRows(sqlmock.NewRows([]string{"entity_id", "aspect", "value"}).AddRow("t1", "description", []byte(`"d"`)))

	d, err := s.PutDefinition(ctx, catalog.DefinitionInput{
		Kind: catalog.KindGlossaryTerm, Name: "Net Revenue", Parent: "mcp:glossary_node:n1",
	})
	require.NoError(t, err)
	assert.Equal(t, "d", d.Aspects.Description)
	assert.Equal(t, "mcp:glossary_node:n1", d.Parent)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPutDefinitionTagIsItsName(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("INSERT INTO catalog_entities").
		WithArgs(sqlmock.AnyArg(), "tag", "mcp:tag:tier:gold", "tier:gold", "tier:gold", nil).
		WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow("mcp:tag:tier:gold"))
	mock.ExpectQuery("LEFT JOIN").WillReturnRows(definitionRows("g", "mcp:tag:tier:gold", "tier:gold", "tier:gold", ""))
	mock.ExpectQuery("FROM catalog_aspects").WillReturnRows(sqlmock.NewRows([]string{"entity_id", "aspect", "value"}))
	d, err := s.PutDefinition(context.Background(), catalog.DefinitionInput{Kind: catalog.KindTag, Name: "tier:gold", Slug: "ignored"})
	require.NoError(t, err)
	assert.Equal(t, "mcp:tag:tier:gold", d.Reference)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPutDefinitionRefuses(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	cases := []catalog.DefinitionInput{
		{Kind: catalog.KindDataset, Name: "x"},
		{Kind: catalog.KindDomain, Name: "  "},
		{Kind: catalog.KindDomain, Name: "---"},
		{Kind: catalog.KindDomain, Name: "Sales", Parent: "mcp:glossary_node:n"},
		{Kind: catalog.KindGlossaryTerm, Name: "x", Parent: "not a ref"},
		{Kind: catalog.KindGlossaryTerm, Name: "x", Parent: "mcp:tag:pii"},
	}
	for _, in := range cases {
		_, err := s.PutDefinition(ctx, in)
		assert.Error(t, err, in)
	}

	mock.ExpectQuery("SELECT id, kind, reference").WillReturnError(errDB)
	_, err := s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryNode, Name: "x", Parent: "mcp:glossary_node:n"})
	assert.ErrorIs(t, err, errDB)

	mock.ExpectQuery("INSERT INTO catalog_entities").WillReturnError(errDB)
	_, err = s.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindDomain, Name: "Sales"})
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDefinitionFailures(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	_, err := s.GetDefinition(ctx, dsRef)
	assert.ErrorIs(t, err, catalog.ErrInvalidRef)

	mock.ExpectQuery("LEFT JOIN").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = s.GetDefinition(ctx, catalog.KeyRef(catalog.KindDomain, "x"))
	assert.ErrorIs(t, err, catalog.ErrNotFound)

	mock.ExpectQuery("LEFT JOIN").WillReturnError(errDB)
	_, err = s.GetDefinition(ctx, catalog.KeyRef(catalog.KindDomain, "x"))
	assert.ErrorIs(t, err, errDB)

	mock.ExpectQuery("LEFT JOIN").WillReturnRows(definitionRows("x", "mcp:domain:x", "x", "X", ""))
	mock.ExpectQuery("FROM catalog_aspects").WillReturnError(errDB)
	_, err = s.GetDefinition(ctx, catalog.KeyRef(catalog.KindDomain, "x"))
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectResolveDataset(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT id, kind, reference FROM catalog_entities WHERE reference").
		WithArgs(dsRef.String()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "kind", "reference"}).AddRow("d1", "dataset", dsRef.String()))
}

func TestWriteAspect(t *testing.T) {
	s, mock := newMock(t)
	now := time.Now()
	mock.ExpectBegin()
	expectResolveDataset(mock)
	mock.ExpectExec("FOR UPDATE").WithArgs("d1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM catalog_aspects").WithArgs("d1", "description").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow([]byte(`"old"`)))
	mock.ExpectQuery("MAX\\(version\\)").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(4))
	mock.ExpectExec("INSERT INTO catalog_aspects").
		WithArgs("d1", "description", []byte(`"new"`), 5, "ana").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO catalog_aspect_history").
		WithArgs("d1", "description", 5, []byte(`"old"`), []byte(`"new"`), "ana", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id", "written_at"}).AddRow("h5", now))
	mock.ExpectCommit()

	c, err := s.WriteAspect(context.Background(), dsRef, catalog.AspectDescription, json.RawMessage(`"new"`), "ana")
	require.NoError(t, err)
	assert.Equal(t, "h5", c.ID)
	assert.Equal(t, 5, c.Version)
	assert.JSONEq(t, `"old"`, string(c.Previous))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWriteAspectClears(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectResolveDataset(mock)
	mock.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM catalog_aspects").WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectQuery("MAX\\(version\\)").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(0))
	mock.ExpectExec("DELETE FROM catalog_aspects").WithArgs("d1", "tags").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("INSERT INTO catalog_aspect_history").
		WithArgs("d1", "tags", 1, nil, nil, "ana", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id", "written_at"}).AddRow("h1", time.Now()))
	mock.ExpectCommit()

	c, err := s.WriteAspect(context.Background(), dsRef, catalog.AspectTags, nil, "ana")
	require.NoError(t, err)
	assert.Nil(t, c.Value)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWriteAspectFailures(t *testing.T) {
	ctx := context.Background()
	lead := func(m sqlmock.Sqlmock) {
		m.ExpectBegin()
		expectResolveDataset(m)
	}
	steps := map[string]func(sqlmock.Sqlmock){
		"not found": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("SELECT id, kind, reference").WillReturnRows(sqlmock.NewRows([]string{"id"}))
			m.ExpectRollback()
		},
		"resolve error": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery("SELECT id, kind, reference").WillReturnError(errDB)
			m.ExpectRollback()
		},
		"lock": func(m sqlmock.Sqlmock) {
			lead(m)
			m.ExpectExec("FOR UPDATE").WillReturnError(errDB)
			m.ExpectRollback()
		},
		"current": func(m sqlmock.Sqlmock) {
			lead(m)
			m.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("SELECT value").WillReturnError(errDB)
			m.ExpectRollback()
		},
		"version": func(m sqlmock.Sqlmock) {
			lead(m)
			m.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows([]string{"value"}))
			m.ExpectQuery("MAX").WillReturnError(errDB)
			m.ExpectRollback()
		},
		"write": func(m sqlmock.Sqlmock) {
			lead(m)
			m.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows([]string{"value"}))
			m.ExpectQuery("MAX").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(0))
			m.ExpectExec("INSERT INTO catalog_aspects").WillReturnError(errDB)
			m.ExpectRollback()
		},
		"history": func(m sqlmock.Sqlmock) {
			lead(m)
			m.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows([]string{"value"}))
			m.ExpectQuery("MAX").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(0))
			m.ExpectExec("INSERT INTO catalog_aspects").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("INSERT INTO catalog_aspect_history").WillReturnError(errDB)
			m.ExpectRollback()
		},
	}
	for name, step := range steps {
		t.Run(name, func(t *testing.T) {
			s, mock := newMock(t)
			step(mock)
			_, err := s.WriteAspect(ctx, dsRef, catalog.AspectDescription, json.RawMessage(`"x"`), "a")
			assert.Error(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}

	t.Run("aspect the kind does not carry", func(t *testing.T) {
		s, mock := newMock(t)
		lead(mock)
		mock.ExpectRollback()
		_, err := s.WriteAspect(ctx, dsRef, "lineage", json.RawMessage(`[]`), "a")
		assert.ErrorIs(t, err, catalog.ErrAspectNotAllowed)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestHistory(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	now := time.Now()
	expectResolveDataset(mock)
	mock.ExpectQuery("FROM catalog_aspect_history h").WithArgs("d1", "description").
		WillReturnRows(sqlmock.NewRows([]string{"id", "version", "previous", "value", "written_by", "written_at", "reverts"}).
			AddRow("h2", 2, []byte(`"a"`), nil, "ben", now, "h1").
			AddRow("h1", 1, nil, []byte(`"a"`), "ana", now, ""))
	hist, err := s.History(ctx, dsRef, catalog.AspectDescription)
	require.NoError(t, err)
	require.Len(t, hist, 2)
	assert.Equal(t, "h1", hist[0].Reverts)
	assert.Nil(t, hist[0].Value)
	assert.Equal(t, dsRef.String(), hist[1].Reference)

	mock.ExpectQuery("SELECT id, kind, reference").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = s.History(ctx, dsRef, catalog.AspectDescription)
	assert.ErrorIs(t, err, catalog.ErrNotFound)

	expectResolveDataset(mock)
	mock.ExpectQuery("FROM catalog_aspect_history h").WillReturnError(errDB)
	_, err = s.History(ctx, dsRef, catalog.AspectDescription)
	assert.ErrorIs(t, err, errDB)

	expectResolveDataset(mock)
	mock.ExpectQuery("FROM catalog_aspect_history h").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("h"))
	_, err = s.History(ctx, dsRef, catalog.AspectDescription)
	assert.Error(t, err)

	expectResolveDataset(mock)
	mock.ExpectQuery("FROM catalog_aspect_history h").WillReturnRows(
		sqlmock.NewRows([]string{"id", "version", "previous", "value", "written_by", "written_at", "reverts"}).
			RowError(0, errDB).AddRow("h", 1, nil, nil, "", now, ""))
	_, err = s.History(ctx, dsRef, catalog.AspectDescription)
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

const changeID = "5f0b3c1e-0000-4000-8000-000000000001"

func TestRollback(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_aspect_history h JOIN").WithArgs(changeID).
		WillReturnRows(sqlmock.NewRows([]string{"aspect", "previous", "id", "kind", "reference"}).
			AddRow("description", []byte(`"old"`), "d1", "dataset", dsRef.String()))
	mock.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow([]byte(`"new"`)))
	mock.ExpectQuery("MAX").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(2))
	mock.ExpectExec("INSERT INTO catalog_aspects").WithArgs("d1", "description", []byte(`"old"`), 3, "admin").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO catalog_aspect_history").
		WithArgs("d1", "description", 3, []byte(`"new"`), []byte(`"old"`), "admin", changeID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "written_at"}).AddRow("h3", time.Now()))
	mock.ExpectCommit()

	c, err := s.Rollback(context.Background(), changeID, "admin")
	require.NoError(t, err)
	assert.Equal(t, changeID, c.Reverts)
	assert.Equal(t, 3, c.Version)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRollbackFailures(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	_, err := s.Rollback(ctx, "nope", "a")
	assert.ErrorIs(t, err, catalog.ErrNotFound)

	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_aspect_history h JOIN").WillReturnRows(sqlmock.NewRows([]string{"aspect"}))
	mock.ExpectRollback()
	_, err = s.Rollback(ctx, changeID, "a")
	assert.ErrorIs(t, err, catalog.ErrNotFound)

	mock.ExpectBegin()
	mock.ExpectQuery("FROM catalog_aspect_history h JOIN").WillReturnError(errDB)
	mock.ExpectRollback()
	_, err = s.Rollback(ctx, changeID, "a")
	assert.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreForNeedsADatabase(t *testing.T) {
	assert.Nil(t, StoreFor(nil))
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	assert.NotNil(t, StoreFor(db))
}

func TestSameJSON(t *testing.T) {
	assert.True(t, sameJSON(nil, nil))
	assert.True(t, sameJSON([]byte(`{"a": 1, "b": [2]}`), []byte(`{"b":[2],"a":1}`)))
	assert.False(t, sameJSON(nil, []byte(`[]`)))
	assert.False(t, sameJSON([]byte(`["a"]`), []byte(`["b"]`)))
	assert.False(t, sameJSON([]byte(`not json`), []byte(`not json`)))
}

// A definition that cannot be stored as given is refused as the caller's
// input, before any statement runs.
func TestPutDefinitionRefusesInvalidInput(t *testing.T) {
	s, mock := newMock(t)
	for _, in := range []catalog.DefinitionInput{
		{Kind: catalog.KindGlossaryTerm, Name: " "},
		{Kind: catalog.KindGlossaryTerm, Name: "!!!"},
		{Kind: catalog.KindTag, Name: "pii", Parent: "mcp:glossary_node:n"},
		{Kind: catalog.KindGlossaryTerm, Name: "Revenue", Parent: "mcp:tag:pii"},
	} {
		_, err := s.PutDefinition(context.Background(), in)
		if !errors.Is(err, catalog.ErrInvalidDefinition) {
			t.Errorf("%+v: %v, want ErrInvalidDefinition", in, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
