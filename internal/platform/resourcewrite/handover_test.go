package resourcewrite

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandOverAddresses_Statements is the handover's transaction: both rows
// checked, the aliases re-pointed before the copy's own address is aliased,
// and every failure rolled back (#2842). The statements themselves are run
// against PostgreSQL by TestHandOverAddresses_RealDB.
func TestHandOverAddresses_Statements(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	count := func(m sqlmock.Sqlmock, n int) {
		m.ExpectBegin()
		m.ExpectQuery(`SELECT count\(\*\) FROM resources`).WithArgs("c", "k").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(n))
	}
	tests := []struct {
		name  string
		setup func(m sqlmock.Sqlmock)
		want  error
		fails bool
	}{
		{"both rows, aliases then own address", func(m sqlmock.Sqlmock) {
			count(m, 2)
			m.ExpectExec(`UPDATE resource_uri_aliases SET resource_id = \$2 WHERE resource_id = \$1`).WithArgs("c", "k").
				WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec(`INSERT INTO resource_uri_aliases`).WithArgs("c", "k").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectCommit()
		}, nil, false},
		{"a row missing", func(m sqlmock.Sqlmock) { count(m, 1); m.ExpectRollback() }, errHandoverMissing, true},
		{"the begin fails", func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(boom) }, boom, true},
		{"the count fails", func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectQuery(`SELECT count`).WillReturnError(boom)
			m.ExpectRollback()
		}, boom, true},
		{"the re-point fails", func(m sqlmock.Sqlmock) {
			count(m, 2)
			m.ExpectExec(`UPDATE resource_uri_aliases`).WillReturnError(boom)
			m.ExpectRollback()
		}, boom, true},
		{"the alias fails", func(m sqlmock.Sqlmock) {
			count(m, 2)
			m.ExpectExec(`UPDATE resource_uri_aliases`).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectExec(`INSERT INTO resource_uri_aliases`).WillReturnError(boom)
			m.ExpectRollback()
		}, boom, true},
		{"the commit fails", func(m sqlmock.Sqlmock) {
			count(m, 2)
			m.ExpectExec(`UPDATE resource_uri_aliases`).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectExec(`INSERT INTO resource_uri_aliases`).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectCommit().WillReturnError(boom)
		}, boom, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			tt.setup(mock)
			err = HandOverAddresses(ctx, db, "c", "k")
			if tt.fails {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.want)
			} else {
				require.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
	assert.Error(t, HandOverAddresses(ctx, nil, "same", "same"), "a file is not its own copy")
}
