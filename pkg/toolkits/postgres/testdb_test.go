package postgres

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serverSession is a test server: the databases on it by name, whether its
// user may create them, and every script it ran. CREATE, ALTER ... RENAME and
// DROP DATABASE change the set as PostgreSQL would.
type serverSession struct {
	mu        sync.Mutex
	createdb  bool
	databases map[string]bool
	scripts   []string
	// fail fails the script it matches.
	fail func(sql string) error
}

func newServer(createdb bool, dbs ...string) *serverSession {
	s := &serverSession{createdb: createdb, databases: map[string]bool{}}
	for _, d := range dbs {
		s.databases[d] = true
	}
	return s
}

func (*serverSession) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (s *serverSession) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Contains(sql, "rolcreatedb") {
		return ledgerRow{exists: s.createdb}
	}
	name, _ := args[0].(string)
	return ledgerRow{exists: s.databases[name]}
}

func (s *serverSession) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for d := range s.databases {
		switch {
		case strings.Contains(sql, "starts_with"):
			family, _ := args[0].(string)
			keep, _ := args[1].(string)
			if strings.HasPrefix(d, family) && d != keep {
				names = append(names, d)
			}
		case scratchName.MatchString(d):
			names = append(names, d)
		}
	}
	slices.Sort(names)
	rows := &fakeRows{}
	for _, n := range names {
		rows.rows = append(rows.rows, []any{n})
	}
	return rows, nil
}

func (*serverSession) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("no transactions")
}

var (
	createRE = regexp.MustCompile(`^CREATE DATABASE (\S+)(?: TEMPLATE (\S+))?$`)
	renameRE = regexp.MustCompile(`^ALTER DATABASE (\S+) RENAME TO (\S+)$`)
	dropRE   = regexp.MustCompile(`^DROP DATABASE IF EXISTS (\S+)`)
)

func (s *serverSession) Script(_ context.Context, sql string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts = append(s.scripts, sql)
	if s.fail != nil {
		if err := s.fail(sql); err != nil {
			return err
		}
	}
	switch {
	case createRE.MatchString(sql):
		m := createRE.FindStringSubmatch(sql)
		if m[2] != "" && !s.databases[m[2]] {
			return &pgconn.PgError{Code: "3D000"}
		}
		s.databases[m[1]] = true
	case renameRE.MatchString(sql):
		m := renameRE.FindStringSubmatch(sql)
		delete(s.databases, m[1])
		s.databases[m[2]] = true
	case dropRE.MatchString(sql):
		delete(s.databases, dropRE.FindStringSubmatch(sql)[1])
	}
	return nil
}

func (*serverSession) Release() {}

func (s *serverSession) has(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for d := range s.databases {
		if strings.HasPrefix(d, prefix) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out
}

// fakeScratch builds each throwaway database's toolkit over a fake session,
// kept in built by database.
func fakeScratch(built map[string]*fakeSession) func(string, Config) (*Toolkit, error) {
	return func(name string, cfg Config) (*Toolkit, error) {
		sess := &fakeSession{ledger: fakeLedger{exists: true}}
		built[cfg.Database] = sess
		cfg.ConnectionName, cfg.LedgerSchema = name, DefaultLedgerSchema
		c := &conn{
			cfg: cfg, types: newTypeCache(), ledgerQ: &sess.ledger,
			openSession: func(context.Context) (session, error) { return sess, nil },
		}
		return &Toolkit{name: name, connections: map[string]*conn{name: c}}, nil
	}
}

// testServerToolkit is a toolkit whose shop connection is its own test
// server, srv, and whose throwaway databases are served by fake sessions
// recording what each was built with.
func testServerToolkit(srv *serverSession, cfg Config) (tk *Toolkit, built map[string]*fakeSession) {
	cfg.ConnectionName = "shop"
	cfg.LedgerSchema = DefaultLedgerSchema
	store := &memStore{}
	c := &conn{
		cfg: cfg, types: newTypeCache(), ledgerQ: &fakeLedger{exists: true},
		openSession: func(context.Context) (session, error) { return srv, nil },
	}
	tk = &Toolkit{name: "shop", connections: map[string]*conn{"shop": c}}
	tk.SetMigrationStore(store)
	built = map[string]*fakeSession{}
	tk.scratchHook = fakeScratch(built)
	return tk, built
}

func TestCheckFromZeroBuildsTheTemplateOnce(t *testing.T) {
	srv := newServer(true)
	tk, built := testServerToolkit(srv, baseConfig())
	ms := []Migration{
		migration(1, "CREATE TABLE a (id int)", "", true),
		{
			Connection: "shop", Schema: "audit", Version: 1, Name: "m", Up: "CREATE TABLE log (id int)", Transactional: true,
			Checksum: ChecksumOf("CREATE TABLE log (id int)", "", true),
		},
	}
	slices.SortFunc(ms, func(a, b Migration) int { return strings.Compare(a.Schema, b.Schema) })
	c := tk.connections["shop"]
	_, err := tk.template(context.Background(), c, c, "", ms)
	require.NoError(t, err)
	want := templateName("shop", "", ms)
	assert.Equal(t, []string{want}, srv.has(templatePrefix))
	assert.Empty(t, srv.has(buildPrefix), "the build was renamed into place")
	require.Len(t, built, 1)
	for db, sess := range built {
		assert.True(t, strings.HasPrefix(db, buildPrefix))
		require.Len(t, sess.txs, 2, "each schema's migration applied in its own transaction")
		assert.Contains(t, sess.txs[0].scripts, "CREATE TABLE log (id int)")
		assert.Contains(t, sess.txs[1].scripts, "CREATE TABLE a (id int)")
	}

	// The head exists: nothing is built again.
	_, err = tk.template(context.Background(), c, c, "", ms)
	require.NoError(t, err)
	assert.Len(t, built, 1)

	// A schema's own check is a template of its own, beside the whole one.
	public := ms[1:]
	require.NoError(t, tk.CheckFromZero(context.Background(), "shop", DefaultSchema, public))
	assert.ElementsMatch(t, []string{want, templateName("shop", DefaultSchema, public)}, srv.has(templatePrefix))

	// A new head replaces its scope's older template and no other.
	public = append(public, migration(2, "ALTER TABLE a ADD COLUMN n int", "", true))
	require.NoError(t, tk.CheckFromZero(context.Background(), "shop", DefaultSchema, public))
	assert.ElementsMatch(t, []string{want, templateName("shop", DefaultSchema, public)}, srv.has(templatePrefix))
}

func TestCheckFromZeroRefusesAMigrationThatDoesNotApply(t *testing.T) {
	srv := newServer(true)
	tk, _ := testServerToolkit(srv, baseConfig())
	inner := tk.scratchHook
	tk.scratchHook = func(name string, cfg Config) (*Toolkit, error) {
		child, err := inner(name, cfg)
		sess := &fakeSession{ledger: fakeLedger{exists: true}, txScriptErr: map[int]error{1: &pgconn.PgError{Code: "42P01"}}}
		child.connections[name].openSession = func(context.Context) (session, error) { return sess, nil }
		return child, err
	}
	err := tk.CheckFromZero(context.Background(), "shop", DefaultSchema, []Migration{
		migration(1, "CREATE TABLE a (id int)", "", true),
		migration(2, "ALTER TABLE missing ADD COLUMN n int", "", true),
	})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeFromZeroFailed, refused.Code)
	assert.Contains(t, err.Error(), "do not apply from zero")
	assert.Contains(t, err.Error(), "migration 2")
	assert.Empty(t, srv.has(buildPrefix), "the failed build is dropped")
	assert.Empty(t, srv.has(templatePrefix))
}

func TestCheckFromZeroNamesTheMissingPrivilege(t *testing.T) {
	tk, _ := testServerToolkit(newServer(false), Config{User: "orders_ro"})
	err := tk.CheckFromZero(context.Background(), "shop", DefaultSchema, nil)
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeCreateDBRequired, refused.Code)
	assert.Contains(t, err.Error(), "CREATEDB")
	assert.Contains(t, err.Error(), `user "orders_ro"`)
}

func TestTestDatabaseClonesTheHeadAndDropsTheClone(t *testing.T) {
	srv := newServer(true)
	tk, built := testServerToolkit(srv, baseConfig())
	store, ok := tk.migrations.(*memStore)
	require.True(t, ok)
	store.migrations = []Migration{migration(1, "CREATE TABLE a (id int)", "", true)}

	store.migrations = append(store.migrations, Migration{
		Connection: "shop", Schema: "audit", Version: 1, Name: "m",
		Up: "CREATE TABLE log (id int)", Transactional: true, Checksum: ChecksumOf("CREATE TABLE log (id int)", "", true),
	})
	db, err := tk.TestDatabase(context.Background(), "shop", []string{DefaultSchema})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(db.Database, clonePrefix))
	assert.True(t, db.HasConnection("shop"), "the clone is served under the connection's name")
	assert.Contains(t, srv.has(clonePrefix), db.Database)
	assert.Contains(t, srv.scripts, "CREATE DATABASE "+db.Database+" TEMPLATE "+templateName("shop", DefaultSchema, store.migrations[:1]),
		"the template holds the schemas the test named and no other")
	assert.Contains(t, built, db.Database)
	assert.Equal(t, scratchConns, db.connections["shop"].cfg.MaxConns)

	db.Close()
	assert.Empty(t, srv.has(clonePrefix), "the clone is dropped when the test ends")
	assert.Contains(t, srv.scripts, "DROP DATABASE IF EXISTS "+db.Database+" WITH (FORCE)")
	(*TestDB)(nil).Close()
}

func TestTestDatabaseWaitsOutATemplateInUse(t *testing.T) {
	srv := newServer(true)
	tk, _ := testServerToolkit(srv, baseConfig())
	busy := 2
	srv.fail = func(sql string) error {
		if strings.Contains(sql, " TEMPLATE ") && busy > 0 {
			busy--
			return &pgconn.PgError{Code: objectInUse}
		}
		return nil
	}
	db, err := tk.TestDatabase(context.Background(), "shop", nil)
	require.NoError(t, err)
	db.Close()
	assert.Zero(t, busy)
}

func TestTestDatabaseRefusals(t *testing.T) {
	srv := newServer(true)
	tk, _ := testServerToolkit(srv, baseConfig())
	_, err := tk.TestDatabase(context.Background(), "nope", nil)
	assert.ErrorContains(t, err, `no application database named "nope"`)

	tk.connections["shop"].cfg.TestConnection = "elsewhere"
	_, err = tk.TestDatabase(context.Background(), "shop", nil)
	assert.ErrorContains(t, err, `names test_connection "elsewhere"`)

	tk.migrations = nil
	_, err = tk.TestDatabase(context.Background(), "shop", nil)
	assert.ErrorIs(t, err, ErrNoMigrationStore)

	srv.fail = func(sql string) error {
		if strings.Contains(sql, " TEMPLATE ") {
			return &pgconn.PgError{Code: insufficientPrivilege}
		}
		return nil
	}
	tk, _ = testServerToolkit(srv, baseConfig())
	_, err = tk.TestDatabase(context.Background(), "shop", nil)
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, CodeCreateDBRequired, refused.Code)
}

func TestTestConnectionNamesAnotherServer(t *testing.T) {
	own, other := newServer(false), newServer(true)
	tk, _ := testServerToolkit(own, Config{TestConnection: "scratch"})
	tk.connections["scratch"] = &conn{
		cfg: Config{ConnectionName: "scratch"}, types: newTypeCache(),
		openSession: func(context.Context) (session, error) { return other, nil },
	}
	require.NoError(t, tk.CheckFromZero(context.Background(), "shop", DefaultSchema, nil))
	assert.Len(t, other.has(templatePrefix), 1, "the template is built on the test_connection's server")
	assert.Empty(t, own.has(templatePrefix))
	assert.Len(t, tk.testServers(), 1)
}

func TestSweepDropsOldScratchDatabasesOnly(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour).Unix()
	recent := time.Now().Unix()
	srv := newServer(true,
		"mcpc_"+itoa(old)+"_0123456789abcdef",
		"mcpb_"+itoa(old)+"_fedcba9876543210",
		"mcpc_"+itoa(recent)+"_0123456789abcdef",
		"mcpt_0a1b2c3d_0123456789abcdef",
		"orders",
	)
	tk, _ := testServerToolkit(srv, baseConfig())
	n, err := tk.SweepTestDatabases(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []string{"mcpc_" + itoa(recent) + "_0123456789abcdef"}, srv.has(clonePrefix))
	assert.Empty(t, srv.has(buildPrefix))
	assert.Len(t, srv.has(templatePrefix), 1)
	assert.Equal(t, []string{"orders"}, srv.has("orders"))

	srv.fail = func(string) error { return errors.New("drop refused") }
	srv.databases["mcpc_"+itoa(old)+"_0123456789abcdef"] = true
	_, err = tk.SweepTestDatabases(context.Background(), time.Hour)
	assert.ErrorContains(t, err, "connection shop")
}

func TestCreatedBefore(t *testing.T) {
	cutoff := time.Unix(1000, 0)
	assert.True(t, createdBefore("mcpc_999_0123456789abcdef", cutoff))
	assert.False(t, createdBefore("mcpc_1001_0123456789abcdef", cutoff))
	assert.False(t, createdBefore("orders", cutoff))
	assert.False(t, createdBefore("mcpt_999_0123456789abcdef", cutoff))
}

func TestDropDatabaseLeavesOtherNamesAlone(t *testing.T) {
	srv := newServer(true, "orders")
	c := &conn{cfg: Config{ConnectionName: "shop"}, openSession: func(context.Context) (session, error) { return srv, nil }}
	c.dropDatabase("orders")
	assert.Empty(t, srv.scripts)
	srv.fail = func(string) error { return errors.New("down") }
	c.dropDatabase("mcpc_1_0123456789abcdef")
	assert.Len(t, srv.scripts, 1)
}

func TestHeadAndNames(t *testing.T) {
	a := []Migration{migration(1, "x", "", true)}
	b := []Migration{migration(1, "y", "", true)}
	assert.NotEqual(t, HeadOf(a), HeadOf(b))
	assert.Len(t, HeadOf(a), 16)
	assert.True(t, strings.HasPrefix(templateName("shop", "", a), templateFamily("shop", "")))
	assert.NotEqual(t, templateFamily("shop", ""), templateFamily("orders", ""))
	assert.NotEqual(t, templateFamily("shop", ""), templateFamily("shop", "public"))
	assert.LessOrEqual(t, len(templateName("shop", "", a)), 63)
	assert.Equal(t, "a,b", scopeOf([]string{"b", "a", "b"}))
	assert.Empty(t, scopeOf(nil))
	name, err := scratchDBName(clonePrefix, time.Unix(42, 0))
	require.NoError(t, err)
	assert.Regexp(t, `^mcpc_42_[0-9a-f]{16}$`, name)
	got := onServer(Config{Database: "orders", User: "app", MaxRows: 7, TestConnection: "x"},
		Config{Host: "db2", Port: 6543, User: "builder", Password: "p", SSLMode: SSLModeRequire}, "mcpc_1_0123456789abcdef")
	assert.Equal(t, Config{
		Database: "mcpc_1_0123456789abcdef", Host: "db2", Port: 6543, User: "builder", Password: "p",
		SSLMode: SSLModeRequire, MaxRows: 7, MaxConns: scratchConns,
	}, got)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestScratchServesOneConnectionWithTheGuard(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	guard := ConnectGuard(func(context.Context, *pgx.Conn) error { return nil })
	tk.SetConnectGuard(guard)
	child, err := tk.newScratch("shop", onServer(baseConfig(), baseConfig(), "mcpc_1_0123456789abcdef"))
	require.NoError(t, err)
	defer func() { _ = child.Close() }()
	assert.True(t, child.HasConnection("shop"))
	assert.Equal(t, "mcpc_1_0123456789abcdef", child.connections["shop"].cfg.Database)
	assert.NotNil(t, child.guard.Load(), "a throwaway database is checked by the platform's guard like any connection")

	bad := baseConfig()
	bad.CABundlePEM = "not a certificate"
	bad.SSLMode = SSLModeVerifyFull
	_, err = tk.scratch("shop", bad)
	assert.Error(t, err)
}
