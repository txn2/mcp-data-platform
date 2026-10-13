package sqllineage

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptout/exportrecord"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

var errBoom = errors.New("boom")

type fakeAudit struct {
	logged []audit.Event
	err    error
}

func (f *fakeAudit) Log(_ context.Context, e audit.Event) error {
	f.logged = append(f.logged, e)
	return f.err
}

func (*fakeAudit) Query(context.Context, audit.QueryFilter) ([]audit.Event, error) {
	return []audit.Event{{ID: "q"}}, nil
}
func (*fakeAudit) Close() error { return errBoom }

type fakeEdges struct {
	edges []catalog.Edge
	err   error
}

func (f *fakeEdges) AddEdges(_ context.Context, e []catalog.Edge) error {
	f.edges = append(f.edges, e...)
	return f.err
}

func resolver(conns map[string]Connection) Resolver {
	return func(bound string) (Connection, bool) {
		c, ok := conns[bound]
		return c, ok
	}
}

var warehouse = map[string]Connection{"Warehouse": {Instance: "warehouse", Catalog: "hive", Schema: "sales"}}

func event(sql string) audit.Event {
	return audit.Event{
		ID: "evt-1", ToolName: "trino_execute", Connection: "Warehouse", Success: true,
		UserEmail: "ana@example.com", Parameters: map[string]any{"sql": sql},
	}
}

func TestRecorderRecordsAWriteFromItsSources(t *testing.T) {
	inner, edges := &fakeAudit{}, &fakeEdges{}
	r := New(inner, edges, resolver(warehouse))
	require.NoError(t, r.Log(context.Background(),
		event("CREATE TABLE daily AS SELECT * FROM orders o JOIN pg.public.users u ON o.uid = u.id")))
	require.Len(t, inner.logged, 1, "the event is written first")
	require.Len(t, edges.edges, 2)
	target := "mcp:dataset:(trino,warehouse,hive.sales.daily)"
	assert.Equal(t, catalog.Edge{
		Source: "mcp:dataset:(trino,warehouse,hive.sales.orders)", Target: target,
		WriterKind: catalog.WriterSQL, WriterRef: "evt-1", WrittenBy: "ana@example.com",
	}, edges.edges[0])
	assert.Equal(t, "mcp:dataset:(trino,warehouse,pg.public.users)", edges.edges[1].Source)
}

func TestRecorderSkips(t *testing.T) {
	cases := map[string]audit.Event{
		"a read":             event("SELECT * FROM orders"),
		"a failed statement": func() audit.Event { e := event("INSERT INTO t SELECT * FROM s"); e.Success = false; return e }(),
		"another tool":       func() audit.Event { e := event("INSERT INTO t SELECT * FROM s"); e.ToolName = "trino_query"; return e }(),
		"an unknown connection": func() audit.Event {
			e := event("INSERT INTO t SELECT * FROM s")
			e.Connection = "Nope"
			return e
		}(),
		"a self-copy": event("INSERT INTO orders SELECT * FROM orders"),
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			edges := &fakeEdges{}
			require.NoError(t, New(&fakeAudit{}, edges, resolver(warehouse)).Log(context.Background(), e))
			assert.Empty(t, edges.edges)
		})
	}

	edges := &fakeEdges{}
	noDefaults := resolver(map[string]Connection{"Warehouse": {Instance: "warehouse"}})
	require.NoError(t, New(&fakeAudit{}, edges, noDefaults).Log(context.Background(),
		event("INSERT INTO t SELECT * FROM a.b.c")))
	assert.Empty(t, edges.edges, "a target the defaults cannot complete names no dataset")
}

func TestRecorderPassesThrough(t *testing.T) {
	inner := &fakeAudit{err: errBoom}
	r := New(inner, &fakeEdges{err: errBoom}, resolver(warehouse))
	assert.ErrorIs(t, r.Log(context.Background(), event("INSERT INTO t SELECT * FROM s")), errBoom,
		"the audit write's error is returned; the lineage failure is logged")
	got, err := r.Query(context.Background(), audit.QueryFilter{})
	require.NoError(t, err)
	assert.Equal(t, "q", got[0].ID)
	assert.ErrorIs(t, r.Close(), errBoom)
}

func TestScriptRunLineage(t *testing.T) {
	edges := &fakeEdges{}
	s := NewScripts(edges, resolver(map[string]Connection{"": {Instance: "warehouse", Catalog: "hive", Schema: "sales"}}))
	run := s.Begin()
	var passed []string
	observe := run.Observe(func(tool string, _, _ map[string]any, _ error) { passed = append(passed, tool) })
	observe("trino_query", map[string]any{"sql": "SELECT * FROM orders"}, nil, nil)
	observe("trino_query", map[string]any{"sql": "SELECT * FROM orders"}, nil, nil)
	observe("trino_export", map[string]any{"sql": "INSERT INTO daily SELECT * FROM refunds"}, nil, nil)
	observe("trino_query", map[string]any{"sql": "SELECT * FROM failed"}, nil, errBoom)
	observe("api_invoke_endpoint", map[string]any{"sql": "SELECT * FROM x"}, nil, nil)
	observe("trino_query", map[string]any{"sql": "SELECT 1", "connection": "Nope"}, nil, nil)
	assert.Len(t, passed, 6, "every call still reaches the next observer")

	run.Record(context.Background(), "s1", "ana@example.com", []exportrecord.Record{
		{ResourceID: "r1"}, {AssetID: "a1"}, {Preview: true, ResourceID: "r2"}, {Bucket: "b", Key: "k"},
	})
	require.Len(t, edges.edges, 4, "two tables read, two files written")
	assert.Equal(t, catalog.Edge{
		Source: "mcp:dataset:(trino,warehouse,hive.sales.orders)", Target: "mcp:resource:r1",
		WriterKind: catalog.WriterScript, WriterRef: "mcp:script:s1", WrittenBy: "ana@example.com",
	}, edges.edges[0])
	assert.Equal(t, "mcp:dataset:(trino,warehouse,hive.sales.refunds)", edges.edges[1].Source,
		"the table a statement writes is not something it read")
	assert.Equal(t, "mcp:asset:a1", edges.edges[2].Target)

	edges.edges = nil
	s.Begin().Record(context.Background(), "s1", "", []exportrecord.Record{{ResourceID: "r1"}})
	assert.Empty(t, edges.edges, "a run that read nothing records nothing")

	edges.err = errBoom
	run.Record(context.Background(), "s1", "", []exportrecord.Record{{ResourceID: "r1"}})

	var nilScripts *Scripts
	nilRun := nilScripts.Begin()
	assert.Nil(t, nilRun)
	assert.Nil(t, nilRun.Observe(nil))
	nilRun.Record(context.Background(), "s", "", nil)
}

// stubToolkit stands for a Trino connection in the registry.
type stubToolkit struct{ kind, name, conn string }

func (s stubToolkit) Kind() string                        { return s.kind }
func (s stubToolkit) Name() string                        { return s.name }
func (s stubToolkit) Connection() string                  { return s.conn }
func (stubToolkit) RegisterTools(*mcp.Server)             {}
func (stubToolkit) Tools() []string                       { return nil }
func (stubToolkit) SetSemanticProvider(semantic.Provider) {}
func (stubToolkit) SetQueryProvider(query.Provider)       {}
func (stubToolkit) Close() error                          { return nil }

type lister []registry.Toolkit

func (l lister) All() []registry.Toolkit { return l }

func TestTrinoResolver(t *testing.T) {
	r := TrinoResolver(lister{stubToolkit{kind: "trino", name: "warehouse", conn: "Warehouse"}})
	c, ok := r("Warehouse")
	require.True(t, ok)
	assert.Equal(t, "warehouse", c.Instance)
	_, ok = r("Nope")
	assert.False(t, ok)

	_, ok = TrinoResolver(nil)("Warehouse")
	assert.False(t, ok)
	assert.Empty(t, defaultConnection(nil))
	cat, schema := trinoDefaults(lister{stubToolkit{kind: "s3"}}, "x")
	assert.Empty(t, cat+schema)
}

func TestTrinoResolverReadsTheToolkitDefaults(t *testing.T) {
	multi, err := trinokit.NewMulti(trinokit.MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]trinokit.Config{
			"warehouse": {Host: "trino.example.com", User: "u", Catalog: "hive", Schema: "sales"},
			"archive":   {Host: "trino.example.com", User: "u", Catalog: "iceberg", Schema: "old"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = multi.Close() })
	r := TrinoResolver(lister{stubToolkit{kind: "s3", name: "lake"}, multi})
	c, ok := r("archive")
	require.True(t, ok)
	assert.Equal(t, Connection{Instance: "archive", Catalog: "iceberg", Schema: "old"}, c)

	c, ok = r("")
	require.True(t, ok, "a call naming no connection runs on the default")
	assert.Equal(t, Connection{Instance: "warehouse", Catalog: "hive", Schema: "sales"}, c)

	assert.Equal(t, "warehouse", defaultConnection([]registry.Toolkit{multi}))
}
