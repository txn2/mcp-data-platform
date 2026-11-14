package sqlprotect

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var protected = map[string]string{"hive.hr.employees.ssn": "mask-ssn"}

// TestCheck_SelectingAsItIs is what a covered caller may send: the column
// selected as it is, anywhere a select list holds it, and statements that do
// not read the table.
func TestCheck_SelectingAsItIs(t *testing.T) {
	for _, sql := range []string{
		`SELECT name, ssn FROM hive.hr.employees`,
		`SELECT * FROM hive.hr.employees`,
		`SELECT e.ssn FROM hr.employees e`,
		`SELECT ssn AS ssn FROM employees`,
		`SELECT ssn FROM (SELECT ssn, name FROM hive.hr.employees) x`,
		`WITH x AS (SELECT ssn FROM hive.hr.employees) SELECT ssn FROM x`,
		`SELECT ssn FROM hive.hr.employees UNION ALL SELECT ssn FROM hive.hr.employees`,
		`SELECT name FROM hive.hr.employees WHERE dept = 7 ORDER BY name LIMIT 10`,
		`SELECT count(*) FROM hive.hr.employees`,
		`SELECT ssn FROM hive.other.people WHERE ssn = '1'`,
		`SELECT a FROM t WHERE a = 1`,
	} {
		assert.NoError(t, Check(context.Background(), sql, protected, nil), sql)
	}
}

// TestCheck_EveryOtherUseIsRefused names the column and the handler for each
// way a value could be learned or copied.
func TestCheck_EveryOtherUseIsRefused(t *testing.T) {
	for _, sql := range []string{
		`SELECT substr(ssn, 1, 3) FROM hive.hr.employees`,
		`SELECT name FROM hive.hr.employees WHERE ssn = '123-45-6789'`,
		`SELECT a.name FROM hive.hr.employees a JOIN hive.hr.employees b ON a.ssn = b.ssn`,
		`SELECT ssn, count(*) FROM hive.hr.employees GROUP BY ssn`,
		`SELECT ssn AS x FROM hive.hr.employees`,
		`SELECT DISTINCT ssn FROM hive.hr.employees`,
		`SELECT name FROM hive.hr.employees ORDER BY ssn`,
		`SELECT ssn FROM hive.hr.employees ORDER BY 1`,
		`SELECT ssn, name FROM hive.hr.employees GROUP BY 1, 2`,
		`SELECT row_number() OVER (PARTITION BY ssn) FROM hive.hr.employees`,
		`SELECT ssn FROM hive.hr.employees INTERSECT SELECT '123-45-6789'`,
		`SELECT ssn FROM hive.hr.employees UNION SELECT '123-45-6789'`,
		`SELECT name FROM hive.hr.employees JOIN hive.hr.badges USING (ssn)`,
		`SELECT name FROM hive.hr.employees NATURAL JOIN hive.hr.badges`,
		`SELECT y FROM (SELECT ssn FROM hive.hr.employees) x(y)`,
		`WITH x(y) AS (SELECT ssn FROM hive.hr.employees) SELECT y FROM x`,
		`WITH x AS (SELECT ssn FROM hive.hr.employees) SELECT ssn FROM x WHERE ssn LIKE '1%'`,
		`SELECT name FROM hive.hr.employees WHERE length(ssn) > 3`,
		`SELECT CASE WHEN ssn > '5' THEN 1 END FROM hive.hr.employees`,
		`INSERT INTO hive.hr.copy SELECT ssn FROM hive.hr.employees`,
		`CREATE TABLE hive.hr.copy AS SELECT ssn FROM hive.hr.employees`,
	} {
		err := Check(context.Background(), sql, protected, nil)
		var refusal *RefusalError
		require.True(t, errors.As(err, &refusal), "%s was not refused: %v", sql, err)
		assert.Equal(t, "hive.hr.employees.ssn", refusal.Column, sql)
		assert.Equal(t, "mask-ssn", refusal.Handler, sql)
		assert.Contains(t, err.Error(), "hive.hr.employees.ssn is protected by mask-ssn", sql)
	}
}

// TestCheck_AStatementThatCannotBeRead is refused when it names a protected
// column, and let through when it does not.
func TestCheck_AStatementThatCannotBeRead(t *testing.T) {
	err := Check(context.Background(), `SELECT try_cast(ssn AS integer) FROM hive.hr.employees`, protected, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be read")
	assert.NoError(t, Check(context.Background(), `SELECT try_cast(dept AS integer) FROM hive.hr.employees`, protected, nil))
	assert.NoError(t, Check(context.Background(), `SELECT try_cast(x AS integer) FROM t WHERE ssnx = 1`, protected, nil), "a longer word is not the column")
}

// TestCheck_AViewIsFollowed reads a view's definition: a view over the
// protected table brings its column into scope.
func TestCheck_AViewIsFollowed(t *testing.T) {
	views := func(_ context.Context, _, _, name string) string {
		if name == "staff" {
			return `SELECT name, ssn FROM hive.hr.employees`
		}
		return ""
	}
	require.Error(t, Check(context.Background(), `SELECT name FROM hive.hr.staff WHERE ssn = '1'`, protected, views))
	assert.NoError(t, Check(context.Background(), `SELECT name, ssn FROM hive.hr.staff`, protected, views))
	assert.NoError(t, Check(context.Background(), `SELECT name FROM hive.hr.other WHERE ssn = '1'`, protected, views), "a relation that is no view and no protected table")
	require.Error(t, ViewUse(context.Background(), `SELECT name, substr(ssn, 1, 3) AS p FROM hive.hr.employees`, protected))
}

// TestCheck_NothingProtected checks nothing.
func TestCheck_NothingProtected(t *testing.T) {
	assert.NoError(t, Check(context.Background(), `SELECT substr(ssn, 1, 3) FROM hive.hr.employees`, nil, nil))
	assert.NoError(t, Check(context.Background(), `SELECT 1`, map[string]string{"nodots": "x"}, nil))
}
