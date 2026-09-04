package provenancesweep

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pass settles the assets written before a prune took their captures with
// their versions (#1623). What matters is which cap it applies, that it writes
// only the assets with something to trim, and that one refused row does not
// stop the rest.

func candidateRows(rows ...[]driver.Value) *sqlmock.Rows {
	r := sqlmock.NewRows([]string{"id", "watermark", "trimmable"})
	for _, row := range rows {
		r.AddRow(row...)
	}
	return r
}

func TestRun_TrimsEveryCandidate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("FROM portal_assets").
		WithArgs(12).
		WillReturnRows(candidateRows(
			[]driver.Value{"asset-a", 321, 320},
			[]driver.Value{"asset-b", 4, 3},
		))
	mock.ExpectExec("UPDATE portal_assets").WithArgs("asset-a", 321).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE portal_assets").WithArgs("asset-b", 4).
		WillReturnResult(sqlmock.NewResult(0, 1))

	Run(context.Background(), db, new(12))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The cap the pass applies is the one the prune applies: the deployment
// default where the configuration names one, and the platform's own where it
// does not. It is bound into the candidate query, so the query's argument is
// the whole of the assertion.
func TestRun_AppliesTheDeploymentDefaultCap(t *testing.T) {
	tests := []struct {
		name    string
		config  *int
		wantArg int
	}{
		{"configured", new(5), 5},
		{"unconfigured falls back to the platform default", nil, 100},
		{"unlimited", new(0), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close() //nolint:errcheck // test cleanup

			mock.ExpectQuery("FROM portal_assets").WithArgs(tt.wantArg).
				WillReturnRows(candidateRows())

			Run(context.Background(), db, tt.config)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// An asset the query named but with nothing below the watermark is not written:
// the pass exists to settle a library, not to rewrite every row in it.
func TestRun_SkipsAnAssetWithNothingToTrim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(candidateRows([]driver.Value{"asset-a", 8, 0}))

	Run(context.Background(), db, new(3))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// One row the database refused is not a reason to leave the rest carrying
// history for versions that no longer exist.
func TestRun_ContinuesPastARefusedAsset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(candidateRows(
			[]driver.Value{"asset-a", 9, 7},
			[]driver.Value{"asset-b", 9, 7},
		))
	mock.ExpectExec("UPDATE portal_assets").WithArgs("asset-a", 9).
		WillReturnError(errors.New("deadlock detected"))
	mock.ExpectExec("UPDATE portal_assets").WithArgs("asset-b", 9).
		WillReturnResult(sqlmock.NewResult(0, 1))

	Run(context.Background(), db, new(3))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRun_CandidateQueryFailureStopsThePass(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("FROM portal_assets").WillReturnError(errors.New("db down"))

	Run(context.Background(), db, new(3))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRun_ScanFailureStopsThePass(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(candidateRows([]driver.Value{"asset-a", "not-a-number", 7}))

	Run(context.Background(), db, new(3))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A deployment with no database has no assets to sweep and no handle to ask.
func TestRun_NoDatabase(_ *testing.T) {
	Run(context.Background(), nil, new(3))
}
