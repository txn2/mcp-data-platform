package notifyqueue

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// TestHistoryWhereBindsEveryFilter is the guard on the one place a
// caller-supplied value meets SQL: every filter must arrive as a bound
// parameter, and an unset filter must add no clause at all.
func TestHistoryWhereBindsEveryFilter(t *testing.T) {
	tests := map[string]struct {
		filter     notification.HistoryFilter
		wantClause string
		wantArgs   []any
	}{
		"empty filter": {
			filter:     notification.HistoryFilter{},
			wantClause: "",
			wantArgs:   nil,
		},
		"recipient only": {
			filter:     notification.HistoryFilter{Recipient: "a@b.io"},
			wantClause: " WHERE recipient = $1",
			wantArgs:   []any{"a@b.io"},
		},
		"status only": {
			filter:     notification.HistoryFilter{Status: notification.StatusFailed},
			wantClause: " WHERE status = $1",
			wantArgs:   []any{notification.StatusFailed},
		},
		"all three keep their placeholder order": {
			filter: notification.HistoryFilter{
				Recipient: "a@b.io", Status: notification.StatusSent, Category: notification.CategoryShare,
			},
			wantClause: " WHERE recipient = $1 AND status = $2 AND category = $3",
			wantArgs:   []any{"a@b.io", notification.StatusSent, notification.CategoryShare},
		},
		"paging fields add no clause": {
			filter:     notification.HistoryFilter{Limit: 10, Offset: 20},
			wantClause: "",
			wantArgs:   nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			clause, args := historyWhere(tc.filter)
			if clause != tc.wantClause {
				t.Errorf("clause = %q, want %q", clause, tc.wantClause)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tc.wantArgs)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Errorf("arg %d = %v, want %v", i, args[i], tc.wantArgs[i])
				}
			}
		})
	}
}

// TestHistoryWhereDoesNotInterpolate asserts the property the parameter
// binding exists for: a value carrying SQL never reaches the clause text.
func TestHistoryWhereDoesNotInterpolate(t *testing.T) {
	hostile := "x@b.io' OR 1=1 --"
	clause, args := historyWhere(notification.HistoryFilter{Recipient: hostile})
	if strings.Contains(clause, "OR 1=1") {
		t.Errorf("filter value reached the SQL text: %q", clause)
	}
	if len(args) != 1 || args[0] != hostile {
		t.Errorf("filter value must be bound verbatim: %v", args)
	}
}

func TestHistoryFilterEffectiveLimit(t *testing.T) {
	tests := map[string]struct {
		limit int
		want  int
	}{
		"unset":     {0, notification.DefaultHistoryLimit},
		"negative":  {-5, notification.DefaultHistoryLimit},
		"in range":  {10, 10},
		"at cap":    {notification.MaxHistoryLimit, notification.MaxHistoryLimit},
		"above cap": {notification.MaxHistoryLimit + 1, notification.MaxHistoryLimit},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := notification.HistoryFilter{Limit: tc.limit}.EffectiveLimit()
			if got != tc.want {
				t.Errorf("EffectiveLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}

// The history queries are exercised here against sqlmock for their SQL shape
// and error paths; TestHistoryRealDB is the backstop that proves the
// statements run against real Postgres.

func TestHistoryList(t *testing.T) {
	t.Run("orders newest first and binds paging last", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		created := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
		mock.ExpectQuery(regexp.QuoteMeta(
			`FROM notifications WHERE recipient = $1 AND status = $2 ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4`)).
			WithArgs("a@b.io", notification.StatusFailed, 10, 20).
			WillReturnRows(notificationRows(t, notification.Notification{
				ID: 7, Recipient: "a@b.io", Category: notification.CategoryShare,
				Status: notification.StatusFailed, Attempts: 5, LastError: "refused",
				Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "Report"}, CreatedAt: created,
			}))

		rows, err := store.List(context.Background(), notification.HistoryFilter{
			Recipient: "a@b.io", Status: notification.StatusFailed, Limit: 10, Offset: 20,
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rows) != 1 || rows[0].ID != 7 || rows[0].Payload.ItemTitle != "Report" {
			t.Fatalf("unexpected rows: %+v", rows)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("an unfiltered listing binds only the page", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		mock.ExpectQuery(regexp.QuoteMeta(`LIMIT $1 OFFSET $2`)).
			WithArgs(notification.DefaultHistoryLimit, 0).
			WillReturnRows(notificationRows(t))

		rows, err := store.List(context.Background(), notification.HistoryFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected no rows, got %d", len(rows))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("a negative offset cannot produce a negative bind", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		mock.ExpectQuery(regexp.QuoteMeta(`LIMIT $1 OFFSET $2`)).
			WithArgs(notification.DefaultHistoryLimit, 0).
			WillReturnRows(notificationRows(t))

		if _, err := store.List(context.Background(), notification.HistoryFilter{Offset: -5}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("query failure", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()
		mock.ExpectQuery("FROM notifications").WillReturnError(errors.New("boom"))
		if _, err := store.List(context.Background(), notification.HistoryFilter{}); err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("undecodable payload", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()
		rows := sqlmock.NewRows([]string{
			"id", "recipient", "category", "payload", "digest",
			"status", "attempts", "last_error", "scheduled_for", "sent_at", "created_at",
		}).AddRow(1, "a@b.io", "share", []byte("{"), false, "sent", 1, "", time.Now(), nil, time.Now())
		mock.ExpectQuery("FROM notifications").WillReturnRows(rows)
		if _, err := store.List(context.Background(), notification.HistoryFilter{}); err == nil {
			t.Error("expected a decode error")
		}
	})
}

func TestHistoryCount(t *testing.T) {
	t.Run("counts the filter without its paging", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM notifications WHERE category = $1`)).
			WithArgs(notification.CategoryShare).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

		total, err := store.Count(context.Background(),
			notification.HistoryFilter{Category: notification.CategoryShare, Limit: 5, Offset: 10})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if total != 42 {
			t.Errorf("total = %d, want 42", total)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("query failure", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("boom"))
		if _, err := store.Count(context.Background(), notification.HistoryFilter{}); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestHistoryCountsByStatus(t *testing.T) {
	t.Run("groups by status", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT status, COUNT(*) FROM notifications GROUP BY status`)).
			WillReturnRows(sqlmock.NewRows([]string{"status", "count"}).
				AddRow(notification.StatusSent, 12).
				AddRow(notification.StatusFailed, 3))

		counts, err := store.CountsByStatus(context.Background(), notification.HistoryFilter{})
		if err != nil {
			t.Fatalf("CountsByStatus: %v", err)
		}
		if counts[notification.StatusSent] != 12 || counts[notification.StatusFailed] != 3 {
			t.Errorf("counts = %v", counts)
		}
		// A status with no rows is absent rather than zero-valued.
		if _, ok := counts[notification.StatusPending]; ok {
			t.Error("an unseen status must not appear in the map")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("applies the filter", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		mock.ExpectQuery(regexp.QuoteMeta(`WHERE recipient = $1 GROUP BY status`)).
			WithArgs("a@b.io").
			WillReturnRows(sqlmock.NewRows([]string{"status", "count"}).AddRow(notification.StatusSent, 1))

		if _, err := store.CountsByStatus(context.Background(),
			notification.HistoryFilter{Recipient: "a@b.io"}); err != nil {
			t.Fatalf("CountsByStatus: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("query failure", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()
		mock.ExpectQuery("GROUP BY status").WillReturnError(errors.New("boom"))
		if _, err := store.CountsByStatus(context.Background(), notification.HistoryFilter{}); err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("unscannable row", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()
		mock.ExpectQuery("GROUP BY status").
			WillReturnRows(sqlmock.NewRows([]string{"status", "count"}).AddRow("sent", "not-a-number"))
		if _, err := store.CountsByStatus(context.Background(), notification.HistoryFilter{}); err == nil {
			t.Error("expected a scan error")
		}
	})
}
