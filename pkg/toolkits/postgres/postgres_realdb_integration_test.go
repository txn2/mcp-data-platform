//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

var targetSeq atomic.Int64

// realTarget returns the platform's migrated database and a second database on
// the same server, as a postgres connection's config.
func realTarget(t *testing.T) (platform *sql.DB, platformCfg, targetCfg map[string]any) {
	t.Helper()
	platform, dsn := testdb.NewWithDSN(t)
	platformCfg = cfgFromDSN(t, dsn)
	name := fmt.Sprintf("pgkind_target_%d_%d", os.Getpid(), targetSeq.Add(1))
	if _, err := platform.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatalf("create target database: %v", err)
	}
	t.Cleanup(func() { _, _ = platform.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`) })
	targetCfg = make(map[string]any, len(platformCfg))
	for k, v := range platformCfg {
		targetCfg[k] = v
	}
	targetCfg[cfgKeyDatabase] = name
	return platform, platformCfg, targetCfg
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

func execOK(t *testing.T, tk *Toolkit, in ExecuteInput) *ExecuteOutput {
	t.Helper()
	res, out, _ := tk.handleExecute(context.Background(), nil, in)
	if res.IsError {
		t.Fatalf("execute failed: %s", allText(res))
	}
	return out.(*ExecuteOutput)
}

func queryOK(t *testing.T, tk *Toolkit, in QueryInput) *QueryOutput {
	t.Helper()
	res, out, _ := tk.handleQuery(context.Background(), nil, in)
	if res.IsError {
		t.Fatalf("query failed: %s", allText(res))
	}
	return out.(*QueryOutput)
}

func allText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

const typesDDL = `CREATE TABLE typed (
  id int4 PRIMARY KEY, big int8, amount numeric(20,6), ratio float8,
  at timestamptz, day date, local timestamp, doc jsonb, raw json,
  uid uuid, tags text[], nums int4[], blob bytea, flag bool, note text)`

func TestTypeMapping_RealDB(t *testing.T) {
	platform, _, target := realTarget(t)
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})

	execOK(t, tk, ExecuteInput{Connection: "target", SQL: typesDDL})
	execOK(t, tk, ExecuteInput{
		Connection: "target",
		SQL:        `INSERT INTO typed VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		Params: json.RawMessage(`[1, 9007199254740993, "12345678901234.123456", 0.5,
			"2026-11-01T12:30:00+02:00", "2026-11-01", "2026-11-01T08:00:00", {"a":[1,2]}, [true],
			"6f1c2c1e-7d2b-4b9e-9a51-0c4f0d1e2a3b", ["x","y \"q\""], [1,null,3], "AAEC", true, null]`),
	})

	byList := queryOK(t, tk, QueryInput{Connection: "target", SQL: `SELECT * FROM typed WHERE id = $1`, Params: json.RawMessage(`[1]`)})
	byName := queryOK(t, tk, QueryInput{Connection: "target", SQL: `SELECT * FROM typed WHERE id = @id`, Params: json.RawMessage(`{"id": 1}`)})
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
}

func TestStatementsAreOneTransaction_RealDB(t *testing.T) {
	platform, _, target := realTarget(t)
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
	execOK(t, tk, ExecuteInput{Connection: "target", SQL: `CREATE TABLE people (email text PRIMARY KEY)`})

	res, _, _ := tk.handleExecute(context.Background(), nil, ExecuteInput{
		Connection: "target",
		Statements: []StatementInput{
			{SQL: `INSERT INTO people VALUES ($1)`, Params: json.RawMessage(`["a@example.com"]`)},
			{SQL: `INSERT INTO people VALUES (@e)`, Params: json.RawMessage(`{"e":"b@example.com"}`)},
			{SQL: `INSERT INTO people VALUES ($1)`, Params: json.RawMessage(`["a@example.com"]`)},
		},
	})
	if !res.IsError {
		t.Fatal("a statements list whose third statement fails succeeded")
	}
	if !strings.Contains(allText(res), "in statement 2") {
		t.Errorf("failure does not name the statement: %s", allText(res))
	}
	got := queryOK(t, tk, QueryInput{Connection: "target", SQL: `SELECT count(*) FROM people`})
	if n, _ := json.Marshal(got.Rows[0][0]); string(n) != "0" {
		t.Fatalf("rows left after a failed list = %s, want 0", n)
	}
}

func TestDetailReachesOnlyTheCaller_RealDB(t *testing.T) {
	platform, _, target := realTarget(t)
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
	execOK(t, tk, ExecuteInput{Connection: "target", SQL: `CREATE TABLE people (email text PRIMARY KEY)`})
	const secret = "s3cret-value@example.com"
	execOK(t, tk, ExecuteInput{Connection: "target", SQL: `INSERT INTO people VALUES ($1)`, Params: json.RawMessage(`["` + secret + `"]`)})

	res, _, _ := tk.handleExecute(context.Background(), nil, ExecuteInput{
		Connection: "target", SQL: `INSERT INTO people VALUES ($1)`, Params: json.RawMessage(`["` + secret + `"]`),
	})
	if !res.IsError {
		t.Fatal("a duplicate insert succeeded")
	}
	first := res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(first, secret) || strings.Contains(res.GetError().Error(), secret) {
		t.Fatalf("the audit-read text repeats the value: %q / %q", first, res.GetError())
	}
	if !strings.Contains(first, "23505") || !strings.Contains(first, "people_pkey") {
		t.Errorf("summary lacks the SQLSTATE or constraint: %q", first)
	}
	if !strings.Contains(allText(res), secret) {
		t.Errorf("the caller did not get the DETAIL: %s", allText(res))
	}
}

func TestOwnDatabaseIsRefused_RealDB(t *testing.T) {
	platform, own, target := realTarget(t)
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})

	if err := tk.ValidateConnection("own", own); err == nil || !strings.Contains(err.Error(), "platform's own database") {
		t.Fatalf("saving the platform's database: err = %v, want a refusal", err)
	}
	if err := tk.ValidateConnection("other", target); err != nil {
		t.Fatalf("saving another database on the same server: %v", err)
	}
	// A connection that reached the toolkit some other way is refused at its
	// first statement.
	if err := tk.AddConnection("own", own); err != nil {
		t.Fatal(err)
	}
	res, _, _ := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "own", SQL: "SELECT 1"})
	if !res.IsError || !strings.Contains(allText(res), "platform's own database") {
		t.Fatalf("a statement on the platform's database: %s", allText(res))
	}
}

func TestSessionStateDoesNotOutliveACall_RealDB(t *testing.T) {
	platform, _, target := realTarget(t)
	target[cfgKeyMaxConns] = 1
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
	execOK(t, tk, ExecuteInput{Connection: "target", SQL: `SET search_path TO pg_catalog`})
	got := queryOK(t, tk, QueryInput{Connection: "target", SQL: `SHOW search_path`})
	if v, _ := got.Rows[0][0].(string); v == "pg_catalog" {
		t.Fatal("a session-level SET outlived the call that made it")
	}
}

func TestQueryIsReadOnlyAndCapped_RealDB(t *testing.T) {
	platform, _, target := realTarget(t)
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
	execOK(t, tk, ExecuteInput{Connection: "target", SQL: `CREATE TABLE n (i int)`})

	res, _, _ := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "target", SQL: `INSERT INTO n VALUES (1)`})
	if !res.IsError || !strings.Contains(allText(res), "25006") {
		t.Fatalf("a write through postgres_query: %s", allText(res))
	}
	got := queryOK(t, tk, QueryInput{Connection: "target", SQL: `SELECT g FROM generate_series(1, 1000000) g`, Limit: 5})
	if len(got.Rows) != 5 || !got.Truncated {
		t.Fatalf("rows = %d truncated = %v, want 5 and true", len(got.Rows), got.Truncated)
	}
	res, _, _ = tk.handleExecute(context.Background(), nil, ExecuteInput{Connection: "target", Statements: []StatementInput{
		{SQL: `INSERT INTO n VALUES (1)`}, {SQL: `COMMIT`}, {SQL: `INSERT INTO n VALUES ('x')`},
	}})
	if !res.IsError || !strings.Contains(allText(res), "COMMIT statement is not run") {
		t.Fatalf("a COMMIT inside a statements list: %s", allText(res))
	}
	got = queryOK(t, tk, QueryInput{Connection: "target", SQL: `SELECT count(*) FROM n`})
	if n, _ := json.Marshal(got.Rows[0][0]); string(n) != "0" {
		t.Fatalf("rows = %s, want 0", n)
	}
}
