package appstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
)

var now = time.Unix(1700000000, 0).UTC()

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

var appCols = []string{
	"id", "name", "description", "owner_email", "connection", "schema_name", "current_release",
	"created_by", "created_at", "updated_at",
}

// appRow is application app_1, shop, at release current.
func appRow(current int) *sqlmock.Rows {
	return sqlmock.NewRows(appCols).AddRow("app_1", "shop", "", "jane@example.com", "orders", "shop", current, "jane@example.com", now, now)
}

func partRows(parts ...[2]string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"kind", "part_id", "added_by", "added_at"})
	for _, p := range parts {
		rows.AddRow(p[0], p[1], "jane@example.com", now)
	}
	return rows
}

func unique(constraint string) error { return &pq.Error{Code: uniqueViolation, Constraint: constraint} }

func TestCreate(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery("INSERT INTO applications").WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	a := &application.Application{Name: "shop"}
	require.NoError(t, s.Create(ctx, a))
	assert.Contains(t, a.ID, "app_")
	assert.Equal(t, []application.Part{}, a.Parts)

	mock.ExpectQuery("INSERT INTO applications").WillReturnError(unique(nameConstraint))
	require.ErrorIs(t, s.Create(ctx, &application.Application{Name: "shop"}), application.ErrNameTaken)
	mock.ExpectQuery("INSERT INTO applications").WillReturnError(unique(schemaConstraint))
	require.ErrorIs(t, s.Create(ctx, &application.Application{Name: "x"}), application.ErrSchemaHeld)
	mock.ExpectQuery("INSERT INTO applications").WillReturnError(errors.New("down"))
	require.ErrorContains(t, s.Create(ctx, &application.Application{Name: "y"}), "create application")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAndList(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery("FROM applications WHERE id").WillReturnRows(appRow(2))
	mock.ExpectQuery("FROM application_parts").WillReturnRows(partRows([2]string{"script", "s1"}))
	a, err := s.Get(ctx, "app_1")
	require.NoError(t, err)
	assert.Equal(t, 2, a.CurrentRelease)
	assert.Len(t, a.Parts, 1)

	mock.ExpectQuery("FROM applications WHERE name").WillReturnRows(sqlmock.NewRows(appCols))
	_, err = s.GetByName(ctx, "nope")
	require.ErrorIs(t, err, application.ErrNotFound)
	mock.ExpectQuery("FROM applications WHERE name").WillReturnError(errors.New("down"))
	_, err = s.GetByName(ctx, "x")
	require.ErrorContains(t, err, "read application")
	mock.ExpectQuery("FROM applications WHERE id").WillReturnRows(appRow(0))
	mock.ExpectQuery("FROM application_parts").WillReturnError(errors.New("down"))
	_, err = s.Get(ctx, "app_1")
	require.ErrorContains(t, err, "parts")

	mock.ExpectQuery("FROM applications ORDER BY name").WillReturnRows(appRow(0))
	mock.ExpectQuery("WHERE application_id = ANY").WillReturnRows(sqlmock.NewRows([]string{"application_id", "kind", "part_id", "added_by", "added_at"}).
		AddRow("app_1", "script", "s1", "jane@example.com", now).AddRow("app_1", "asset", "a1", "jane@example.com", now))
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Len(t, list[0].Parts, 2, "a listing carries each application's parts")
	mock.ExpectQuery("FROM applications ORDER BY name").WillReturnRows(appRow(0))
	mock.ExpectQuery("WHERE application_id = ANY").WillReturnError(errors.New("down"))
	_, err = s.List(ctx)
	require.Error(t, err)
	mock.ExpectQuery("FROM applications ORDER BY name").WillReturnError(errors.New("down"))
	_, err = s.List(ctx)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAndDelete(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectExec("UPDATE applications SET description").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Update(ctx, &application.Application{ID: "app_1"}))
	mock.ExpectExec("UPDATE applications SET description").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Update(ctx, &application.Application{ID: "app_2"}), application.ErrNotFound)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT kind, part_id FROM application_parts").WillReturnRows(
		sqlmock.NewRows([]string{"kind", "part_id"}).AddRow("script", "s1").AddRow("asset", "a1").AddRow("source", "hook"))
	mock.ExpectExec("UPDATE scripts SET released_version = NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets SET released_version = NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.Delete(ctx, "app_1"))

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT kind, part_id FROM application_parts").WillReturnRows(sqlmock.NewRows([]string{"kind", "part_id"}))
	mock.ExpectExec("DELETE FROM applications").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	require.ErrorIs(t, s.Delete(ctx, "app_9"), application.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestParts(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	p := application.Part{Kind: application.KindScript, ID: "s1", AddedBy: "jane@example.com"}
	mock.ExpectExec("INSERT INTO application_parts").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.AddPart(ctx, "app_1", p))
	mock.ExpectExec("INSERT INTO application_parts").WillReturnError(unique(partConstraint))
	require.ErrorIs(t, s.AddPart(ctx, "app_2", p), application.ErrPartTaken)
	mock.ExpectExec("INSERT INTO application_parts").WillReturnError(errors.New("down"))
	require.Error(t, s.AddPart(ctx, "app_2", p))

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM application_parts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE scripts SET released_version = NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.RemovePart(ctx, "app_1", application.KindScript, "s1"))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM application_parts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets SET released_version = NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.RemovePart(ctx, "app_1", application.KindAsset, "a1"))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM application_parts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.RemovePart(ctx, "app_1", application.KindSource, "hook"))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM application_parts").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	require.ErrorIs(t, s.RemovePart(ctx, "app_1", application.KindSource, "x"), application.ErrNoPart)

	mock.ExpectQuery("SELECT application_id FROM application_parts").WillReturnRows(sqlmock.NewRows([]string{"application_id"}))
	_, err := s.Owning(ctx, "script", "s9")
	require.ErrorIs(t, err, application.ErrNotFound)
	mock.ExpectQuery("SELECT application_id FROM application_parts").WillReturnRows(sqlmock.NewRows([]string{"application_id"}).AddRow("app_1"))
	mock.ExpectQuery("FROM applications WHERE id").WillReturnRows(appRow(0))
	mock.ExpectQuery("FROM application_parts").WillReturnRows(partRows())
	a, err := s.Owning(ctx, "script", "s1")
	require.NoError(t, err)
	assert.Equal(t, "shop", a.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

var releaseCols = []string{"number", "action", "migration_version", "restored", "changes", "note", "created_by", "created_at"}

func TestReleases(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery("FROM application_releases").WillReturnRows(sqlmock.NewRows(releaseCols).
		AddRow(2, "rollback", 1, 1, []byte(`{"parts":null}`), "", "admin", now).
		AddRow(1, "release", 1, 0, []byte(`{}`), "first", "admin", now))
	mock.ExpectQuery("FROM application_release_pins").WillReturnRows(sqlmock.NewRows([]string{"number", "kind", "part_id", "version"}).
		AddRow(1, "script", "s1", 1).AddRow(2, "script", "s1", 1))
	list, err := s.Releases(ctx, "app_1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, []application.PinChange{}, list[0].Changes.Parts, "an empty list is [] never null")
	assert.Equal(t, 1, list[1].PinOf("script", "s1"))

	mock.ExpectQuery("FROM application_releases").WillReturnRows(sqlmock.NewRows(releaseCols))
	_, err = s.Release(ctx, "app_1", 9)
	require.ErrorIs(t, err, application.ErrNoRelease)
	mock.ExpectQuery("FROM application_releases").WillReturnRows(sqlmock.NewRows(releaseCols).
		AddRow(1, "release", 1, 0, []byte(`not json`), "", "admin", now))
	_, err = s.Release(ctx, "app_1", 1)
	require.Error(t, err)
	mock.ExpectQuery("FROM application_releases").WillReturnRows(sqlmock.NewRows(releaseCols).
		AddRow(1, "release", 1, 0, []byte(`{}`), "", "admin", now))
	mock.ExpectQuery("FROM application_release_pins").WillReturnRows(sqlmock.NewRows([]string{"number", "kind", "part_id", "version"}).
		AddRow(1, "asset", "a1", 3))
	rel, err := s.Release(ctx, "app_1", 1)
	require.NoError(t, err)
	assert.Equal(t, 3, rel.PinOf("asset", "a1"))
	mock.ExpectQuery("FROM application_releases").WillReturnError(errors.New("down"))
	_, err = s.Releases(ctx, "app_1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecord(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	rel := application.Release{
		Action: "release", CreatedBy: "admin",
		Pins: []application.Pin{{Kind: "script", PartID: "s1", Version: 2}},
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_release FROM applications").WillReturnRows(sqlmock.NewRows([]string{"current_release"}).AddRow(1))
	mock.ExpectQuery("INSERT INTO application_releases").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	mock.ExpectExec("INSERT INTO application_release_pins").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE applications SET current_release").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT kind, part_id FROM application_parts").WillReturnRows(
		sqlmock.NewRows([]string{"kind", "part_id"}).AddRow("script", "s1"))
	mock.ExpectExec("UPDATE scripts SET released_version = NULL").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE scripts s SET released_version = p.version").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets a SET released_version = p.version").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	got, err := s.Record(ctx, "app_1", application.Recorded{Release: rel, After: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Number)
	assert.Equal(t, []int{}, got.Changes.Applied)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_release FROM applications").WillReturnRows(sqlmock.NewRows([]string{"current_release"}).AddRow(2))
	mock.ExpectRollback()
	_, err = s.Record(ctx, "app_1", application.Recorded{Release: rel, After: 1})
	require.ErrorIs(t, err, application.ErrStale)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_release FROM applications").WillReturnRows(sqlmock.NewRows([]string{"current_release"}))
	mock.ExpectRollback()
	_, err = s.Record(ctx, "app_9", application.Recorded{Release: rel})
	require.ErrorIs(t, err, application.ErrNotFound)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_release FROM applications").WillReturnRows(sqlmock.NewRows([]string{"current_release"}).AddRow(0))
	mock.ExpectQuery("INSERT INTO application_releases").WillReturnError(unique(releaseKey))
	mock.ExpectRollback()
	_, err = s.Record(ctx, "app_1", application.Recorded{Release: rel})
	require.ErrorIs(t, err, application.ErrStale)
	require.NoError(t, mock.ExpectationsWereMet())
}
