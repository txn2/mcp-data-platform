package scriptrefs

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	namesShop = `def main():
    rows = platform.query("SELECT email FROM users WHERE id = :id", connection = "shop", params = {"id": 1})["rows"]
    return rows
`
	namesOtherConnection = `def main():
    platform.query("SELECT email FROM users", connection = "warehouse")
`
	namesKeyOnly = `def main():
    row = platform.query("SELECT * FROM users", connection = "shop")["rows"][0]
    return row["email"]
`
	namesSubstring = `def main():
    platform.query("SELECT email_verified FROM users", connection = "shop")
`
)

func TestScriptReferences_NamesTheScriptsThatNameTheObject(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	rows := sqlmock.NewRows([]string{"name", "source_code"}).
		AddRow("intake", namesShop).
		AddRow("warehouse-report", namesOtherConnection).
		AddRow("reader", namesKeyOnly).
		AddRow("verified", namesSubstring).
		AddRow("broken", "def main(:")
	mock.ExpectQuery(`SELECT name, source_code FROM scripts WHERE source_code ILIKE \$1`).WithArgs("%email%").WillReturnRows(rows)
	mock.ExpectQuery(`SELECT name, source_code FROM scripts`).WithArgs(`%user\_id%`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "source_code"}))

	got, err := Lookup(db)(context.Background(), "shop", []string{"email", "user_id"})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"email": {"intake", "reader"}}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScriptReferences_ReadFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT name, source_code FROM scripts`).WillReturnError(errors.New("down"))
	_, err = Lookup(db)(context.Background(), "shop", []string{"email"})
	assert.ErrorContains(t, err, "reading the scripts that name email")

	mock.ExpectQuery(`SELECT name, source_code FROM scripts`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "source_code"}).AddRow("a", namesShop).RowError(0, errors.New("cut")))
	_, err = Lookup(db)(context.Background(), "shop", []string{"email"})
	assert.Error(t, err)
}

func TestHasWordAndLikeEscape(t *testing.T) {
	assert.True(t, hasWord("SELECT Email FROM users", "email"))
	assert.True(t, hasWord("email", "email"))
	assert.True(t, hasWord("x.email_verified, email", "email"))
	assert.False(t, hasWord("email_verified", "email"))
	assert.False(t, hasWord("my_email", "email"))
	assert.Equal(t, `100\%\_a\\b`, likeEscape(`100%_a\b`))
}
