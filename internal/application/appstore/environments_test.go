package appstore

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appenv"
)

var errDown = errors.New("down")

// The environment writes (#2696), their refusals and their failures.
func TestEnvironmentWrites(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()

	// A promote into the last environment makes it current and holds its pins.
	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"name", "release"}).AddRow("prod", 1))
	mock.ExpectQuery("UPDATE application_environments e SET release").WillReturnRows(sqlmock.NewRows([]string{"last"}).AddRow(true))
	mock.ExpectExec("UPDATE applications SET current_release").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT kind, part_id FROM application_parts").WillReturnRows(sqlmock.NewRows([]string{"kind", "part_id"}))
	mock.ExpectExec("UPDATE scripts s SET released_version").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE portal_assets a SET released_version").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	require.NoError(t, s.SetEnvironmentRelease(ctx, "app_1", application.Move{Environment: "prod", Number: 2, Expect: 1, By: "admin"}))

	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"name", "release"}).AddRow("prod", 3))
	mock.ExpectRollback()
	require.ErrorIs(t, s.SetEnvironmentRelease(ctx, "app_1", application.Move{Environment: "prod", Number: 2, Expect: 1, By: "admin"}), application.ErrStale)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"name", "release"}).AddRow("prod", 3))
	mock.ExpectRollback()
	require.ErrorIs(t, s.SetEnvironmentRelease(ctx, "app_1", application.Move{Environment: "stage", Number: 2, Expect: 3, By: "admin"}), appenv.ErrNoEnvironment, "the lock found another environment")

	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.SetEnvironmentRelease(ctx, "app_1", application.Move{Environment: "prod", Number: 2, Expect: 1, By: "admin"}), "lock application")

	// Add: after names one, or none puts it first; a taken name is refused.
	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT position \\+ 1").WillReturnRows(sqlmock.NewRows([]string{"p"}).AddRow(1))
	mock.ExpectExec("SET position = position \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO application_environments").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.AddEnvironment(ctx, "app_1", application.Environment{Name: "stage"}, "dev"))

	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET position = position \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO application_environments").WillReturnError(unique(envKey))
	mock.ExpectRollback()
	require.ErrorIs(t, s.AddEnvironment(ctx, "app_1", application.Environment{Name: "dev"}, ""), application.ErrNameTaken)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT position \\+ 1").WillReturnRows(sqlmock.NewRows([]string{"p"}))
	mock.ExpectRollback()
	require.ErrorIs(t, s.AddEnvironment(ctx, "app_1", application.Environment{Name: "qa"}, "nope"), appenv.ErrNoEnvironment)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET position = position \\+ 1").WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.AddEnvironment(ctx, "app_1", application.Environment{Name: "qa"}, ""), "make room")

	// Remove closes the gap and drops its variables.
	mock.ExpectBegin()
	mock.ExpectQuery("DELETE FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"position"}).AddRow(1))
	mock.ExpectExec("SET position = position - 1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM application_variables").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	require.NoError(t, s.RemoveEnvironment(ctx, "app_1", "stage"))
	mock.ExpectBegin()
	mock.ExpectQuery("DELETE FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"position"}))
	mock.ExpectRollback()
	require.ErrorIs(t, s.RemoveEnvironment(ctx, "app_1", "stage"), appenv.ErrNoEnvironment)
	mock.ExpectBegin()
	mock.ExpectQuery("DELETE FROM application_environments").WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.RemoveEnvironment(ctx, "app_1", "stage"), "remove environment")

	mock.ExpectExec("SET requires_approval").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetEnvironmentApproval(ctx, "app_1", "prod", true, "admin"))
	mock.ExpectExec("SET requires_approval").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.SetEnvironmentApproval(ctx, "app_1", "qa", true, "admin"), appenv.ErrNoEnvironment)
	mock.ExpectExec("SET requires_approval").WillReturnError(errDown)
	require.Error(t, s.SetEnvironmentApproval(ctx, "app_1", "qa", true, "admin"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVariablesAndResolution(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO application_variables").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetVariable(ctx, "app_1", application.Variable{Name: "A", Value: "1"}))
	mock.ExpectExec("INSERT INTO application_variables").WillReturnError(errDown)
	require.Error(t, s.SetVariable(ctx, "app_1", application.Variable{Name: "A", Value: "1"}))
	mock.ExpectExec("DELETE FROM application_variables").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.DeleteVariable(ctx, "app_1", "", "A"))
	mock.ExpectExec("DELETE FROM application_variables").WillReturnError(errDown)
	require.Error(t, s.DeleteVariable(ctx, "app_1", "", "A"))
	mock.ExpectQuery("FROM application_variables").WillReturnRows(sqlmock.NewRows([]string{"environment", "name", "value", "updated_by", "updated_at"}).
		AddRow("", "A", "1", "j", now))
	vars, err := s.Variables(ctx, "app_1")
	require.NoError(t, err)
	assert.Len(t, vars, 1)
	mock.ExpectQuery("FROM application_variables").WillReturnError(errDown)
	_, err = s.Variables(ctx, "app_1")
	require.Error(t, err)

	cols := []string{"name", "version", "vars"}
	mock.ExpectQuery("FROM application_parts p").WillReturnRows(sqlmock.NewRows(cols).AddRow("dev", 4, []byte(`{"A":"1"}`)))
	got, err := s.ForScript(ctx, "s1", "dev")
	require.NoError(t, err)
	assert.Equal(t, appenv.Script{OK: true, Environment: "dev", Version: 4, Variables: map[string]string{"A": "1"}}, *got)
	mock.ExpectQuery("FROM application_parts p").WillReturnRows(sqlmock.NewRows(cols))
	got, err = s.ForScript(ctx, "s1", "")
	require.NoError(t, err)
	assert.False(t, got.OK)
	mock.ExpectQuery("FROM application_parts p").WillReturnRows(sqlmock.NewRows(cols).AddRow(nil, 0, []byte(`{}`)))
	_, err = s.ForScript(ctx, "s1", "qa")
	require.ErrorIs(t, err, appenv.ErrNoEnvironment)
	mock.ExpectQuery("FROM application_parts p").WillReturnRows(sqlmock.NewRows(cols).AddRow("dev", 0, []byte(`[`)))
	_, err = s.ForScript(ctx, "s1", "dev")
	require.ErrorContains(t, err, "variables")
	mock.ExpectQuery("FROM application_parts p").WillReturnError(errDown)
	_, err = s.ForScript(ctx, "s1", "dev")
	require.Error(t, err)

	mock.ExpectQuery("JOIN application_environments e").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("dev").AddRow("prod"))
	names, err := s.EnvironmentNames(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, []string{"dev", "prod"}, names)
	mock.ExpectQuery("JOIN application_environments e").WillReturnError(errDown)
	_, err = s.EnvironmentNames(ctx, "s1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnvironmentReadFailures(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectQuery("FROM application_environments").WillReturnError(errDown)
	_, err := s.environments(ctx, "app_1")
	require.ErrorContains(t, err, "read environments")
	mock.ExpectQuery("FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("dev"))
	_, err = s.environments(ctx, "app_1")
	require.ErrorContains(t, err, "read environment")

	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"name", "release"}).AddRow("dev", 0))
	mock.ExpectQuery("UPDATE application_environments e SET release").WillReturnRows(sqlmock.NewRows([]string{"last"}))
	mock.ExpectRollback()
	require.ErrorIs(t, s.SetEnvironmentRelease(ctx, "app_1", application.Move{Environment: "dev", Number: 1, Expect: 0, By: "a"}), appenv.ErrNoEnvironment)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"name", "release"}).AddRow("dev", 0))
	mock.ExpectQuery("UPDATE application_environments e SET release").WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.SetEnvironmentRelease(ctx, "app_1", application.Move{Environment: "dev", Number: 1, Expect: 0, By: "a"}), "set environment release")
	mock.ExpectBegin()
	mock.ExpectExec("SELECT 1 FROM applications").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM application_environments").WillReturnRows(sqlmock.NewRows([]string{"name", "release"}).AddRow("prod", 0))
	mock.ExpectQuery("UPDATE application_environments e SET release").WillReturnRows(sqlmock.NewRows([]string{"last"}).AddRow(true))
	mock.ExpectExec("UPDATE applications SET current_release").WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorContains(t, s.SetEnvironmentRelease(ctx, "app_1", application.Move{Environment: "prod", Number: 1, Expect: 0, By: "a"}), "make release current")
	require.NoError(t, mock.ExpectationsWereMet())
}
