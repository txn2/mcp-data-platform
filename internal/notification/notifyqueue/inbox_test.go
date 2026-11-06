package notifyqueue

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func TestCountUnread(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`WHERE recipient = $1 AND read_at IS NULL`)).
		WithArgs("a@b.io").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	n, err := store.CountUnread(context.Background(), "a@b.io")
	if err != nil || n != 3 {
		t.Fatalf("CountUnread = %d, %v", n, err)
	}
	mock.ExpectQuery("COUNT").WillReturnError(errors.New("boom"))
	if _, err := store.CountUnread(context.Background(), "a@b.io"); err == nil {
		t.Error("expected an error")
	}
}

func TestUnreadAmong(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()
	got, err := store.UnreadAmong(context.Background(), "a@b.io", nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no ids asks nothing: %v, %v", got, err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`recipient = $1 AND read_at IS NULL AND id = ANY($2)`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(2)))
	got, err = store.UnreadAmong(context.Background(), "a@b.io", []int64{1, 2})
	if err != nil || !got[2] || got[1] {
		t.Fatalf("UnreadAmong = %v, %v", got, err)
	}
	mock.ExpectQuery("SELECT id").WillReturnError(errors.New("boom"))
	if _, err := store.UnreadAmong(context.Background(), "a@b.io", []int64{1}); err == nil {
		t.Error("expected an error")
	}
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	if _, err := store.UnreadAmong(context.Background(), "a@b.io", []int64{1}); err == nil {
		t.Error("expected a scan error")
	}
}

func TestMarkRead(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`WHERE recipient = $1 AND read_at IS NULL AND id = ANY($2)`)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	if n, err := store.MarkRead(context.Background(), "a@b.io", []int64{1, 2}); err != nil || n != 2 {
		t.Fatalf("MarkRead(ids) = %d, %v", n, err)
	}
	mock.ExpectExec(`read_at IS NULL$`).WithArgs("a@b.io").WillReturnResult(sqlmock.NewResult(0, 5))
	if n, err := store.MarkRead(context.Background(), "a@b.io", nil); err != nil || n != 5 {
		t.Fatalf("MarkRead(all) = %d, %v", n, err)
	}
	mock.ExpectExec("UPDATE notifications").WillReturnError(errors.New("boom"))
	if _, err := store.MarkRead(context.Background(), "a@b.io", nil); err == nil {
		t.Error("expected an error")
	}
	mock.ExpectExec("UPDATE notifications").WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))
	if _, err := store.MarkRead(context.Background(), "a@b.io", nil); err == nil {
		t.Error("expected a rows-affected error")
	}
}

func TestRetractEvent(t *testing.T) {
	store, mock, done := newMockQueueStore(t)
	defer done()
	if err := store.RetractEvent(context.Background(), ""); err != nil {
		t.Fatalf("an empty id retracts nothing: %v", err)
	}
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notifications WHERE payload ? 'event_id'`)).
		WithArgs("evt_1", notification.StatusPending).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`SET payload = payload - 'message'`)).
		WithArgs("evt_1").WillReturnResult(sqlmock.NewResult(0, 2))
	if err := store.RetractEvent(context.Background(), "evt_1"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE").WillReturnError(errors.New("boom"))
	if err := store.RetractEvent(context.Background(), "evt_1"); err == nil {
		t.Error("expected an error")
	}
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE").WillReturnError(errors.New("boom"))
	if err := store.RetractEvent(context.Background(), "evt_1"); err == nil {
		t.Error("expected an error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestHistoryWhereUnread(t *testing.T) {
	clause, args := historyWhere(notification.HistoryFilter{Recipient: "a@b.io", Unread: true})
	if clause != " WHERE recipient = $1 AND read_at IS NULL" || len(args) != 1 {
		t.Errorf("clause = %q args = %v", clause, args)
	}
}
