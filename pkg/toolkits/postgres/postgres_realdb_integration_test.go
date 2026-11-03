//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/lib/pq" // the target servers' maintenance databases

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

var targetSeq atomic.Int64

// envTargets names the servers an application database is tested against:
// space-separated maintenance DSNs, one per supported major. make test-realdb
// starts a PostgreSQL 17 and an 18 and sets it (#2447); the platform's own
// database stays on the server testdb provides.
const envTargets = "APPDB_TARGET_DSNS"

// appdbTargets reads envTargets once, keyed by each server's major ("pg17").
var appdbTargets = sync.OnceValues(func() (map[string]string, error) {
	out := map[string]string{}
	for dsn := range strings.FieldsSeq(os.Getenv(envTargets)) {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return nil, err
		}
		var num int
		err = db.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&num)
		_ = db.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the version of a server in %s: %w", envTargets, err)
		}
		out[fmt.Sprintf("pg%d", num/10000)] = dsn
	}
	return out, nil
})

// onTargets runs fn once per server envTargets names, as a subtest named by
// the server's major, which realTarget reads. A run naming no server fails:
// the floor is what these tests hold the kind to.
func onTargets(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	targets, err := appdbTargets()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) == 0 {
		t.Fatalf("%s names no server; make test-realdb starts PostgreSQL 17 and 18 and sets it", envTargets)
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

// realDB is one test's databases: the platform's own, which the own-database
// guard reads, and a fresh target on the server the subtest names.
type realDB struct {
	platform *sql.DB
	own      map[string]any
	// server is the target server's maintenance database: roles, sessions
	// and databases are the server's.
	server *sql.DB
	target map[string]any
	major  string
}

// subtestMajor is the subtest segment onTargets named: the one after the test's
// own name, beneath which a test may run subtests of its own.
func subtestMajor(name string) string {
	parts := strings.SplitN(name, "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// realTarget returns the platform's migrated database and a fresh database on
// the target server onTargets named this subtest for.
func realTarget(t *testing.T) realDB {
	t.Helper()
	platform, dsn := testdb.NewWithDSN(t)
	rt := realDB{platform: platform, own: cfgFromDSN(t, dsn), major: subtestMajor(t.Name())}
	targets, err := appdbTargets()
	if err != nil {
		t.Fatal(err)
	}
	serverDSN, ok := targets[rt.major]
	if !ok {
		t.Fatalf("realTarget runs under onTargets, which names the subtest for a server in %s; %q is not one", envTargets, rt.major)
	}
	if rt.server, err = sql.Open("postgres", serverDSN); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.server.Close() })
	name := fmt.Sprintf("pgkind_target_%d_%d", os.Getpid(), targetSeq.Add(1))
	if _, err := rt.server.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatalf("create target database: %v", err)
	}
	t.Cleanup(func() { _, _ = rt.server.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`) })
	rt.target = cfgFromDSN(t, serverDSN)
	rt.target[cfgKeyDatabase] = name
	return rt
}

func cfgFromDSN(t *testing.T, dsn string) map[string]any {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	return map[string]any{
		cfgKeyHost: u.Hostname(), cfgKeyPort: port, cfgKeyDatabase: strings.TrimPrefix(u.Path, "/"),
		cfgKeyUser: u.User.Username(), cfgKeyPassword: pw, cfgKeySSLMode: SSLModeDisable,
	}
}

func newRealToolkit(t *testing.T, platform *sql.DB, conns map[string]map[string]any) *Toolkit {
	t.Helper()
	multi, err := ParseMultiConfig("target", conns)
	if err != nil {
		t.Fatal(err)
	}
	if len(multi.Instances) != len(conns) {
		t.Fatalf("parsed %d of %d connections", len(multi.Instances), len(conns))
	}
	tk := NewMulti(multi)
	tk.SetConnectGuard(OwnDatabaseGuard(platform))
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

func execOK(t *testing.T, tk *Toolkit, st Statement) *Result {
	t.Helper()
	res, err := tk.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	return res
}

func queryOK(t *testing.T, tk *Toolkit, st Statement) *Result {
	t.Helper()
	res, err := tk.Query(context.Background(), st)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	return res
}

// values decodes a JSON document the way a script's values arrive: numbers
// keep every digit.
func values(t *testing.T, doc string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

const typesDDL = `CREATE TABLE typed (
  id int4 PRIMARY KEY, big int8, amount numeric(20,6), ratio float8,
  at timestamptz, day date, local timestamp, doc jsonb, raw json,
  uid uuid, tags text[], nums int4[], blob bytea, flag bool, note text)`

func TestTypeMapping_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		platform, target := rt.platform, rt.target
		tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})

		execOK(t, tk, Statement{Connection: "target", SQL: typesDDL})
		execOK(t, tk, Statement{
			Connection: "target",
			SQL:        `INSERT INTO typed VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			Params: values(t, `[1, 9007199254740993, "12345678901234.123456", 0.5,
			"2026-11-01T12:30:00+02:00", "2026-11-01", "2026-11-01T08:00:00", {"a":[1,2]}, [true],
			"6f1c2c1e-7d2b-4b9e-9a51-0c4f0d1e2a3b", ["x","y \"q\""], [1,null,3], "AAEC", true, null]`),
		})

		byList := queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT * FROM typed WHERE id = $1`, Params: []any{int64(1)}})
		byName := queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT * FROM typed WHERE id = @id`, Params: map[string]any{"id": int64(1)}})
		a, _ := json.Marshal(byList.Rows)
		b, _ := json.Marshal(byName.Rows)
		if string(a) != string(b) {
			t.Fatalf("list and object params differ:\n%s\n%s", a, b)
		}
		if len(byList.Rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(byList.Rows))
		}
		row := byList.Rows[0]
		want := map[string]string{
			"id": `1`, "big": `"9007199254740993"`, "amount": `"12345678901234.123456"`, "ratio": `0.5`,
			"at": `"2026-11-01T10:30:00Z"`, "day": `"2026-11-01"`, "local": `"2026-11-01T08:00:00"`,
			"doc": `{"a":[1,2]}`, "raw": `[true]`, "uid": `"6f1c2c1e-7d2b-4b9e-9a51-0c4f0d1e2a3b"`,
			"tags": `["x","y \"q\""]`, "nums": `[1,null,3]`, "blob": `"AAEC"`, "flag": `true`, "note": `null`,
		}
		for i, col := range byList.Columns {
			got, _ := json.Marshal(row[i])
			if string(got) != want[col.Name] {
				t.Errorf("%s (%s) = %s, want %s", col.Name, col.Type, got, want[col.Name])
			}
		}
		if byList.Columns[2].Type != "numeric" || byList.Columns[4].Type != "timestamp with time zone" {
			t.Errorf("column types = %+v", byList.Columns)
		}
	})
}

func TestDetailReachesOnlyTheCaller_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		platform, target := rt.platform, rt.target
		tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
		execOK(t, tk, Statement{Connection: "target", SQL: `CREATE TABLE people (email text PRIMARY KEY)`})
		const secret = "s3cret-value@example.com"
		execOK(t, tk, Statement{Connection: "target", SQL: `INSERT INTO people VALUES ($1)`, Params: []any{secret}})

		_, err := tk.Execute(context.Background(), Statement{Connection: "target", SQL: `INSERT INTO people VALUES ($1)`, Params: []any{secret}})
		failed, ok := errors.AsType[*FailedError](err)
		if !ok {
			t.Fatalf("a duplicate insert: err = %v, want a FailedError", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the error text repeats the value: %q", err)
		}
		if !strings.Contains(err.Error(), "23505") || !strings.Contains(err.Error(), "people_pkey") {
			t.Errorf("summary lacks the SQLSTATE or constraint: %q", err)
		}
		if !strings.Contains(failed.CallerText(), secret) {
			t.Errorf("the script did not get the DETAIL: %s", failed.CallerText())
		}
	})
}

func TestOwnDatabaseIsRefused_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		own, target := rt.own, rt.target
		tk := newRealToolkit(t, rt.platform, map[string]map[string]any{"target": target})

		if err := tk.ValidateConnection("own", own); err == nil || !strings.Contains(err.Error(), "platform's own database") {
			t.Fatalf("saving the platform's database: err = %v, want a refusal", err)
		}
		if err := tk.ValidateConnection("other", target); err != nil {
			t.Fatalf("saving another database: %v", err)
		}
		// A connection that reached the toolkit some other way is refused at its
		// first statement.
		if err := tk.AddConnection("own", own); err != nil {
			t.Fatal(err)
		}
		_, err := tk.Query(context.Background(), Statement{Connection: "own", SQL: "SELECT 1"})
		if err == nil || !strings.Contains(err.Error(), "platform's own database") {
			t.Fatalf("a statement on the platform's database: err = %v", err)
		}
	})
}

func TestSessionStateDoesNotOutliveAStatement_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		platform, target := rt.platform, rt.target
		target[cfgKeyMaxConns] = 1
		tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
		execOK(t, tk, Statement{Connection: "target", SQL: `SET search_path TO pg_catalog`})
		got := queryOK(t, tk, Statement{Connection: "target", SQL: `SHOW search_path`})
		if v, _ := got.Rows[0][0].(string); v == "pg_catalog" {
			t.Fatal("a session-level SET outlived the statement that made it")
		}
	})
}

func TestQueryIsReadOnlyAndCapped_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		rt := realTarget(t)
		platform, target := rt.platform, rt.target
		tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
		execOK(t, tk, Statement{Connection: "target", SQL: `CREATE TABLE n (i int)`})

		_, err := tk.Query(context.Background(), Statement{Connection: "target", SQL: `INSERT INTO n VALUES (1)`})
		if err == nil || !strings.Contains(err.Error(), "25006") {
			t.Fatalf("a write through Query: err = %v", err)
		}
		got := queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT g FROM generate_series(1, 1000000) g`, Limit: 5})
		if len(got.Rows) != 5 || !got.Truncated {
			t.Fatalf("rows = %d truncated = %v, want 5 and true", len(got.Rows), got.Truncated)
		}
		got = queryOK(t, tk, Statement{Connection: "target", SQL: `SELECT count(*) FROM n`})
		if n, _ := json.Marshal(got.Rows[0][0]); string(n) != "0" {
			t.Fatalf("rows = %s, want 0", n)
		}
	})
}
