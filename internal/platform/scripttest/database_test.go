package scripttest

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// usersDB is one clone: a users table whose email is unique.
type usersDB struct {
	mu     sync.Mutex
	emails map[string]bool
}

func (*usersDB) HasConnection(name string) bool { return name == "shop" }

func (d *usersDB) Query(context.Context, postgres.Statement) (*postgres.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var users int64
	for range d.emails {
		users++
	}
	return &postgres.Result{
		Connection: "shop", Command: "SELECT", Columns: []postgres.Column{{Name: "n", Type: "bigint"}},
		Rows: [][]any{{users}},
	}, nil
}

func (d *usersDB) Execute(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	params, _ := s.Params.([]any)
	email, _ := params[0].(string)
	if d.emails[email] {
		return nil, &postgres.FailedError{Code: postgres.CodeStatementFailed, Summary: "postgres: statement failed with SQLSTATE 23505 (unique_violation); constraint users_email_key"}
	}
	d.emails[email] = true
	return &postgres.Result{Connection: "shop", Command: "INSERT", RowsAffected: 1, Rows: [][]any{}}, nil
}

func (*usersDB) Batch(context.Context, string, []postgres.Statement) ([]*postgres.Result, error) {
	return nil, errors.New("unused")
}

func (*usersDB) Begin(context.Context, postgres.TxOptions) (scriptsession.AppDBTx, error) {
	return nil, errors.New("unused")
}

func (*usersDB) OpenCursor(context.Context, postgres.Statement, int) (scriptsession.AppDBCursor, error) {
	return nil, nil //nolint:nilnil // never called
}

func (*usersDB) CopyIn(context.Context, postgres.CopyIn) (int64, error) { return 0, nil }

func (*usersDB) CopyOut(context.Context, postgres.Statement, string, io.Writer, int64) (*postgres.CopyOutResult, error) {
	return nil, nil //nolint:nilnil // never called
}

// live is the connection's own database, which a test never reaches.
type live struct{ usersDB }

func (*live) Execute(context.Context, postgres.Statement) (*postgres.Result, error) {
	return nil, errors.New("the connection's own database was reached")
}

const usersScript = `def main():
    """Add a user."""
    platform.execute("INSERT INTO users (email) VALUES (:e)", connection = "shop", params = {"e": "first@example.com"})

def test_duplicate_email_is_refused():
    testing.database("shop")
    main()
    msg = assert.fails(lambda: platform.execute("INSERT INTO users (email) VALUES (:e)", connection = "shop", params = {"e": "first@example.com"}))
    assert.contains(msg, "unique_violation")
    assert.eq(platform.query("SELECT count(*) AS n FROM users", connection = "shop")["rows"][0]["n"], 1)

def test_each_test_starts_from_a_new_database():
    testing.database("shop")
    assert.eq(platform.query("SELECT count(*) AS n FROM users", connection = "shop")["rows"][0]["n"], 0)
`

func TestTestingDatabase_AnswersFromACloneDroppedAfterEachTest(t *testing.T) {
	var mu sync.Mutex
	opened, dropped := 0, 0
	scriptsession.InstallAppDB(scriptsession.AppDB{
		Runner: func() scriptsession.AppDBRunner { return &live{} },
		TestDatabase: func(context.Context, string, []string) (scriptsession.AppDBRunner, func(), error) {
			mu.Lock()
			defer mu.Unlock()
			opened++
			return &usersDB{emails: map[string]bool{}}, func() { mu.Lock(); dropped++; mu.Unlock() }, nil
		},
	})
	t.Cleanup(func() { scriptsession.InstallAppDB(scriptsession.AppDB{}) })

	report, err := Run(context.Background(), Request{Source: usersScript, Name: "users"})
	require.NoError(t, err)
	for _, r := range report.Tests {
		assert.True(t, r.Passed, "%s: %s", r.Name, r.Failure)
	}
	assert.Equal(t, 2, report.Passed)
	assert.Equal(t, opened, dropped, "every clone is dropped")
	assert.GreaterOrEqual(t, opened, 2, "each test has a database of its own")
	assert.Equal(t, 100.0, report.Coverage.Percent, "coverage counts statements reached against a clone")
}

func TestTestingDatabase_ReportsWhyNoneCanBeBuilt(t *testing.T) {
	scriptsession.InstallAppDB(scriptsession.AppDB{
		Runner: func() scriptsession.AppDBRunner { return &live{} },
		TestDatabase: func(context.Context, string, []string) (scriptsession.AppDBRunner, func(), error) {
			return nil, nil, errors.New(`appdb: user "orders_ro" of connection "shop" lacks the CREATEDB privilege`)
		},
	})
	t.Cleanup(func() { scriptsession.InstallAppDB(scriptsession.AppDB{}) })
	report, err := Run(context.Background(), Request{Source: usersScript, Name: "users"})
	require.NoError(t, err)
	require.Len(t, report.Tests, 2)
	assert.False(t, report.Tests[0].Passed)
	assert.Contains(t, report.Tests[0].Failure, "CREATEDB")
	assert.True(t, strings.HasPrefix(report.Tests[0].Failure, "in testing.database:"), report.Tests[0].Failure)

	report, err = Run(context.Background(), Request{Source: strings.Replace(usersScript, `testing.database("shop")`, `testing.database("")`, 1), Name: "users"})
	require.NoError(t, err)
	assert.Contains(t, report.Tests[0].Failure, "name the application database")
}

func TestTestingDatabase_NamesTheSchemasItIsBuiltFrom(t *testing.T) {
	var got [][]string
	scriptsession.InstallAppDB(scriptsession.AppDB{
		Runner: func() scriptsession.AppDBRunner { return &live{} },
		TestDatabase: func(_ context.Context, _ string, schemas []string) (scriptsession.AppDBRunner, func(), error) {
			got = append(got, schemas)
			return &usersDB{emails: map[string]bool{}}, func() {}, nil
		},
	})
	t.Cleanup(func() { scriptsession.InstallAppDB(scriptsession.AppDB{}) })
	source := strings.Replace(usersScript, `testing.database("shop")`, `testing.database("shop", schemas = ["app", "audit"])`, 1)
	report, err := Run(context.Background(), Request{Source: source, Name: "users"})
	require.NoError(t, err)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
	require.NotEmpty(t, got)
	assert.Equal(t, []string{"app", "audit"}, got[0], "the first test names two schemas")

	for _, bad := range []string{`schemas = [1]`, `schemas = [""]`, `schemas = "app"`} {
		source = strings.Replace(usersScript, `testing.database("shop")`, `testing.database("shop", `+bad+`)`, 1)
		report, err = Run(context.Background(), Request{Source: source, Name: "users"})
		require.NoError(t, err)
		assert.False(t, report.Tests[0].Passed, bad)
	}
}
