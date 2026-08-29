package trino

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exec is the platform's one write path into Trino. What is asserted here is
// the guard, not the transport: the same ReadOnlyInterceptor the MCP tools run
// decides, against the same per-connection settings, so a read_only connection
// refuses a statement here exactly as trino_execute would.

func TestScratchTarget(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]Config{
			"warehouse": {Host: "trino.example.com", User: "u", ReadOnly: true},
			"scratch": {
				Host:    "trino.example.com",
				User:    "u",
				Scratch: ScratchConfig{Catalog: "scratch", Schema: "uploads"},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	target, ok := tk.ScratchTarget("scratch")
	require.True(t, ok)
	assert.Equal(t, "scratch", target.Catalog)
	assert.Equal(t, "uploads", target.Schema)

	_, ok = tk.ScratchTarget("warehouse")
	assert.False(t, ok, "a connection with no scratch block cannot hold a table")

	_, ok = tk.ScratchTarget("nonexistent")
	assert.False(t, ok)

	// An empty name resolves to the default connection, matching
	// multiserver.Manager.Client("").
	_, ok = tk.ScratchTarget("")
	assert.False(t, ok, "the default connection here is the read-only warehouse")
}

// TestScratchTarget_FollowsAddAndRemoveConnection: a connection added through
// the admin API can hold a table from its first call rather than from the next
// restart, and one removed stops offering the target immediately.
func TestScratchTarget_FollowsAddAndRemoveConnection(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]Config{"warehouse": {Host: "trino.example.com", User: "u"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	require.NoError(t, tk.AddConnection("scratch", map[string]any{
		"host": "trino.example.com",
		"user": "u",
		"scratch": map[string]any{
			"catalog": "scratch",
			"schema":  "uploads",
		},
	}))
	target, ok := tk.ScratchTarget("scratch")
	require.True(t, ok)
	assert.Equal(t, "scratch.uploads", target.Catalog+"."+target.Schema)

	require.NoError(t, tk.RemoveConnection("scratch"))
	_, ok = tk.ScratchTarget("scratch")
	assert.False(t, ok)
}

// TestScratchTarget_ReAddingWithoutATargetClearsIt: editing a connection to
// drop its scratch block must take the target away, not leave the old one
// standing.
func TestScratchTarget_ReAddingWithoutATargetClearsIt(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]Config{
			"warehouse": {Host: "trino.example.com", User: "u"},
			"scratch": {
				Host:    "trino.example.com",
				User:    "u",
				Scratch: ScratchConfig{Catalog: "scratch", Schema: "uploads"},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	_, ok := tk.ScratchTarget("scratch")
	require.True(t, ok)

	require.NoError(t, tk.AddConnection("scratch", map[string]any{"host": "trino.example.com", "user": "u"}))
	_, ok = tk.ScratchTarget("scratch")
	assert.False(t, ok)
}

// TestExec_ReadOnlyConnectionRefusesWriteSQL is the guard the whole path
// exists for.
func TestExec_ReadOnlyConnectionRefusesWriteSQL(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]Config{
			"warehouse": {Host: "trino.example.com", User: "u", ReadOnly: true},
			"scratch":   {Host: "trino.example.com", User: "u"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	const ddl = `CREATE TABLE "scratch"."uploads"."t" ("a" VARCHAR)`
	err = tk.Exec(context.Background(), "warehouse", ddl)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
	assert.Contains(t, err.Error(), "warehouse", "the refusal names the connection it applies to")
}

// TestExec_UnconfiguredConnectionRefuses: a name the toolkit holds no setting
// for is not one it routes to, so the door closes rather than opening.
func TestExec_UnconfiguredConnectionRefuses(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]Config{"warehouse": {Host: "trino.example.com", User: "u", ReadOnly: true}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	err = tk.Exec(context.Background(), "ghost", `CREATE TABLE "a"."b"."c" ("x" VARCHAR)`)
	require.Error(t, err)
	// The manager rejects the unknown name before the interceptor is reached.
	assert.Contains(t, err.Error(), "resolving trino connection")
}

// TestExec_SingleConnectionReadOnlyRefuses covers the single-connection
// toolkit, where the connection argument selects nothing and read_only holds
// for every call. The interceptor is kept on the toolkit for exactly this.
func TestExec_SingleConnectionReadOnlyRefuses(t *testing.T) {
	tk, err := New("only", Config{Host: "trino.example.com", User: "u", ReadOnly: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	err = tk.Exec(context.Background(), "", `INSERT INTO a.b.c VALUES (1)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-only mode")
}

// TestExec_NoClientAvailable is the unwired shape: a toolkit with neither a
// manager nor a client reports it rather than dereferencing nothing.
func TestExec_NoClientAvailable(t *testing.T) {
	tk := &Toolkit{name: "empty"}
	err := tk.Exec(context.Background(), "", "SELECT 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Trino client available")
}

// TestCheckExecWritable_ReadSQLIsNeverRefused: the interceptor only decides on
// write SQL, so a read statement passes whatever the connection's setting.
func TestCheckExecWritable_ReadSQLIsNeverRefused(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]Config{"warehouse": {Host: "trino.example.com", User: "u", ReadOnly: true}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	assert.NoError(t, tk.checkExecWritable(context.Background(), "warehouse", "SELECT 1"))
	assert.Error(t, tk.checkExecWritable(context.Background(), "warehouse", "DROP TABLE a.b.c"))

	// An empty connection name resolves to the default, which is what the
	// interceptor's own Before hook does on the MCP path.
	assert.Error(t, tk.checkExecWritable(context.Background(), "", "DROP TABLE a.b.c"))
}

// TestCheckExecWritable_NoInterceptorPermits covers a toolkit where no
// connection restricts writes: there is nothing to check.
func TestCheckExecWritable_NoInterceptorPermits(t *testing.T) {
	tk := &Toolkit{name: "open"}
	assert.NoError(t, tk.checkExecWritable(context.Background(), "any", "DROP TABLE a.b.c"))
}

// TestGetScratchConfig covers the parse, including the half-configured block
// that is dropped rather than half-honored.
func TestGetScratchConfig(t *testing.T) {
	full := getScratchConfig(map[string]any{
		"scratch": map[string]any{"catalog": "scratch", "schema": "uploads"},
	})
	assert.Equal(t, ScratchConfig{Catalog: "scratch", Schema: "uploads"}, full)

	assert.Equal(t, ScratchConfig{}, getScratchConfig(map[string]any{}),
		"no block means registration is unavailable")
	assert.Equal(t, ScratchConfig{}, getScratchConfig(map[string]any{"scratch": "not-a-map"}))
	assert.Equal(t, ScratchConfig{}, getScratchConfig(map[string]any{
		"scratch": map[string]any{"catalog": "scratch"},
	}), "a catalog with no schema is not a usable target")
	assert.Equal(t, ScratchConfig{}, getScratchConfig(map[string]any{
		"scratch": map[string]any{"schema": "uploads"},
	}), "a schema with no catalog is not a usable target")
}

// TestParseConfig_ReadsTheScratchTarget pins that the YAML reaches Config.
func TestParseConfig_ReadsTheScratchTarget(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"host":    "trino.example.com",
		"user":    "u",
		"scratch": map[string]any{"catalog": "scratch", "schema": "uploads"},
	})
	require.NoError(t, err)
	assert.True(t, cfg.Scratch.Configured())
	assert.Equal(t, "scratch", cfg.Scratch.Catalog)
}

func TestBuildScratchTargets(t *testing.T) {
	targets := buildScratchTargets(map[string]Config{
		"warehouse": {},
		"scratch":   {Scratch: ScratchConfig{Catalog: "c", Schema: "s"}},
		"half":      {Scratch: ScratchConfig{Catalog: "c"}},
	})
	assert.Len(t, targets, 1, "only a usable target gets an entry")
	assert.Contains(t, targets, "scratch")
}

// TestExec_WritableConnectionPassesTheGuard: the read-only check is what Exec
// adds, and a writable connection gets past it to the client. There is no
// coordinator behind the client here, so what is asserted is that the failure
// comes from execution rather than from the guard.
func TestExec_WritableConnectionPassesTheGuard(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "scratch",
		Instances: map[string]Config{
			"scratch": {
				Host: "127.0.0.1", Port: 1, User: "u", Timeout: time.Second,
				Scratch: ScratchConfig{Catalog: "scratch", Schema: "uploads"},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	err = tk.Exec(context.Background(), "scratch", `CREATE SCHEMA IF NOT EXISTS "scratch"."uploads"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "executing statement",
		"the guard permitted it and the transport is what failed")
	assert.NotContains(t, err.Error(), "read-only")
}

// TestExec_DefaultConnectionResolvesWhenNoneIsNamed, matching
// multiserver.Manager.Client("").
func TestExec_DefaultConnectionResolvesWhenNoneIsNamed(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "scratch",
		Instances: map[string]Config{
			"scratch": {Host: "127.0.0.1", Port: 1, User: "u", Timeout: time.Second},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	err = tk.Exec(context.Background(), "", "SELECT 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "executing statement")
}

// TestTableExists_NoClientAvailable is the unwired shape for the existence
// lookup (#1546): no manager and no client reports it rather than querying.
func TestTableExists_NoClientAvailable(t *testing.T) {
	tk := &Toolkit{name: "empty"}
	_, err := tk.TableExists(context.Background(), "", "scratch", "uploads", "t")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Trino client available")
}

// TestTableExists_UnconfiguredConnectionRefuses: a connection the toolkit does
// not hold is refused before any statement is built.
func TestTableExists_UnconfiguredConnectionRefuses(t *testing.T) {
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]Config{"warehouse": {Host: "trino.example.com", User: "u", ReadOnly: true}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	_, err = tk.TableExists(context.Background(), "ghost", "scratch", "uploads", "t")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving trino connection")
}

// TestTableExists_QuotesTheIdentifiersItIsHanded: the catalog, schema and table
// come from a registration row and are quoted rather than trusted.
func TestTableExists_QuotesTheIdentifiersItIsHanded(t *testing.T) {
	assert.Equal(t, `"scr""atch"`, quoteIdentifier(`scr"atch`))
	assert.Equal(t, `'up''loads'`, quoteLiteral(`up'loads`))
}

// fakeTrinoStatement serves the protocol the existence lookup drives: the
// POST to /v1/statement is queued with a nextUri, and the GET of that uri
// carries the columns, the rows (one when present is set) and the finished
// state. It records the statement so the test can read the identifiers it
// carried.
func fakeTrinoStatement(t *testing.T, present bool) (srv *httptest.Server, statement *string) {
	t.Helper()
	var sent string
	mux := http.NewServeMux()
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("POST /v1/statement", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"q1","infoUri":"%s/ui/q1","nextUri":"%s/v1/statement/q1/1","stats":{"state":"QUEUED"}}`, srv.URL, srv.URL)
	})
	mux.HandleFunc("GET /v1/statement/q1/1", func(w http.ResponseWriter, _ *http.Request) {
		data := "[]"
		if present {
			data = "[[1]]"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"q1","infoUri":"%s/ui/q1","columns":[{"name":"present","type":"integer","typeSignature":{"rawType":"integer","arguments":[]}}],"data":%s,"stats":{"state":"FINISHED"}}`, srv.URL, data)
	})
	return srv, &sent
}

// TestTableExists_AsksInformationSchema pins #1546's lookup end to end against
// a Trino that answers: the statement names the catalog's information_schema
// with the quoted identifiers, and the answer is the presence of a row.
func TestTableExists_AsksInformationSchema(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(fmt.Sprintf("present=%v", present), func(t *testing.T) {
			srv, statement := fakeTrinoStatement(t, present)
			u, err := url.Parse(srv.URL)
			require.NoError(t, err)
			port, err := strconv.Atoi(u.Port())
			require.NoError(t, err)
			off := false
			tk, err := NewMulti(MultiConfig{
				DefaultConnection: "warehouse",
				Instances: map[string]Config{
					"warehouse": {Host: "127.0.0.1", Port: port, User: "u", SSL: &off, ReadOnly: true},
					"scratch":   {Host: "127.0.0.1", Port: port, User: "u", SSL: &off},
				},
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tk.Close() })

			exists, err := tk.TableExists(context.Background(), "scratch", "scr", "uploads", "admin_x")
			require.NoError(t, err)
			assert.Equal(t, present, exists)
			assert.Contains(t, *statement, `FROM "scr".information_schema.tables WHERE table_schema = 'uploads' AND table_name = 'admin_x'`)
		})
	}
}
