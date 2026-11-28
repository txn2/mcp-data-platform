package embedyield

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, 0), mock
}

// TestStore_MarkClearBusy pins the three statements and what each is handed:
// a mark holds for the default hold under this process's id, a clear removes
// this replica's row and every lapsed one, and busy asks for any live row.
func TestStore_MarkClearBusy(t *testing.T) {
	s, mock := newMock(t)
	if s.instance == "" || s.hold != DefaultHold {
		t.Fatalf("instance %q hold %v", s.instance, s.hold)
	}
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO embed_interactive").WithArgs(s.instance, DefaultHold.Seconds()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.Mark(ctx); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	if busy, err := s.Busy(ctx); err != nil || !busy {
		t.Fatalf("Busy = %v, %v; want true", busy, err)
	}
	mock.ExpectExec("DELETE FROM embed_interactive WHERE instance = \\$1 OR until < now\\(\\)").WithArgs(s.instance).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// TestStore_ErrorsSurface: each statement's failure reaches the gate, which
// treats it as not published or idle.
func TestStore_ErrorsSurface(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	boom := errors.New("db down")
	mock.ExpectExec("INSERT").WillReturnError(boom)
	mock.ExpectExec("DELETE").WillReturnError(boom)
	mock.ExpectQuery("SELECT").WillReturnError(boom)
	if err := s.Mark(ctx); !errors.Is(err, boom) {
		t.Errorf("Mark: %v", err)
	}
	if err := s.Clear(ctx); !errors.Is(err, boom) {
		t.Errorf("Clear: %v", err)
	}
	if _, err := s.Busy(ctx); !errors.Is(err, boom) {
		t.Errorf("Busy: %v", err)
	}
}

// TestNew_DrawsADistinctIDPerProcess: two replicas never share a row.
func TestNew_DrawsADistinctIDPerProcess(t *testing.T) {
	a, b := New(nil, 0), New(nil, 0)
	if a.instance == b.instance {
		t.Errorf("two stores drew one id %q", a.instance)
	}
}

// TestStore_BackgroundMarks pins the background statements (#2846): a mark
// writes this process's expected input in milliseconds for the background
// hold, the read sums the live marks, and a clear removes this replica's row
// and every lapsed one.
func TestStore_BackgroundMarks(t *testing.T) {
	s, mock := newMock(t)
	ctx := context.Background()
	mock.ExpectExec("INSERT INTO embed_background").
		WithArgs(s.instance, int64(4500), embedding.DefaultBackgroundHold.Seconds()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.MarkBackground(ctx, 4500*time.Millisecond); err != nil {
		t.Fatalf("MarkBackground: %v", err)
	}
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(input_ms\\), 0\\) FROM embed_background WHERE until > now\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(int64(7250)))
	if d, err := s.BackgroundInput(ctx); err != nil || d != 7250*time.Millisecond {
		t.Fatalf("BackgroundInput = %s, %v; want 7.25s", d, err)
	}
	mock.ExpectExec("DELETE FROM embed_background WHERE instance = \\$1 OR until < now\\(\\)").WithArgs(s.instance).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.ClearBackground(ctx); err != nil {
		t.Fatalf("ClearBackground: %v", err)
	}

	boom := errors.New("db down")
	mock.ExpectExec("INSERT").WillReturnError(boom)
	mock.ExpectExec("DELETE").WillReturnError(boom)
	mock.ExpectQuery("SELECT").WillReturnError(boom)
	if err := s.MarkBackground(ctx, time.Second); !errors.Is(err, boom) {
		t.Errorf("MarkBackground: %v", err)
	}
	if err := s.ClearBackground(ctx); !errors.Is(err, boom) {
		t.Errorf("ClearBackground: %v", err)
	}
	if _, err := s.BackgroundInput(ctx); !errors.Is(err, boom) {
		t.Errorf("BackgroundInput: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
