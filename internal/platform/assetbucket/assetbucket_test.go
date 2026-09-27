package assetbucket

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pass writes the portal bucket into asset and version rows naming none,
// and only those.
func TestRun_FillsRowsNamingNoBucket(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec(regexp.QuoteMeta(fillAssetsSQL)).WithArgs("portal").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(fillVersionsSQL)).WithArgs("portal").WillReturnResult(sqlmock.NewResult(0, 5))

	assets, versions, err := Run(context.Background(), db, "portal")
	require.NoError(t, err)
	assert.Equal(t, int64(2), assets)
	assert.Equal(t, int64(5), versions)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// runLoggedAgainst runs the logged form over a fresh mock answering with the
// given counts, and asserts it issued both statements.
func runLoggedAgainst(t *testing.T, bucket string, assets, versions int64) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec(regexp.QuoteMeta(fillAssetsSQL)).WillReturnResult(sqlmock.NewResult(0, assets))
	mock.ExpectExec(regexp.QuoteMeta(fillVersionsSQL)).WillReturnResult(sqlmock.NewResult(0, versions))
	RunLogged(context.Background(), db, bucket)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunLogged_IssuesBothStatements(t *testing.T) {
	runLoggedAgainst(t, "portal", 1, 3)
	runLoggedAgainst(t, "portal", 0, 0)
}

// With no portal bucket there is nothing to name, and nothing is written.
func TestRun_NoBucketWritesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	assets, versions, err := Run(context.Background(), db, "")
	require.NoError(t, err)
	assert.Zero(t, assets+versions)
	assert.NoError(t, mock.ExpectationsWereMet())

	_, _, err = Run(context.Background(), nil, "portal")
	assert.NoError(t, err)
}

func TestRun_Failures(t *testing.T) {
	boom := errors.New("down")
	for name, setup := range map[string]func(sqlmock.Sqlmock){
		"assets": func(m sqlmock.Sqlmock) {
			m.ExpectExec(regexp.QuoteMeta(fillAssetsSQL)).WillReturnError(boom)
		},
		"versions": func(m sqlmock.Sqlmock) {
			m.ExpectExec(regexp.QuoteMeta(fillAssetsSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec(regexp.QuoteMeta(fillVersionsSQL)).WillReturnError(boom)
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			setup(mock)
			_, _, err = Run(context.Background(), db, "portal")
			require.ErrorIs(t, err, boom)

			db2, mock2, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db2.Close() }()
			setup(mock2)
			RunLogged(context.Background(), db2, "portal")
			assert.NoError(t, mock2.ExpectationsWereMet())
		})
	}
}
