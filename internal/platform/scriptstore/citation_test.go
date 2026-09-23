package scriptstore

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const citedID = "6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55"

// Citation answers a knowledge page's script citation (#1855): the display name
// and owner of a script that exists, the name when there is no display name,
// nil for one that does not, and nil without a query for an id scripts.id
// cannot hold.
func TestCitation(t *testing.T) {
	t.Run("an existing script carries its display name and owner", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).WithArgs(citedID).
			WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(scriptRow(rowSpec{
				id: citedID, name: "orders-sync", owner: "jane@example.com", paramsJSON: emptyParams(t),
			})...))

		c, err := s.Citation(context.Background(), citedID)
		require.NoError(t, err)
		require.NotNil(t, c)
		assert.Equal(t, "Daily", c.Label)
		assert.Equal(t, "jane@example.com", c.Owner)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a script with no display name is labeled by its name", func(t *testing.T) {
		s, mock := newMock(t)
		row := scriptRow(rowSpec{id: citedID, name: "orders-sync", owner: "jane@example.com", paramsJSON: emptyParams(t)})
		row[2] = "" // display_name
		mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).WithArgs(citedID).
			WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(row...))

		c, err := s.Citation(context.Background(), citedID)
		require.NoError(t, err)
		require.NotNil(t, c)
		assert.Equal(t, "orders-sync", c.Label)
	})

	t.Run("a missing script is nil", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).WithArgs(citedID).
			WillReturnRows(sqlmock.NewRows(scriptSelectColumns))

		c, err := s.Citation(context.Background(), citedID)
		require.NoError(t, err)
		assert.Nil(t, c)
	})

	t.Run("an id that is not a uuid is nil without a query", func(t *testing.T) {
		s, mock := newMock(t)
		c, err := s.Citation(context.Background(), "script_01HK7")
		require.NoError(t, err)
		assert.Nil(t, c)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a read failure is returned", func(t *testing.T) {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).WithArgs(citedID).
			WillReturnError(errors.New("db down"))

		c, err := s.Citation(context.Background(), citedID)
		require.Error(t, err)
		assert.Nil(t, c)
	})
}
