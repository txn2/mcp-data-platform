//go:build integration

package testdb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// EnvAppDBTargets names the servers an application database is tested
// against: space-separated maintenance DSNs, one per supported major. make
// test-realdb starts a PostgreSQL 17 and an 18 and sets it (#2447).
const EnvAppDBTargets = "APPDB_TARGET_DSNS"

var appdbSeq atomic.Int64

// appdbTargets reads EnvAppDBTargets once, keyed by each server's major.
var appdbTargets = sync.OnceValues(func() (map[string]string, error) {
	out := map[string]string{}
	for dsn := range strings.FieldsSeq(os.Getenv(EnvAppDBTargets)) {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return nil, err
		}
		var num int
		err = db.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&num)
		_ = db.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the version of a server in %s: %w", EnvAppDBTargets, err)
		}
		out[fmt.Sprintf("pg%d", num/10000)] = dsn
	}
	return out, nil
})

// OnAppDBTargets runs fn once per server EnvAppDBTargets names, as a subtest
// named by its major ("pg17"), which AppDBTarget reads. A run naming no
// server fails: PostgreSQL 17 is the oldest the application database kind
// accepts, and these tests hold it to that.
func OnAppDBTargets(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	targets, err := appdbTargets()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) == 0 {
		t.Fatalf("%s names no server; make test-realdb starts PostgreSQL 17 and 18 and sets it", EnvAppDBTargets)
	}
	majors := make([]string, 0, len(targets))
	for m := range targets {
		majors = append(majors, m)
	}
	sort.Strings(majors)
	for _, m := range majors {
		t.Run(m, fn)
	}
}

// AppDB is one test's databases: the platform's own, migrated, and a fresh
// database on the target server the subtest names.
type AppDB struct {
	// Platform is the platform's own database, which the own-database guard
	// reads.
	Platform *sql.DB
	// Server is the target server's maintenance database.
	Server *sql.DB
	// Target is the fresh database's connection configuration, as an appdb
	// connection is stored.
	Target map[string]any
	// Database is the fresh database's name.
	Database string
	// Major is the target server's major, "pg17" or "pg18".
	Major string
}

// AppDBTarget returns the platform's database and a fresh database on the
// server OnAppDBTargets named this subtest for, dropped when the test ends.
func AppDBTarget(t *testing.T) AppDB {
	t.Helper()
	platform := New(t)
	parts := strings.SplitN(t.Name(), "/", 3)
	if len(parts) < 2 {
		t.Fatal("AppDBTarget runs under OnAppDBTargets")
	}
	out := AppDB{Platform: platform, Major: parts[1]}
	targets, err := appdbTargets()
	if err != nil {
		t.Fatal(err)
	}
	dsn, ok := targets[out.Major]
	if !ok {
		t.Fatalf("AppDBTarget runs under OnAppDBTargets, which names the subtest for a server in %s; %q is not one", EnvAppDBTargets, out.Major)
	}
	if out.Server, err = sql.Open("postgres", dsn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Server.Close() })
	out.Database = fmt.Sprintf("appdb_target_%d_%d", os.Getpid(), appdbSeq.Add(1))
	if _, err := out.Server.Exec(`CREATE DATABASE ` + quoteIdent(out.Database)); err != nil {
		t.Fatalf("create target database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = out.Server.Exec(`DROP DATABASE IF EXISTS ` + quoteIdent(out.Database) + ` WITH (FORCE)`)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	out.Target = map[string]any{
		"host": u.Hostname(), "port": port, "database": out.Database,
		"user": u.User.Username(), "password": pw, "sslmode": "disable",
	}
	return out
}

// TargetDB opens the fresh target database directly, as its server's user.
func (a AppDB) TargetDB(t *testing.T) *sql.DB {
	t.Helper()
	pw, _ := a.Target["password"].(string)
	u := url.URL{
		Scheme: "postgres", User: url.UserPassword(fmt.Sprint(a.Target["user"]), pw),
		Host: fmt.Sprintf("%v:%v", a.Target["host"], a.Target["port"]), Path: "/" + a.Database, RawQuery: "sslmode=disable",
	}
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
