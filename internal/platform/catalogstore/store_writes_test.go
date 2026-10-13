package catalogstore

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

const changeID2 = "5f0b3c1e-0000-4000-8000-000000000002"

// expectWrite expects one aspect write of d1 after the entity was resolved:
// the lock, the current value, the version and the two inserts. The change
// recorded is h<version>.
func expectWrite(mock sqlmock.Sqlmock, aspect string, current []byte, last int, value []byte) {
	mock.ExpectExec("FOR UPDATE").WithArgs("d1").WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"value"})
	if current != nil {
		rows.AddRow(current)
	}
	mock.ExpectQuery("SELECT value FROM catalog_aspects").WithArgs("d1", aspect).WillReturnRows(rows)
	mock.ExpectQuery("MAX\\(version\\)").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(last))
	mock.ExpectExec("INSERT INTO catalog_aspects").
		WithArgs("d1", aspect, value, last+1, "ana").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO catalog_aspect_history").
		WillReturnRows(sqlmock.NewRows([]string{"id", "written_at"}).AddRow("h"+strconv.Itoa(last+1), time.Now()))
}

func TestWriteAspectsWritesEveryEditInOneTransaction(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectResolveDataset(mock)
	expectWrite(mock, "description", []byte(`"old"`), 1, []byte(`"new"`))
	expectResolveDataset(mock)
	expectWrite(mock, "tags", nil, 0, []byte(`["pii"]`))
	mock.ExpectCommit()

	out, err := s.WriteAspects(context.Background(), []catalog.AspectEdit{
		{
			Ref: dsRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"new"`),
			Expect: json.RawMessage(`"old"`), Checked: true,
		},
		{Ref: dsRef, Aspect: catalog.AspectTags, Value: json.RawMessage(`["pii"]`), Checked: true},
	}, "ana")
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "h2", out[0].ID)
	assert.Equal(t, 2, out[0].Version)
	assert.Equal(t, "h1", out[1].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWriteAspectsRefusesAStaleRead(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectResolveDataset(mock)
	mock.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM catalog_aspects").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow([]byte(`"written since"`)))
	mock.ExpectRollback()

	_, err := s.WriteAspects(context.Background(), []catalog.AspectEdit{{
		Ref: dsRef, Aspect: catalog.AspectDescription, Value: json.RawMessage(`"new"`),
		Expect: json.RawMessage(`"old"`), Checked: true,
	}}, "ana")
	require.ErrorIs(t, err, catalog.ErrStale)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWriteAspectsFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("entity not found", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id, kind, reference").WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectRollback()
		_, err := s.WriteAspects(ctx, []catalog.AspectEdit{{Ref: dsRef, Aspect: catalog.AspectDescription}}, "a")
		require.ErrorIs(t, err, catalog.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("aspect the kind does not carry", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		expectResolveDataset(mock)
		mock.ExpectRollback()
		_, err := s.WriteAspects(ctx, []catalog.AspectEdit{{Ref: dsRef, Aspect: "lineage", Value: json.RawMessage(`[]`)}}, "a")
		require.ErrorIs(t, err, catalog.ErrAspectNotAllowed)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("begin", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin().WillReturnError(errDB)
		_, err := s.WriteAspects(ctx, []catalog.AspectEdit{{Ref: dsRef, Aspect: catalog.AspectDescription}}, "a")
		require.ErrorIs(t, err, errDB)
	})
}

// expectChangeForRevert expects the read of changeID for a revert.
func expectChangeForRevert(mock sqlmock.Sqlmock, version int, previous []byte) {
	mock.ExpectQuery("SELECT h.aspect, h.version, h.previous").WithArgs(changeID).
		WillReturnRows(sqlmock.NewRows([]string{"aspect", "version", "previous", "id", "kind", "reference"}).
			AddRow("description", version, previous, "d1", "dataset", dsRef.String()))
}

func TestRevertChangesRestoresWhatEachReplaced(t *testing.T) {
	s, mock := newMock(t)
	now := time.Now()
	mock.ExpectBegin()
	expectChangeForRevert(mock, 2, []byte(`"old"`))
	mock.ExpectExec("FOR UPDATE").WithArgs("d1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("ORDER BY version DESC LIMIT 1").WithArgs("d1", "description").
		WillReturnRows(sqlmock.NewRows([]string{"id", "version", "written_by", "written_at"}).AddRow(changeID, 2, "ana", now))
	mock.ExpectExec("FOR UPDATE").WithArgs("d1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM catalog_aspects").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow([]byte(`"new"`)))
	mock.ExpectQuery("MAX\\(version\\)").WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(2))
	mock.ExpectExec("INSERT INTO catalog_aspects").
		WithArgs("d1", "description", []byte(`"old"`), 3, "admin").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO catalog_aspect_history").
		WithArgs("d1", "description", 3, []byte(`"new"`), []byte(`"old"`), "admin", changeID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "written_at"}).AddRow("h3", now))
	mock.ExpectCommit()

	out, err := s.RevertChanges(context.Background(), []string{changeID}, "admin")
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, changeID, out[0].Reverts)
	assert.JSONEq(t, `"old"`, string(out[0].Value))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRevertChangesRefusesAChangeWrittenOver(t *testing.T) {
	s, mock := newMock(t)
	now := time.Now()
	mock.ExpectBegin()
	expectChangeForRevert(mock, 2, []byte(`"old"`))
	mock.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("ORDER BY version DESC LIMIT 1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "version", "written_by", "written_at"}).AddRow(changeID2, 3, "ben", now))
	mock.ExpectRollback()

	_, err := s.RevertChanges(context.Background(), []string{changeID}, "admin")
	var conflict *catalog.ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, changeID, conflict.Change)
	assert.Equal(t, changeID2, conflict.Newer.ID)
	assert.Equal(t, "ben", conflict.Newer.WrittenBy)
	assert.Equal(t, dsRef.String(), conflict.Newer.Reference)
	assert.Equal(t, catalog.AspectDescription, conflict.Aspect)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRevertChangesFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("an id that is not a change", func(t *testing.T) {
		s, mock := newMock(t)
		_, err := s.RevertChanges(ctx, []string{changeID, "nope"}, "a")
		require.ErrorIs(t, err, catalog.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
	steps := map[string]func(sqlmock.Sqlmock){
		"change not found": func(m sqlmock.Sqlmock) {
			m.ExpectQuery("SELECT h.aspect, h.version").WillReturnRows(sqlmock.NewRows([]string{"aspect"}))
		},
		"reading the change": func(m sqlmock.Sqlmock) {
			m.ExpectQuery("SELECT h.aspect, h.version").WillReturnError(errDB)
		},
		"lock": func(m sqlmock.Sqlmock) {
			expectChangeForRevert(m, 1, nil)
			m.ExpectExec("FOR UPDATE").WillReturnError(errDB)
		},
		"latest": func(m sqlmock.Sqlmock) {
			expectChangeForRevert(m, 1, nil)
			m.ExpectExec("FOR UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("ORDER BY version DESC LIMIT 1").WillReturnError(errDB)
		},
	}
	for name, step := range steps {
		t.Run(name, func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectBegin()
			step(mock)
			mock.ExpectRollback()
			_, err := s.RevertChanges(ctx, []string{changeID}, "a")
			require.Error(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestTaggedEntities(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	mock.ExpectQuery("a.value @> jsonb_build_array").WithArgs("pii", 10).
		WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow(dsRef.String()).AddRow(catalog.ColumnRef(dsRef, "a").String()))
	refs, err := s.TaggedEntities(ctx, "pii", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{dsRef.String(), catalog.ColumnRef(dsRef, "a").String()}, refs)

	mock.ExpectQuery("a.value @> jsonb_build_array").WillReturnRows(sqlmock.NewRows([]string{"reference"}))
	refs, err = s.TaggedEntities(ctx, "none", 10)
	require.NoError(t, err)
	assert.NotNil(t, refs, "no tagged entity is an empty list")
	assert.Empty(t, refs)

	mock.ExpectQuery("a.value @> jsonb_build_array").WillReturnError(errDB)
	_, err = s.TaggedEntities(ctx, "pii", 10)
	require.ErrorIs(t, err, errDB)

	mock.ExpectQuery("a.value @> jsonb_build_array").
		WillReturnRows(sqlmock.NewRows([]string{"reference", "extra"}).AddRow("a", "b"))
	_, err = s.TaggedEntities(ctx, "pii", 10)
	require.Error(t, err)

	mock.ExpectQuery("a.value @> jsonb_build_array").
		WillReturnRows(sqlmock.NewRows([]string{"reference"}).AddRow("a").RowError(0, errDB))
	_, err = s.TaggedEntities(ctx, "pii", 10)
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}
