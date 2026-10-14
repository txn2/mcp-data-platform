package catalogimport

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRunner is a runner over a mocked database whose opens are counted.
func mockRunner(t *testing.T, openErr error) (*Runner, sqlmock.Sqlmock, *int) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	opens := 0
	rn := NewRunner(db, New(nil, NewStore(db), Options{}), nil, func(string, string) (Source, error) {
		opens++
		return nil, openErr
	})
	return rn, mock, &opens
}

func TestImportOnUpgradeWithNothingConfigured(t *testing.T) {
	rn, mock, opens := mockRunner(t, nil)
	rn.ImportOnUpgrade(context.Background())
	assert.Zero(t, *opens)
	assert.Empty(t, rn.Configured())
	_, err := rn.StartConfigured(context.Background(), "ana")
	require.ErrorIs(t, err, ErrNoSource)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportOnUpgradeAfterAFinishedImport(t *testing.T) {
	rn, mock, opens := mockRunner(t, nil)
	rn.Configure(&SourceConfig{URL: "http://dh", Token: "t"})
	assert.Equal(t, "http://dh", rn.Configured())
	mock.ExpectQuery(regexp.QuoteMeta(selectImported)).WithArgs("http://dh").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	rn.ImportOnUpgrade(context.Background())
	assert.Zero(t, *opens, "an imported DataHub is not imported again")

	mock.ExpectQuery(regexp.QuoteMeta(selectImported)).WillReturnError(errBoom)
	rn.ImportOnUpgrade(context.Background())
	assert.Zero(t, *opens, "an unreadable record starts nothing")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportOnUpgradeStartsAnImport(t *testing.T) {
	rn, mock, opens := mockRunner(t, errBoom)
	rn.Configure(&SourceConfig{URL: "http://dh", Token: "t"})
	mock.ExpectQuery(regexp.QuoteMeta(selectImported)).WithArgs("http://dh").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	rn.ImportOnUpgrade(context.Background())
	assert.Equal(t, 1, *opens, "an import is started; this one cannot open DataHub")

	rn, mock, opens = mockRunner(t, nil)
	rn.Configure(&SourceConfig{URL: "http://dh", Token: "t"})
	mock.ExpectQuery(regexp.QuoteMeta(selectImported)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	rn.ImportOnUpgrade(context.Background())
	assert.Equal(t, 1, *opens)
	assert.NoError(t, mock.ExpectationsWereMet(), "another replica's run is left to finish")
}

func TestStopOnNoRunner(_ *testing.T) {
	var rn *Runner
	rn.Stop()
}

func TestParseDatasetURN(t *testing.T) {
	got, err := parseDatasetURN("urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.a,b,PROD)")
	require.NoError(t, err)
	assert.Equal(t, datasetURN{Platform: "urn:li:dataPlatform:trino", Name: "hive.sales.a,b", Env: "PROD"}, got)
	for _, bad := range []string{"urn:li:corpuser:x", "urn:li:dataset:(urn:li:dataPlatform:trino)", "urn:li:dataset:(a,b"} {
		_, err := parseDatasetURN(bad)
		assert.Error(t, err, bad)
	}
}
