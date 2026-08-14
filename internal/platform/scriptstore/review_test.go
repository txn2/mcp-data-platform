package scriptstore

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// pendingReviewColumns is the result-set shape the queue query returns, in the
// order scan reads it.
var pendingReviewColumns = []string{
	"script_id", "script_name", "display_name", "description", "owner_email",
	"scope", "version", "version_id", "version_status",
	"author", "author_roles", "first_approval", "created_at",
}

func TestListPendingReviews(t *testing.T) {
	t.Run("maps a row into the queue shape", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery("FROM script_versions").
			WithArgs(script.StatusSuperseded, script.StatusDeprecated, script.VersionStatusDraft).
			WillReturnRows(sqlmock.NewRows(pendingReviewColumns).AddRow(
				"script_1", "daily", "Daily", "A daily report", "jane@example.com",
				script.ScopeGlobal, 3, "sver_3",
				script.VersionStatusDraft, "jane@example.com",
				pq.Array([]string{"analyst"}), false, rowTime))

		rows, err := s.ListPendingReviews(context.Background())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, "script_1", rows[0].ScriptID)
		assert.Equal(t, 3, rows[0].Version)
		assert.Equal(t, script.VersionStatusDraft, rows[0].VersionStatus)
		assert.Equal(t, []string{"analyst"}, rows[0].AuthorRoles)
		assert.False(t, rows[0].FirstApproval)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("an empty queue is an empty slice, never nil", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery("FROM script_versions").
			WillReturnRows(sqlmock.NewRows(pendingReviewColumns))

		rows, err := s.ListPendingReviews(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, rows)
		assert.Empty(t, rows)
	})

	t.Run("null author roles read as an empty list", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery("FROM script_versions").
			WillReturnRows(sqlmock.NewRows(pendingReviewColumns).AddRow(
				"script_1", "daily", "Daily", "", "jane@example.com",
				script.ScopePersonal, 1, "sver_1",
				script.VersionStatusApplied, "jane@example.com",
				pq.Array([]string(nil)), true, rowTime))

		rows, err := s.ListPendingReviews(context.Background())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, []string{}, rows[0].AuthorRoles,
			"a version whose author held nothing is a grant that cannot be validated, not a null")
	})

	t.Run("a query failure is reported", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery("FROM script_versions").WillReturnError(errors.New("boom"))

		_, err := s.ListPendingReviews(context.Background())
		assert.ErrorContains(t, err, "list pending script reviews")
	})

	t.Run("a scan failure is reported", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery("FROM script_versions").
			WillReturnRows(sqlmock.NewRows(pendingReviewColumns).AddRow(
				"script_1", "daily", "Daily", "", "jane@example.com",
				script.ScopePersonal, "not-a-number", "sver_1",
				script.VersionStatusApplied, "jane@example.com",
				pq.Array([]string{}), true, rowTime))

		_, err := s.ListPendingReviews(context.Background())
		assert.ErrorContains(t, err, "scanning pending script review row")
	})

	t.Run("an iteration failure is reported", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery("FROM script_versions").
			WillReturnRows(sqlmock.NewRows(pendingReviewColumns).
				AddRow("script_1", "daily", "Daily", "", "jane@example.com",
					script.ScopePersonal, 1, "sver_1",
					script.VersionStatusApplied, "jane@example.com",
					pq.Array([]string{}), true, rowTime).
				RowError(0, errors.New("connection lost")))

		_, err := s.ListPendingReviews(context.Background())
		assert.ErrorContains(t, err, "iterate pending script reviews")
	})
}

func TestRejectVersionMocked(t *testing.T) {
	t.Run("an affected row is a rejection", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec("UPDATE script_versions SET status").
			WithArgs("script_1", 2, script.VersionStatusRejected, script.VersionStatusDraft).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, s.RejectVersion(context.Background(), "script_1", 2))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no affected row is a conflict, not a success", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec("UPDATE script_versions").WillReturnResult(sqlmock.NewResult(0, 0))

		err := s.RejectVersion(context.Background(), "script_1", 2)
		assert.ErrorIs(t, err, script.ErrVersionConflict)
	})

	t.Run("a write failure is reported", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec("UPDATE script_versions").WillReturnError(errors.New("boom"))

		assert.ErrorContains(t, s.RejectVersion(context.Background(), "script_1", 2),
			"reject script version")
	})

	t.Run("an undeterminable row count is reported rather than assumed rejected", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectExec("UPDATE script_versions").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("no RowsAffected")))

		assert.ErrorContains(t, s.RejectVersion(context.Background(), "script_1", 2),
			"reading script version rejection result")
	})
}

func TestPendingReviewAgeDays(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		created time.Time
		want    int
	}{
		{"same day", now.Add(-3 * time.Hour), 0},
		{"three days", now.AddDate(0, 0, -3), 3},
		{"a clock skew ahead of now is not a negative age", now.Add(time.Hour), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := script.PendingReview{CreatedAt: tt.created}
			assert.Equal(t, tt.want, p.AgeDays(now))
		})
	}
}
