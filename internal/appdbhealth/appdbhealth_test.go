package appdbhealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// answer is what the fake answers a statement containing marker.
type answer struct {
	marker string
	cols   []string
	rows   [][]any
	err    error
}

// fake answers each statement by the first answer whose marker it contains,
// and records the statements and params it was sent.
type fake struct {
	answers []answer
	sent    []postgres.Statement
}

func (f *fake) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	f.sent = append(f.sent, s)
	for _, a := range f.answers {
		if strings.Contains(s.SQL, a.marker) {
			if a.err != nil {
				return nil, a.err
			}
			cols := make([]postgres.Column, len(a.cols))
			for i, c := range a.cols {
				cols[i] = postgres.Column{Name: c}
			}
			return &postgres.Result{Columns: cols, Rows: a.rows}, nil
		}
	}
	return &postgres.Result{}, nil
}

func healthyAnswers() []answer {
	return []answer{
		{
			marker: "server_version_num", cols: []string{"version", "version_num", "database", "size_bytes", "max_connections", "xid_age", "cache_hit_ratio", "in_recovery", "stats_reset"},
			rows: [][]any{{"17.8", int64(170008), "orders", int64(9000), int64(100), int64(1800), 0.99, false, "2026-11-01T00:00:00Z"}},
		},
		{marker: "FROM pg_extension", cols: []string{"name", "version"}, rows: [][]any{{"plpgsql", "1.0"}, {statStatements, "1.11"}}},
		{marker: "GROUP BY 1 ORDER BY 1", cols: []string{"state", "n"}, rows: [][]any{{"active", int64(2)}, {"idle in transaction", int64(1)}}},
		{
			marker: "FROM pg_stat_user_tables", cols: []string{"schema", "name", "size_bytes", "row_estimate", "live_tuples", "dead_tuples", "seq_scans", "index_scans", "last_vacuum", "last_autovacuum", "last_analyze", "last_autoanalyze", "xid_age"},
			rows: [][]any{
				{"public", "orders", int64(4096), int64(3000), int64(3000), int64(1000), int64(4), int64(90), nil, "2026-11-01T00:00:00Z", nil, nil, int64(1700)},
				{"public", "tiny", int64(10), int64(4), int64(4), int64(6), int64(0), int64(0), nil, nil, nil, nil, int64(5)},
			},
		},
		{marker: "s.idx_scan = 0", cols: []string{"schema", "table", "name", "size_bytes", "scans"}, rows: [][]any{{"public", "orders", "orders_note_idx", int64(8192), int64(0)}}},
		{marker: "HAVING count(*) > 1", cols: []string{"schema", "table", "names", "definition"}, rows: [][]any{{"public", "items", []any{"a", "b"}, "CREATE INDEX a ON public.items USING btree (sku)"}}},
		{marker: "WHERE NOT i.indisvalid", cols: []string{"schema", "table", "name", "size_bytes", "scans"}, rows: [][]any{{"public", "items", "items_bad", int64(1), int64(0)}}},
		{
			marker: "pg_backend_pid()", cols: []string{"pid", "usename", "application", "state", "seconds", "state_seconds", "wait", "query"},
			rows: [][]any{
				{int64(10), "app", "psql", "idle in transaction", 40.0, 35.0, "", "UPDATE t SET x = 1"},
				{int64(11), "app", "api", "active", 90.0, 90.0, "Lock:relation", "SELECT pg_sleep(100)"},
			},
		},
		{marker: "pg_blocking_pids", cols: []string{"pid", "usename", "blocked_by", "seconds"}, rows: [][]any{{int64(11), "app", []any{int64(10)}, 12.5}}},
		{marker: "FROM pg_stat_replication", cols: []string{"name", "state", "lag_bytes", "lag_seconds"}, rows: [][]any{{"standby", "streaming", int64(2048), 0.5}}},
		{marker: "FROM pg_stat_statements", cols: []string{"query_id", "calls", "total_ms", "mean_ms", "rows", "query"}, rows: [][]any{{"42", int64(5), 10.0, 2.0, int64(5), "SELECT $1"}}},
	}
}

func TestRead_ReportsEverySection(t *testing.T) {
	f := &fake{answers: healthyAnswers()}
	r := Read(context.Background(), f, "shop", Options{LongRunning: 30 * time.Second})
	assert.Empty(t, r.Errors)
	assert.Equal(t, "17.8", r.Server.Version)
	assert.Equal(t, int64(100), r.Connections.Max)
	assert.Equal(t, int64(3), r.Connections.Total)
	require.Len(t, r.Tables, 2)
	assert.InDelta(t, 0.25, r.Tables[0].DeadRatio, 1e-9)
	assert.Equal(t, "2026-11-01T00:00:00Z", r.Tables[0].LastAutovacuum)
	assert.Equal(t, "orders_note_idx", r.Indexes.Unused[0].Name)
	assert.Equal(t, []string{"a", "b"}, r.Indexes.Duplicate[0].Names)
	assert.Equal(t, "items_bad", r.Indexes.Invalid[0].Name)
	require.Len(t, r.Sessions, 2)
	assert.Equal(t, []int64{10}, r.Blocking[0].BlockedBy)
	assert.Equal(t, int64(2048), r.Replication[0].LagBytes)
	assert.True(t, r.Statements.Installed)
	assert.Equal(t, "SELECT $1", r.Statements.Top[0].Query)
	for _, s := range f.sent {
		if strings.Contains(s.SQL, "pg_backend_pid()") {
			assert.Contains(t, s.SQL, "secs => 30", "the long-running bound reaches the statement")
		}
	}

	sum := Summarize(r)
	assert.Equal(t, 1, sum.IdleInTransaction)
	assert.InDelta(t, 35.0, sum.LongestIdleSeconds, 1e-9)
	assert.Equal(t, 1, sum.LongRunning)
	assert.Equal(t, "public.orders", sum.MaxDeadTable, "a table under the row floor is not judged")
	assert.InDelta(t, 0.03, sum.ConnectionShare(), 1e-9)
	assert.Equal(t, int64(2048), sum.ReplicaLagByte)
	assert.Equal(t, 1, sum.InvalidIndexes)
	assert.Zero(t, Summary{}.ConnectionShare())
}

func TestRead_ASectionTheDatabaseRefusesIsNamed(t *testing.T) {
	answers := append([]answer{{marker: "FROM pg_stat_replication", err: errors.New("appdb: SQLSTATE 42501")}}, healthyAnswers()...)
	r := Read(context.Background(), &fake{answers: answers}, "shop", Options{})
	assert.Equal(t, []SectionError{{Section: SectionReplication, Error: "appdb: SQLSTATE 42501"}}, r.Errors)
	assert.NotEmpty(t, r.Tables, "the rest of the report stands")
}

func TestRead_Statements(t *testing.T) {
	notInstalled := healthyAnswers()
	notInstalled[1].rows = [][]any{{"plpgsql", "1.0"}}
	r := Read(context.Background(), &fake{answers: notInstalled}, "shop", Options{})
	assert.False(t, r.Statements.Installed)
	assert.Contains(t, r.Statements.Note, "not installed")
	assert.Empty(t, r.Errors)

	notLoaded := append([]answer{{marker: "FROM pg_stat_statements", err: errors.New("appdb: SQLSTATE 55000 (object_not_in_prerequisite_state)")}}, healthyAnswers()...)
	r = Read(context.Background(), &fake{answers: notLoaded}, "shop", Options{})
	assert.Contains(t, r.Statements.Note, "shared_preload_libraries")
	assert.Empty(t, r.Errors)

	failed := append([]answer{{marker: "FROM pg_stat_statements", err: errors.New("appdb: SQLSTATE 42501")}}, healthyAnswers()...)
	r = Read(context.Background(), &fake{answers: failed}, "shop", Options{})
	assert.Equal(t, SectionStatements, r.Errors[0].Section)
}

func TestRead_AStandbyHasNoReplicationSection(t *testing.T) {
	answers := healthyAnswers()
	answers[0].rows[0][7] = true
	f := &fake{answers: answers}
	r := Read(context.Background(), f, "shop", Options{})
	assert.Empty(t, r.Replication)
	for _, s := range f.sent {
		assert.NotContains(t, s.SQL, "pg_stat_replication")
	}
}

func TestFindings(t *testing.T) {
	f := &fake{answers: healthyAnswers()}
	got := Findings(context.Background(), f, "shop", "public", 0.2)
	kinds := make([]string, 0, len(got))
	for _, x := range got {
		kinds = append(kinds, x.Kind)
	}
	assert.Equal(t, []string{FindingUnusedIndex, FindingDuplicateIndex, FindingDeadTuples}, kinds)
	for _, s := range f.sent {
		assert.Contains(t, s.SQL, "= $1", "every read is narrowed to the schema")
		assert.Equal(t, []any{"public"}, s.Params)
		assert.NotContains(t, s.SQL, "pg_stat_activity", "describe reads no session")
	}
	for _, x := range got {
		assert.NotContains(t, x.Detail, "UPDATE t SET", "a finding carries no statement text")
	}
	assert.Len(t, Findings(context.Background(), f, "shop", "public", 0.5), 2, "under the threshold, no dead tuple finding")
}

func TestInSchema(t *testing.T) {
	assert.Equal(t, "SELECT 1 FROM t WHERE x = $1\nORDER BY 1", inSchema("SELECT 1 FROM t\nORDER BY 1", "x"))
	assert.Equal(t, "SELECT 1 WHERE a AND x = $1\nGROUP BY 1", inSchema("SELECT 1 WHERE a\nGROUP BY 1", "x"))
	assert.Equal(t, "SELECT 1", inSchema("SELECT 1", "x"))
}

func TestRowReaders(t *testing.T) {
	r := row{"big": "9007199254740993", "f": int64(2), "n": 1.5, "s": 7}
	assert.Equal(t, int64(9007199254740993), r.num("big"))
	assert.Equal(t, int64(1), r.num("n"))
	assert.Zero(t, r.num("s"))
	assert.InDelta(t, 2.0, r.float("f"), 1e-9)
	assert.Zero(t, r.float("s"))
	assert.Equal(t, []int64{}, r.nums("missing"))
	assert.Equal(t, []string{}, r.strs("missing"))
}
