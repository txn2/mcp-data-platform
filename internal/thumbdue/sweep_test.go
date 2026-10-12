package thumbdue

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// The sweep's statements are held to PostgreSQL by the real-database tests
// and the SQL gate; what is held here is the Go around them.

var errDB = errors.New("db down")

// chunkRows is the chunk size, as the mark statement is bound with it.
const chunkRows = int64(chunk)

func newMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func expectLock(mock sqlmock.Sqlmock, got bool) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).WithArgs(lockResources).
		WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(got))
}

func expectUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).WithArgs(lockResources).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectRecorded(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT rule FROM thumbnail_sweeps WHERE subject = $1")).
		WithArgs("resources").WillReturnRows(rows)
}

func chunkResult(n int64, last any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"count", "max"}).AddRow(n, last)
}

// A table is marked in chunks until a chunk comes back short, and the rule is
// recorded after the last.
func TestSweep_MarksInChunksAndRecordsTheRule(t *testing.T) {
	db, mock := newMock(t)
	expectLock(mock, true)
	expectRecorded(mock, sqlmock.NewRows([]string{"rule"}))
	mock.ExpectQuery("WITH next AS").WithArgs("", chunkRows).WillReturnRows(chunkResult(chunkRows, "r2000"))
	mock.ExpectQuery("WITH next AS").WithArgs("r2000", chunkRows).WillReturnRows(chunkResult(5, "r2005"))
	mock.ExpectExec("INSERT INTO thumbnail_sweeps").WithArgs("resources", "rule-1").WillReturnResult(sqlmock.NewResult(0, 1))
	expectUnlock(mock)

	marked, err := Sweep(context.Background(), db, Resources, "rule-1")
	if err != nil || marked != chunkRows+5 {
		t.Fatalf("Sweep = %d, %v; want %d", marked, err, chunkRows+5)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// A table already swept under the rule, a lock another replica holds and an
// empty table each mark nothing.
func TestSweep_MarksNothingWhenNothingIsOwed(t *testing.T) {
	t.Run("swept under the rule", func(t *testing.T) {
		db, mock := newMock(t)
		expectLock(mock, true)
		expectRecorded(mock, sqlmock.NewRows([]string{"rule"}).AddRow("rule-1"))
		expectUnlock(mock)
		if marked, err := Sweep(context.Background(), db, Resources, "rule-1"); err != nil || marked != 0 {
			t.Fatalf("Sweep = %d, %v", marked, err)
		}
	})
	t.Run("another replica sweeps", func(t *testing.T) {
		db, mock := newMock(t)
		expectLock(mock, false)
		if marked, err := Sweep(context.Background(), db, Resources, "rule-1"); err != nil || marked != 0 {
			t.Fatalf("Sweep = %d, %v", marked, err)
		}
	})
	t.Run("an empty table", func(t *testing.T) {
		db, mock := newMock(t)
		expectLock(mock, true)
		expectRecorded(mock, sqlmock.NewRows([]string{"rule"}).AddRow("rule-0"))
		mock.ExpectQuery("WITH next AS").WillReturnRows(chunkResult(0, nil))
		mock.ExpectExec("INSERT INTO thumbnail_sweeps").WillReturnResult(sqlmock.NewResult(0, 1))
		expectUnlock(mock)
		if marked, err := Sweep(context.Background(), db, Resources, "rule-1"); err != nil || marked != 0 {
			t.Fatalf("Sweep = %d, %v", marked, err)
		}
	})
}

// Each step's failure is reported, and the lock is released after one.
func TestSweep_EachFailureIsReported(t *testing.T) {
	for name, expect := range map[string]func(sqlmock.Sqlmock){
		"the lock": func(m sqlmock.Sqlmock) {
			m.ExpectQuery("pg_try_advisory_lock").WillReturnError(errDB)
		},
		"the record read": func(m sqlmock.Sqlmock) {
			expectLock(m, true)
			m.ExpectQuery("SELECT rule FROM thumbnail_sweeps").WillReturnError(errDB)
			expectUnlock(m)
		},
		"a chunk": func(m sqlmock.Sqlmock) {
			expectLock(m, true)
			expectRecorded(m, sqlmock.NewRows([]string{"rule"}))
			m.ExpectQuery("WITH next AS").WillReturnError(errDB)
			expectUnlock(m)
		},
		"the record write": func(m sqlmock.Sqlmock) {
			expectLock(m, true)
			expectRecorded(m, sqlmock.NewRows([]string{"rule"}))
			m.ExpectQuery("WITH next AS").WillReturnRows(chunkResult(1, "r1"))
			m.ExpectExec("INSERT INTO thumbnail_sweeps").WillReturnError(errDB)
			m.ExpectExec("pg_advisory_unlock").WillReturnError(errDB)
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, mock := newMock(t)
			expect(mock)
			if _, err := Sweep(context.Background(), db, Resources, "rule-1"); err == nil {
				t.Fatal("expected an error")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Error(err)
			}
		})
	}
	db, _ := newMock(t)
	if _, err := Sweep(context.Background(), db, Table("users"), "r"); err == nil {
		t.Error("a table the renderer does not draw from is swept")
	}
}
