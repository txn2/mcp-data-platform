//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// realRoles creates two login roles on the test server for one test and drops
// them after it: roles are the server's, not a database's.
func realRoles(t *testing.T, server *sql.DB) (granted, refused string) {
	t.Helper()
	n := targetSeq.Add(1)
	granted = fmt.Sprintf("pgkind_granted_%d_%d", os.Getpid(), n)
	refused = fmt.Sprintf("pgkind_refused_%d_%d", os.Getpid(), n)
	for _, r := range []string{granted, refused} {
		if _, err := server.Exec(`CREATE ROLE "` + r + `" LOGIN PASSWORD 'pw-` + r + `'`); err != nil {
			t.Fatalf("create role: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, r := range []string{granted, refused} {
			_, _ = server.Exec(`DROP OWNED BY "` + r + `"`)
			_, _ = server.Exec(`DROP ROLE IF EXISTS "` + r + `"`)
		}
	})
	return granted, refused
}

// roleToolkit is a toolkit on a fresh target whose personas "granted" and
// "refused" log in as two roles of their own, with one pooled connection per
// role so consecutive calls share a physical connection.
func roleToolkit(t *testing.T) (*Toolkit, *sql.DB, string, string) {
	t.Helper()
	rt := realTarget(t)
	platform, target := rt.platform, rt.target
	granted, refused := realRoles(t, rt.server)
	target[cfgKeyMaxConns] = 1
	target[CfgKeyPersonaRoles] = map[string]any{"granted": granted, "refused": refused}
	target[CfgKeyPersonaRolePasswords] = map[string]any{"granted": "pw-" + granted, "refused": "pw-" + refused}
	tk := newRealToolkit(t, platform, map[string]map[string]any{"target": target})
	execOK(t, tk, Statement{Connection: "target", SQL: "CREATE TABLE ledger (id int PRIMARY KEY, owner_id text NOT NULL, note text)"})
	execOK(t, tk, Statement{Connection: "target", SQL: `GRANT SELECT, INSERT ON ledger TO "` + granted + `"`})
	execOK(t, tk, Statement{Connection: "target", SQL: "INSERT INTO ledger VALUES (1, 'sub-a', 'a'), (2, 'sub-b', 'b')"})
	return tk, rt.server, granted, refused
}

func as(persona, user string) context.Context {
	return WithIdentity(context.Background(), Identity{Persona: persona, UserID: user, Source: SourceEndpoint})
}

func sqlStateOf(t *testing.T, err error) string {
	t.Helper()
	f, ok := errors.AsType[*FailedError](err)
	if !ok || f.Statement == nil {
		t.Fatalf("want a statement the database refused, got %v", err)
	}
	return f.Statement.SQLState
}

// A persona's statements run as its own login role: a table granted to one
// role is read by its persona and refused (42501) to the other, and a role
// that logged in has nothing to RESET ROLE back to.
func TestPersonaRoles_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _, granted, refused := roleToolkit(t)
		read := Statement{Connection: "target", SQL: "SELECT count(*) AS n FROM ledger"}

		res, err := tk.Query(as("granted", ""), read)
		if err != nil {
			t.Fatalf("the granted persona's read: %v", err)
		}
		if fmt.Sprint(res.Rows[0][0]) != "2" {
			t.Fatalf("rows = %v", res.Rows)
		}
		_, err = tk.Query(as("refused", ""), read)
		if got := sqlStateOf(t, err); got != "42501" {
			t.Fatalf("the refused persona's read: sqlstate %s", got)
		}

		who, err := tk.Query(as("refused", ""), Statement{Connection: "target", SQL: "SELECT current_user, session_user"})
		if err != nil {
			t.Fatal(err)
		}
		if who.Rows[0][0] != refused || who.Rows[0][1] != refused {
			t.Fatalf("the refused persona ran as %v, want the role %s logged in as itself", who.Rows[0], refused)
		}

		tx, err := tk.Begin(as("refused", ""), TxOptions{Connection: "target"})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Execute(context.Background(), Statement{SQL: "RESET ROLE"}); err != nil {
			t.Fatalf("RESET ROLE: %v", err)
		}
		_, err = tx.Query(context.Background(), read)
		if got := sqlStateOf(t, err); got != "42501" {
			t.Fatalf("after RESET ROLE the refused persona read with sqlstate %s, want 42501", got)
		}

		dflt, err := tk.Query(context.Background(), Statement{Connection: "target", SQL: "SELECT current_user"})
		if err != nil {
			t.Fatal(err)
		}
		if dflt.Rows[0][0] == granted || dflt.Rows[0][0] == refused {
			t.Fatalf("a statement for no persona ran as a persona role: %v", dflt.Rows[0][0])
		}
	})
}

// Every transaction sets the caller's claims, and an RLS policy on
// mcp.user_id shows each caller their own rows. A claim never outlives its
// transaction, even one a statement set for its session.
func TestCallerClaims_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _, granted, _ := roleToolkit(t)
		execOK(t, tk, Statement{Connection: "target", SQL: "ALTER TABLE ledger ENABLE ROW LEVEL SECURITY"})
		execOK(t, tk, Statement{Connection: "target", SQL: `CREATE POLICY own ON ledger TO "` + granted +
			`" USING (owner_id = nullif(current_setting('mcp.user_id', true), ''))`})
		read := Statement{Connection: "target", SQL: "SELECT id FROM ledger ORDER BY id"}

		for user, want := range map[string]string{"sub-a": "[[1]]", "sub-b": "[[2]]", "": "[]"} {
			res, err := tk.Query(as("granted", user), read)
			if err != nil {
				t.Fatalf("%q: %v", user, err)
			}
			if got := fmt.Sprint(res.Rows); got != want {
				t.Fatalf("caller %q saw %s, want %s", user, got, want)
			}
		}

		claims := Statement{Connection: "target", SQL: "SELECT current_setting('mcp.user_id', true), current_setting('mcp.email', true), " +
			"current_setting('mcp.persona', true), current_setting('mcp.source', true)"}
		id := Identity{Persona: "granted", UserID: "sub-a", Email: "a@example.com", Source: SourceScript}
		res, err := tk.Query(WithIdentity(context.Background(), id), claims)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(res.Rows[0]); got != "[sub-a a@example.com granted script]" {
			t.Fatalf("claims = %s", got)
		}

		if _, err := tk.Execute(as("granted", "sub-a"), Statement{
			Connection: "target",
			SQL:        "SELECT set_config('mcp.user_id', 'sub-b', false)",
		}); err != nil {
			t.Fatal(err)
		}
		res, err = tk.Query(as("granted", ""), claims)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(res.Rows[0]); got != "[  granted endpoint]" {
			t.Fatalf("the next caller on the same connection read %s, want its own empty claims", got)
		}
	})
}

// The probe logs in as every persona role and names each, and a role that
// fails to log in fails the probe without repeating its password.
func TestProbeRoles_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, server, granted, refused := roleToolkit(t)
		r := tk.ProbeConnection(context.Background(), "target")
		if !r.OK {
			t.Fatalf("probe failed: %+v", r)
		}
		for _, want := range []string{"persona granted runs as " + granted, "persona refused runs as " + refused} {
			if !strings.Contains(r.Detail, want) {
				t.Fatalf("probe detail %q lacks %q", r.Detail, want)
			}
		}

		if _, err := server.Exec(`ALTER ROLE "` + refused + `" PASSWORD 'rotated'`); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, refused); err != nil {
			t.Fatal(err)
		}
		tk2 := freshRoleToolkit(t, tk)
		r = tk2.ProbeConnection(context.Background(), "target")
		if r.OK {
			t.Fatalf("probe with a stale role password passed: %+v", r)
		}
		if text := fmt.Sprintf("%+v", r); strings.Contains(text, "pw-"+refused) || strings.Contains(text, "pw-"+granted) {
			t.Fatalf("probe result repeats a role password: %s", text)
		}
	})
}

// freshRoleToolkit is tk's target served by a new toolkit, whose role pools
// log in again.
func freshRoleToolkit(t *testing.T, tk *Toolkit) *Toolkit {
	t.Helper()
	c, ok := tk.lookup("target")
	if !ok {
		t.Fatal("no target")
	}
	child, err := tk.scratch("target", c.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Close() })
	return child
}
