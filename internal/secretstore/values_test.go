package secretstore

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const valuesSQL = `SELECT value FROM gateway_secrets WHERE value IS NOT NULL`

// TestValues is #2820's read: every stored value decrypted for the content
// check, a value that does not decrypt left out, and a failed read reported.
func TestValues(t *testing.T) {
	s, mock := newMock(t, prefixEncryptor{})
	mock.ExpectQuery(valuesSQL).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:hunter22").AddRow("enc:s3cret-key"))
	got, err := s.Values(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"hunter22", "s3cret-key"}, got)

	s, mock = newMock(t, prefixEncryptor{fail: true})
	mock.ExpectQuery(valuesSQL).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:hunter22"))
	got, err = s.Values(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got, "a value that does not decrypt is no value a caller holds")

	s, mock = newMock(t, nil)
	mock.ExpectQuery(valuesSQL).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("plain"))
	got, err = s.Values(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"plain"}, got)

	s, mock = newMock(t, prefixEncryptor{})
	mock.ExpectQuery(valuesSQL).WillReturnError(errors.New("down"))
	_, err = s.Values(context.Background())
	require.Error(t, err)

	s, mock = newMock(t, prefixEncryptor{})
	mock.ExpectQuery(valuesSQL).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:a").RowError(0, errors.New("broken")))
	_, err = s.Values(context.Background())
	require.Error(t, err)
}
