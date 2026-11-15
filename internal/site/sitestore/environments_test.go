package sitestore

import (
	"context"
	"database/sql"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

func TestSetStatus(t *testing.T) {
	ctx := context.Background()
	back := time.Date(2030, 1, 2, 14, 0, 0, 0, time.UTC)
	t.Run("writes the status and records it with its message", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("UPDATE site_environments")).
			WithArgs("site_1", "prod", site.StatusOffline, "Back at 2pm", &back, "", false, "a@example.com").
			WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(3))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WithArgs("site_1", "prod", site.StatusOffline, 3, "a@example.com", "Back at 2pm").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		require.NoError(t, s.SetStatus(ctx, "site_1", "prod", site.EnvironmentStatus{Status: site.StatusOffline, Message: "Back at 2pm", BackAt: &back}, "a@example.com"))
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("a redirect records its target", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("UPDATE site_environments")).WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(3))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WithArgs("site_1", "prod", site.StatusRedirect, 3, "a", "https://x.example").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		require.NoError(t, s.SetStatus(ctx, "site_1", "prod", site.EnvironmentStatus{Status: site.StatusRedirect, RedirectTo: "https://x.example"}, "a"))
	})
	t.Run("no such environment", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("UPDATE site_environments")).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		require.ErrorIs(t, s.SetStatus(ctx, "site_1", "x", site.EnvironmentStatus{Status: site.StatusOnline}, "a"), site.ErrNoEnvironment)
	})
	t.Run("a failure", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(q("UPDATE site_environments")).WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(3))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WillReturnError(errDown)
		mock.ExpectRollback()
		require.ErrorContains(t, s.SetStatus(ctx, "site_1", "x", site.EnvironmentStatus{Status: site.StatusOnline}, "a"), "record status change")
	})
}

func TestRenameEnvironment(t *testing.T) {
	ctx := context.Background()
	lock := func(mock sqlmock.Sqlmock) {
		mock.ExpectBegin()
		mock.ExpectQuery(q("SELECT name FROM sites WHERE id = $1 FOR UPDATE")).WithArgs("site_1").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("marketing"))
	}
	t.Run("moves every reference in one transaction", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("UPDATE site_environments SET name = $3")).WithArgs("site_1", "uat", "qa", "a").WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(4))
		for _, stmt := range []string{"UPDATE site_domains", "UPDATE site_deployments", "UPDATE site_jobs", "UPDATE sites SET main_environment", "UPDATE site_grants"} {
			mock.ExpectExec(q(stmt)).WithArgs("site_1", "uat", "qa").WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectExec(q("UPDATE application_parts")).WithArgs("marketing/uat", "marketing/qa").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("UPDATE application_release_pins")).WithArgs("marketing/uat", "marketing/qa").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("UPDATE approval_requests SET target = $3")).WithArgs("site_1", "uat", "qa").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("INSERT INTO site_deployments")).WithArgs("site_1", "qa", 4, "a", "uat").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		require.NoError(t, s.RenameEnvironment(ctx, "site_1", "uat", "qa", "a"))
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("a name in use", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("UPDATE site_environments SET name = $3")).WillReturnError(errDupKey)
		mock.ExpectRollback()
		require.ErrorIs(t, s.RenameEnvironment(ctx, "site_1", "uat", "stage", "a"), site.ErrEnvironmentInUse)
	})
	t.Run("no such environment, no such site", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock)
		mock.ExpectQuery(q("UPDATE site_environments SET name = $3")).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		require.ErrorIs(t, s.RenameEnvironment(ctx, "site_1", "nope", "qa", "a"), site.ErrNoEnvironment)
		mock.ExpectBegin()
		mock.ExpectQuery(q("SELECT name FROM sites")).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		require.ErrorIs(t, s.RenameEnvironment(ctx, "site_x", "uat", "qa", "a"), site.ErrNotFound)
	})
}

func TestRemoveEnvironment(t *testing.T) {
	ctx := context.Background()
	lock := func(mock sqlmock.Sqlmock, main string) {
		mock.ExpectBegin()
		mock.ExpectQuery(q("SELECT name, COALESCE(main_environment, '') FROM sites")).WithArgs("site_1").
			WillReturnRows(sqlmock.NewRows([]string{"name", "main"}).AddRow("marketing", main))
	}
	attachedRow := func(mock sqlmock.Sqlmock, domains, parts, jobs int) {
		mock.ExpectQuery(q("SELECT (SELECT COUNT(*) FROM site_domains")).WithArgs("site_1", "stage", "marketing/stage").
			WillReturnRows(sqlmock.NewRows([]string{"d", "p", "j"}).AddRow(domains, parts, jobs))
	}
	t.Run("removes it, withdraws, narrows grants and marks history", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock, "prod")
		attachedRow(mock, 0, 0, 0)
		mock.ExpectExec(q("DELETE FROM site_environments")).WithArgs("site_1", "stage").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("UPDATE approval_requests SET status = 'withdrawn'")).WithArgs("site_1", "stage").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("DELETE FROM site_grants WHERE site_id = $1 AND environments = ARRAY[$2]::text[]")).WithArgs("site_1", "stage").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("UPDATE site_grants SET environments = array_remove")).WithArgs("site_1", "stage").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(q("UPDATE site_deployments SET removed_at = NOW()")).WithArgs("site_1", "stage").WillReturnResult(sqlmock.NewResult(0, 2))
		mock.ExpectExec(q("UPDATE site_environments e SET position")).WithArgs("site_1", "stage").WillReturnResult(sqlmock.NewResult(0, 3))
		mock.ExpectCommit()
		require.NoError(t, s.RemoveEnvironment(ctx, "site_1", "stage"))
		require.NoError(t, mock.ExpectationsWereMet())
	})
	for _, tc := range []struct {
		name                 string
		main                 string
		domains, parts, jobs int
		want                 string
	}{
		{"the main address", "stage", 0, 0, 0, "main address"},
		{"a domain", "prod", 1, 0, 0, "domain(s)"},
		{"an application part", "prod", 0, 1, 0, "part of an application"},
		{"a running job", "prod", 0, 0, 1, "job is running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newMock(t)
			lock(mock, tc.main)
			if tc.main != "stage" {
				attachedRow(mock, tc.domains, tc.parts, tc.jobs)
			}
			mock.ExpectRollback()
			err := s.RemoveEnvironment(ctx, "site_1", "stage")
			require.ErrorIs(t, err, site.ErrEnvironmentInUse)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	t.Run("no such environment", func(t *testing.T) {
		s, mock := newMock(t)
		lock(mock, "prod")
		attachedRow(mock, 0, 0, 0)
		mock.ExpectExec(q("DELETE FROM site_environments")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()
		require.ErrorIs(t, s.RemoveEnvironment(ctx, "site_1", "stage"), site.ErrNoEnvironment)
	})
}
