package connid

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// mockTK is a single-connection toolkit whose instance name and configured
// connection name can differ, which is the distinction under test.
type mockTK struct {
	kind, name, conn string
}

func (m *mockTK) Kind() string                          { return m.kind }
func (m *mockTK) Name() string                          { return m.name }
func (m *mockTK) Connection() string                    { return m.conn }
func (*mockTK) RegisterTools(_ *mcp.Server)             {}
func (*mockTK) Tools() []string                         { return nil }
func (*mockTK) SetSemanticProvider(_ semantic.Provider) {}
func (*mockTK) SetQueryProvider(_ query.Provider)       {}
func (*mockTK) Close() error                            { return nil }

// listerTK is a multi-connection toolkit: one registered toolkit serving
// several connections, each named independently of it.
type listerTK struct {
	mockTK
	conns []toolkit.ConnectionDetail
}

func (m *listerTK) ListConnections() []toolkit.ConnectionDetail { return m.conns }

// declares is a Declarer over a fixed set of "kind/instance" keys.
type declares map[string]bool

func (d declares) DeclaresConnection(kind, instance string) bool { return d[kind+"/"+instance] }

func resolverFixture(declared Declarer) *Resolver {
	return NewResolver([]registry.Toolkit{
		&mockTK{kind: "s3", name: "data_lake", conn: "Data Lake"},
		&mockTK{kind: "datahub", name: "primary"},
		&listerTK{
			mockTK: mockTK{kind: "trino", name: "warehouse", conn: "warehouse"},
			conns:  []toolkit.ConnectionDetail{{Name: "warehouse"}, {Name: "staging"}},
		},
	}, declared)
}

// TestResolverByInstance covers the translation from the name an instance is
// configured and stored under to the name a call binds it by (#1396), which is
// the crossing every defect in this area has come from.
func TestResolverByInstance(t *testing.T) {
	r := resolverFixture(nil)

	assert.Equal(t, Bound("Data Lake"), r.ByInstance("s3", "data_lake").Bound,
		"a single-connection toolkit binds by its connection name")
	assert.Equal(t, Bound("primary"), r.ByInstance("datahub", "primary").Bound,
		"one that carries no connection name binds by its instance")
	assert.Equal(t, Bound("warehouse"), r.ByInstance("trino", "warehouse").Bound,
		"a multi-connection toolkit routes on the instance key, so it is already the bound name")
	assert.Equal(t, Bound("data_lake"), r.ByInstance("datahub", "data_lake").Bound,
		"the match is per kind, so a name another kind carries is not borrowed")

	unclaimed := r.ByInstance("s3", "unclaimed")
	assert.Equal(t, Bound("unclaimed"), unclaimed.Bound,
		"an instance no live toolkit claims keeps its own name")
	assert.False(t, unclaimed.Live, "and says nothing serves it")

	empty := NewResolver(nil, nil).ByInstance("s3", "data_lake")
	assert.Equal(t, Bound("data_lake"), empty.Bound)
	assert.False(t, empty.Live)
}

// TestResolverToolkitName pins the third identity. For a multi-connection
// toolkit it is neither of the connection's names, and the connection source
// map's per-kind fallback is keyed by it.
func TestResolverToolkitName(t *testing.T) {
	r := resolverFixture(nil)

	staging := r.ByInstance("trino", "staging")
	assert.Equal(t, Instance("staging"), staging.Instance)
	assert.Equal(t, Bound("staging"), staging.Bound)
	assert.Equal(t, "warehouse", staging.Toolkit,
		"the aggregate toolkit's own name, which names no connection")

	lake := r.ByInstance("s3", "data_lake")
	assert.Equal(t, "data_lake", lake.Toolkit,
		"a single-connection toolkit's name is its instance")
}

// TestResolverByBound is the reverse crossing: a tool call argument or a
// persona rule carries the bound name and the caller needs the instance.
func TestResolverByBound(t *testing.T) {
	r := resolverFixture(nil)

	got, ok := r.ByBound("s3", "Data Lake")
	require.True(t, ok)
	assert.Equal(t, Instance("data_lake"), got.Instance)

	_, ok = r.ByBound("s3", "data_lake")
	assert.False(t, ok,
		"the instance name is not a bound name, and must not resolve as one")

	_, ok = r.ByBound("trino", "nothing")
	assert.False(t, ok)
}

// TestResolverOwnership covers who owns a connection, which is what the admin
// API refuses a write or a delete on.
func TestResolverOwnership(t *testing.T) {
	r := resolverFixture(declares{"trino/warehouse": true, "s3/data_lake": true})

	assert.True(t, r.ByInstance("trino", "warehouse").IsFile())
	assert.True(t, r.ByInstance("s3", "data_lake").IsFile())
	assert.False(t, r.ByInstance("trino", "staging").IsFile(),
		"a connection the file does not declare belongs to the store")
	// The declaration is keyed by the INSTANCE, not the bound name: a
	// single-connection toolkit's connection_name is not what the file wrote.
	byBound := declares{"s3/Data Lake": true}
	assert.False(t, NewResolver([]registry.Toolkit{
		&mockTK{kind: "s3", name: "data_lake", conn: "Data Lake"},
	}, byBound).ByInstance("s3", "data_lake").IsFile())

	assert.False(t, resolverFixture(nil).ByInstance("trino", "warehouse").IsFile(),
		"a nil Declarer declares nothing")
}

// TestResolverAll covers the enumeration discovery and the persona boundary
// share, so the two cannot advertise different connection sets.
func TestResolverAll(t *testing.T) {
	r := resolverFixture(nil)

	trino := r.All("trino")
	require.Len(t, trino, 2)
	assert.Equal(t, []Bound{"warehouse", "staging"}, []Bound{trino[0].Bound, trino[1].Bound})
	assert.Empty(t, r.All("api"), "a kind with no registered toolkit serves nothing")

	assert.Len(t, r.All(""), 4, "an empty kind enumerates every kind")
	for _, c := range r.All("") {
		assert.True(t, c.Live)
	}

	// A lister currently serving nothing contributes no connection.
	none := NewResolver([]registry.Toolkit{
		&listerTK{mockTK: mockTK{kind: "api", name: "gw"}},
	}, nil)
	assert.Empty(t, none.All("api"))
}
