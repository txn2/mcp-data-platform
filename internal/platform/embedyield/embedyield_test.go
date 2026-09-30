package embedyield

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
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
