package scriptstore

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// formSource defines form_spec(req) and form_submit(req, values): an endpoint
// handler on a script saved with it is a form (#2441).
const formSource = `def form_spec(req):
    """The form."""
    return {"fields": [{"name": "email", "type": "email"}]}

def form_submit(req, values):
    """Accepts."""
    return None
`

// argsWithAt is n AnyArg matchers with want at position at (1-based, as the
// statement numbers its parameters).
func argsWithAt(n, at int, want driver.Value) []driver.Value {
	out := make([]driver.Value, n)
	for i := range out {
		out[i] = sqlmock.AnyArg()
	}
	out[at-1] = want
	return out
}

// TestWritesIsFormFromTheSource proves every save writes is_form from the
// source it saves, not from the record the caller handed in (#2441): the
// script_handlers view reads it to report an endpoint on the script as a
// form, so a caller cannot make a script a form, or stop it being one, by
// setting the field.
func TestWritesIsFormFromTheSource(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "a form source", source: formSource, want: true},
		{name: "any other source", source: "def main():\n    return 1\n", want: false},
	}
	for _, tc := range cases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
				WithArgs(argsWithAt(15, 15, tc.want)...).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at", "is_form"}).
					AddRow("script_1", rowTime, rowTime, tc.want))
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_resource_pins")).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()

			sc := &script.Script{Name: "contact", Source: tc.source, OwnerEmail: "j@example.com", IsForm: !tc.want}
			require.NoError(t, s.Create(context.Background(), sc, testAuthor))
			assert.Equal(t, tc.want, sc.IsForm, "the record carries what the database stored")
			require.NoError(t, mock.ExpectationsWereMet())
		})

		t.Run("update: "+tc.name, func(t *testing.T) {
			s, mock := newMock(t)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).
				WithArgs(argsWithAt(18, 18, tc.want)...).
				WillReturnRows(sqlmock.NewRows([]string{"changed"}).AddRow(false))
			expectRestamp(mock)
			mock.ExpectCommit()

			sc := &script.Script{ID: "script_1", Source: tc.source, IsForm: !tc.want}
			require.NoError(t, s.Update(context.Background(), sc))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestScanScript_ReadsIsForm proves the column reaches the record in its
// place in the scan order.
func TestScanScript_ReadsIsForm(t *testing.T) {
	for _, want := range []bool{true, false} {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
			WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(scriptRow(rowSpec{
				id: "script_1", name: "contact", owner: "j@example.com", paramsJSON: emptyParams(t), isForm: want, released: 2,
			})...))

		sc, err := s.GetByID(context.Background(), "script_1")
		require.NoError(t, err)
		require.NotNil(t, sc)
		assert.Equal(t, want, sc.IsForm)
		assert.Equal(t, 2, sc.ReleasedVersion, "the column after is_form still lands where it belongs")
	}
}
