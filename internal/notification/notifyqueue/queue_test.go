package notifyqueue

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func newMockQueueStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresStore(db), mock, func() { _ = db.Close() }
}

func notificationRows(t *testing.T, ns ...notification.Notification) *sqlmock.Rows {
	t.Helper()
	rows := sqlmock.NewRows([]string{
		"id", "recipient", "category", "payload", "digest",
		"status", "attempts", "last_error", "scheduled_for", "sent_at", "created_at",
	})
	for _, n := range ns {
		payload, err := json.Marshal(n.Payload)
		if err != nil {
			t.Fatal(err)
		}
		rows.AddRow(n.ID, n.Recipient, n.Category, payload, n.Digest,
			n.Status, n.Attempts, n.LastError, n.ScheduledFor, nil, n.CreatedAt)
	}
	return rows
}

// enqueueInsert is the exact statement Enqueue must issue. Matching the full
// column list rather than the table name alone means a column added, dropped or
// reordered fails here instead of being rubber-stamped: sqlmock validates
// against this string, not against a schema, so a loose pattern would accept an
// insert real Postgres rejects. TestQueueStoreRealDB is the backstop that
// catches what no mock can.
var enqueueInsert = regexp.QuoteMeta(
	`INSERT INTO notifications (recipient, category, payload, digest, scheduled_for)`)

func TestQueueStore_Enqueue(t *testing.T) {
	t.Run("unscheduled notification defers to the database clock", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		payload, err := json.Marshal(notification.Payload{Kind: notification.KindAsset, ItemTitle: "Report"})
		if err != nil {
			t.Fatal(err)
		}
		// scheduled_for is bound as nil, which is what makes COALESCE($5, NOW())
		// stamp the row with the database clock. Passing a Go-side timestamp
		// here would reintroduce the host/DB clock skew the nil exists to avoid.
		mock.ExpectExec(enqueueInsert).
			WithArgs("a@b.io", notification.CategoryShare, payload, false, nil).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("SELECT pg_notify").
			WithArgs(NotifyChannel).
			WillReturnResult(sqlmock.NewResult(0, 0))

		if err := store.Enqueue(context.Background(), notification.Notification{
			Recipient: "a@b.io", Category: notification.CategoryShare,
			Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "Report"},
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("digest notification passes its schedule through", func(t *testing.T) {
		store, mock, done := newMockQueueStore(t)
		defer done()

		when := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
		payload, err := json.Marshal(notification.Payload{Kind: notification.KindAsset, ItemTitle: "Digest"})
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectExec(enqueueInsert).
			WithArgs("a@b.io", notification.CategoryShare, payload, true, when).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("SELECT pg_notify").
			WithArgs(NotifyChannel).
			WillReturnResult(sqlmock.NewResult(0, 0))

		if err := store.Enqueue(context.Background(), notification.Notification{
			Recipient: "a@b.io", Category: notification.CategoryShare, Digest: true,
			ScheduledFor: when,
			Payload:      notification.Payload{Kind: notification.KindAsset, ItemTitle: "Digest"},
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

func TestQueueStore_Enqueue_NotifyFailureIgnored(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("INSERT INTO notifications").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_notify").
		WillReturnError(errors.New("notify unavailable"))

	if err := store.Enqueue(context.Background(), notification.Notification{Recipient: "a@b.io"}); err != nil {
		t.Fatalf("Enqueue must succeed despite notify failure: %v", err)
	}
}

func TestQueueStore_Enqueue_InsertError(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("INSERT INTO notifications").
		WillReturnError(errors.New("insert failed"))

	if err := store.Enqueue(context.Background(), notification.Notification{Recipient: "a@b.io"}); err == nil {
		t.Fatal("expected insert error")
	}
}

func TestQueueStore_ClaimImmediate(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	n := notification.Notification{
		ID: 7, Recipient: "a@b.io", Category: notification.CategoryShare,
		Status: notification.StatusSending, Attempts: 1, ScheduledFor: time.Now(), CreatedAt: time.Now(),
		Payload: notification.Payload{Kind: notification.KindAsset, ItemTitle: "Report"},
	}
	mock.ExpectQuery("UPDATE notifications").
		WithArgs(120).
		WillReturnRows(notificationRows(t, n))

	got, err := store.ClaimImmediate(context.Background(), 2*time.Minute)
	if err != nil {
		t.Fatalf("ClaimImmediate: %v", err)
	}
	if got.ID != 7 || got.Payload.ItemTitle != "Report" {
		t.Errorf("unexpected claim: %+v", got)
	}
}

func TestQueueStore_ClaimImmediate_NoWork(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectQuery("UPDATE notifications").
		WillReturnRows(notificationRows(t))

	if _, err := store.ClaimImmediate(context.Background(), time.Minute); !errors.Is(err, notification.ErrNoWork) {
		t.Fatalf("expected notification.ErrNoWork, got %v", err)
	}
}

func TestQueueStore_ClaimDigest(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	a := notification.Notification{ID: 1, Recipient: "a@b.io", Digest: true, ScheduledFor: time.Now(), CreatedAt: time.Now()}
	b := notification.Notification{ID: 2, Recipient: "a@b.io", Digest: true, ScheduledFor: time.Now(), CreatedAt: time.Now()}
	mock.ExpectQuery("UPDATE notifications").
		WithArgs(60).
		WillReturnRows(notificationRows(t, a, b))

	got, err := store.ClaimDigest(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimDigest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
}

func TestQueueStore_Claim_QueryError(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectQuery("UPDATE notifications").WillReturnError(errors.New("boom"))

	if _, err := store.ClaimDigest(context.Background(), time.Minute); err == nil {
		t.Fatal("expected error")
	}
}

func TestQueueStore_Claim_BadPayload(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	rows := sqlmock.NewRows([]string{
		"id", "recipient", "category", "payload", "digest",
		"status", "attempts", "last_error", "scheduled_for", "sent_at", "created_at",
	}).
		AddRow(1, "a@b.io", notification.CategoryShare, []byte("{bad"), false,
			notification.StatusSending, 1, "", time.Now(), nil, time.Now())
	mock.ExpectQuery("UPDATE notifications").WillReturnRows(rows)

	if _, err := store.ClaimImmediate(context.Background(), time.Minute); err == nil {
		t.Fatal("expected payload decode error")
	}
}

func TestQueueStore_MarkSent(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("UPDATE notifications").
		WillReturnResult(sqlmock.NewResult(0, 2))

	if err := store.MarkSent(context.Background(), []int64{1, 2}); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
}

func TestQueueStore_MarkSent_Error(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("UPDATE notifications").WillReturnError(errors.New("boom"))

	if err := store.MarkSent(context.Background(), []int64{1}); err == nil {
		t.Fatal("expected error")
	}
}

func TestQueueStore_Retry(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("UPDATE notifications").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Retry(context.Background(), []int64{1}, "smtp timeout", 30*time.Second); err != nil {
		t.Fatalf("Retry: %v", err)
	}
}

func TestQueueStore_Retry_Error(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("UPDATE notifications").WillReturnError(errors.New("boom"))

	if err := store.Retry(context.Background(), []int64{1}, "e", time.Second); err == nil {
		t.Fatal("expected error")
	}
}

func TestQueueStore_Fail(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("UPDATE notifications").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Fail(context.Background(), []int64{1}, "gave up"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
}

func TestQueueStore_PurgeOld(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("DELETE FROM notifications").
		WithArgs(int((30 * 24 * time.Hour).Seconds()), int((7 * 24 * time.Hour).Seconds())).
		WillReturnResult(sqlmock.NewResult(0, 3))

	n, err := store.PurgeOld(context.Background(), 30*24*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("PurgeOld: %v", err)
	}
	if n != 3 {
		t.Errorf("purged = %d; want 3", n)
	}
}

func TestQueueStore_PurgeOld_Error(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("DELETE FROM notifications").WillReturnError(errors.New("boom"))

	if _, err := store.PurgeOld(context.Background(), time.Hour, time.Hour); err == nil {
		t.Fatal("expected error")
	}
}

func TestQueueStore_Fail_Error(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()

	mock.ExpectExec("UPDATE notifications").WillReturnError(errors.New("boom"))

	if err := store.Fail(context.Background(), []int64{1}, "e"); err == nil {
		t.Fatal("expected error")
	}
}
