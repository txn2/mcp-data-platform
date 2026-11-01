package schemagate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

type lister []registry.Toolkit

func (l lister) All() []registry.Toolkit { return l }

func TestTrinoDescriber_ResolvesConnections(t *testing.T) {
	multi, err := trinokit.NewMulti(trinokit.MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]trinokit.Config{
			"warehouse": {Host: "127.0.0.1", Port: 1, User: "u", Catalog: "hive", Schema: "sales"},
			"archive":   {Host: "127.0.0.1", Port: 1, User: "u", Catalog: "iceberg", Schema: "old"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = multi.Close() })
	d := TrinoDescriber{Toolkits: lister{multi}}

	ns, ok := d.Namespace("")
	require.True(t, ok)
	assert.Equal(t, Namespace{Connection: "warehouse", Catalog: "hive", Schema: "sales"}, ns)
	ns, ok = d.Namespace("archive")
	require.True(t, ok)
	assert.Equal(t, Namespace{Connection: "archive", Catalog: "iceberg", Schema: "old"}, ns)
	_, ok = d.Namespace("elsewhere")
	assert.False(t, ok)

	_, err = d.Describe(context.Background(), "elsewhere", "hive", "sales", "orders")
	require.ErrorIs(t, err, errNoConnection)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.Describe(ctx, "warehouse", "hive", "sales", "orders")
	require.Error(t, err, "a canceled describe reports its failure")
}

func TestTrinoDescriber_NoToolkits(t *testing.T) {
	_, ok := TrinoDescriber{}.Namespace("")
	assert.False(t, ok)
	_, ok = TrinoDescriber{Toolkits: lister{}}.Namespace("")
	assert.False(t, ok)
}

func TestNewStoreAndCleanup(t *testing.T) {
	store := searchgate.NewMemoryStore(time.Millisecond)
	require.NoError(t, store.MarkDiscovered(context.Background(), "k"))

	g := New(Config{Store: store})
	stop := make(chan struct{})
	g.StartCleanup(5*time.Millisecond, stop)
	assert.Eventually(t, func() bool {
		ok, _ := store.HasDiscovered(context.Background(), "k")
		return !ok
	}, 2*time.Second, 5*time.Millisecond)
	close(stop)
}

func TestWireAssemblesAndStops(t *testing.T) {
	mw, stop := Wire(nil, time.Minute, lister{}, nil, true)
	require.NotNil(t, mw)
	stop()
}

type cleanupFails struct{ searchgate.Store }

func (cleanupFails) Cleanup(context.Context) error { return errors.New("db down") }

func TestCleanupFailureKeepsTheLoopRunning(t *testing.T) {
	calls := make(chan struct{}, 4)
	g := New(Config{Store: countingCleanup{cleanupFails{}, calls}})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	g.StartCleanup(time.Millisecond, stop)
	<-calls
	<-calls
}

type countingCleanup struct {
	cleanupFails
	calls chan struct{}
}

func (c countingCleanup) Cleanup(ctx context.Context) error {
	select {
	case c.calls <- struct{}{}:
	default:
	}
	return c.cleanupFails.Cleanup(ctx)
}

func TestColumnsOfKeepsNameTypeAndComment(t *testing.T) {
	got := columnsOf([]trinoclient.ColumnDef{{Name: "p_id", Type: "bigint", Nullable: "", Comment: "record id"}})
	assert.Equal(t, []Column{{Name: "p_id", Type: "bigint", Comment: "record id"}}, got)
}
