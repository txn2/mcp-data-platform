package callcatchup

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// errStoreNotFound is the platform store's own absent-record error, which
// Reader translates into ErrNotFound.
var errStoreNotFound = errors.New("connection instance not found")

// record stands in for the platform's ConnectionInstance.
type record struct{ config map[string]any }

// fakeStore is a RecordStore over a fixed set of records.
type fakeStore struct {
	mu         sync.Mutex
	records    map[string]record
	err        error
	persistent bool
	gets       int
}

func (f *fakeStore) Get(_ context.Context, kind, name string) (record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.err != nil {
		return record{}, f.err
	}
	r, ok := f.records[kind+"/"+name]
	if !ok {
		return record{}, errStoreNotFound
	}
	return r, nil
}

func (f *fakeStore) Persistent() bool { return f.persistent }

func (f *fakeStore) drop(kind, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, kind+"/"+name)
}

func (f *fakeStore) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

func storeOf(store *fakeStore) Store {
	return Reader(store, errStoreNotFound, func(r record) map[string]any { return r.config })
}

// managerTK is a toolkit that manages connections, as trino, s3, the gateway,
// api and graphql all do.
type managerTK struct {
	kind string
	mu   sync.Mutex
	held map[string]map[string]any
	fail error
}

func newManagerTK(kind string) *managerTK {
	return &managerTK{kind: kind, held: map[string]map[string]any{}}
}

func (m *managerTK) Kind() string                          { return m.kind }
func (m *managerTK) Name() string                          { return m.kind }
func (*managerTK) Connection() string                      { return "" }
func (*managerTK) RegisterTools(_ *mcp.Server)             {}
func (*managerTK) Tools() []string                         { return nil }
func (*managerTK) SetSemanticProvider(_ semantic.Provider) {}
func (*managerTK) SetQueryProvider(_ query.Provider)       {}
func (*managerTK) Close() error                            { return nil }

func (m *managerTK) AddConnection(name string, config map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.held[name] = config
	return nil
}

func (m *managerTK) RemoveConnection(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.held, name)
	return nil
}

func (m *managerTK) HasConnection(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.held[name]
	return ok
}

// source is a ToolkitSource over a fixed set of toolkits.
type source struct{ toolkits []registry.Toolkit }

func (s source) All() []registry.Toolkit { return s.toolkits }

func TestNew_NilWhereThereIsNothingToCatchUpTo(t *testing.T) {
	assert.Nil(t, New(nil, storeOf(&fakeStore{persistent: true})), "no registry is nowhere to put a connection")
	assert.Nil(t, New(source{}, nil), "no store is nothing to catch up to")
	assert.Nil(t, Reader(&fakeStore{persistent: false}, errStoreNotFound,
		func(r record) map[string]any { return r.config }),
		"a store that does not outlive the process holds nothing another replica wrote")
}

// TestTakeOn_ServesAConnectionSavedOnAnotherReplica is the defect: the row is
// committed before the save returns, and the announcement of it arrives later.
// A call landing here in between must be answered, not refused.
func TestTakeOn_ServesAConnectionSavedOnAnotherReplica(t *testing.T) {
	tk := newManagerTK("trino")
	store := &fakeStore{persistent: true, records: map[string]record{
		"trino/warehouse": {config: map[string]any{"dsn": "trino://example"}},
	}}

	New(source{toolkits: []registry.Toolkit{tk}}, storeOf(store)).
		TakeOn(context.Background(), "trino", "warehouse")

	assert.True(t, tk.HasConnection("warehouse"), "the connection the store holds is in service")
}

// TestTakeOn_AConnectionAlreadyServedCostsNoRead holds the cost contract: every
// tool call naming a connection passes through here, and the common case is a
// connection this process already serves.
func TestTakeOn_AConnectionAlreadyServedCostsNoRead(t *testing.T) {
	tk := newManagerTK("trino")
	require.NoError(t, tk.AddConnection("warehouse", map[string]any{}))
	store := &fakeStore{persistent: true, records: map[string]record{}}

	New(source{toolkits: []registry.Toolkit{tk}}, storeOf(store)).
		TakeOn(context.Background(), "trino", "warehouse")

	assert.Zero(t, store.getCount(), "a connection in service is not looked up")
}

// TestTakeOn_AConnectionNobodySavedIsNotServed: the handler's own refusal names
// the connection far better than this layer could, so a genuine typo must reach
// it unchanged.
func TestTakeOn_AConnectionNobodySavedIsNotServed(t *testing.T) {
	tk := newManagerTK("trino")
	store := &fakeStore{persistent: true, records: map[string]record{}}

	New(source{toolkits: []registry.Toolkit{tk}}, storeOf(store)).
		TakeOn(context.Background(), "trino", "typo")

	assert.False(t, tk.HasConnection("typo"))
}

// TestTakeOn_WithdrawsAConnectionDeletedWhileItWasTakenOn: a deletion that
// commits while the connection is being taken on was announced while this
// process did not serve it, so the announcement removed nothing here. Without
// the second read the deleted connection would be served until a restart.
func TestTakeOn_WithdrawsAConnectionDeletedWhileItWasTakenOn(t *testing.T) {
	tk := newManagerTK("api")
	store := &fakeStore{persistent: true, records: map[string]record{
		"api/billing": {config: map[string]any{"base_url": "https://example.test"}},
	}}
	// The toolkit's add is where the delete lands, which is the window the
	// second read closes.
	deleting := &deletingTK{managerTK: tk, onAdd: func() { store.drop("api", "billing") }}

	New(source{toolkits: []registry.Toolkit{deleting}}, storeOf(store)).
		TakeOn(context.Background(), "api", "billing")

	assert.False(t, tk.HasConnection("billing"), "a connection deleted mid-catch-up is not left in service")
}

// deletingTK runs onAdd during AddConnection, to drive the delete-during-
// catch-up window deterministically.
type deletingTK struct {
	*managerTK
	onAdd func()
}

func (d *deletingTK) AddConnection(name string, config map[string]any) error {
	if d.onAdd != nil {
		d.onAdd()
	}
	return d.managerTK.AddConnection(name, config)
}

// TestTakeOn_AKindWithNoLiveToolkitIsNotReportedAsServed: a stored connection
// of a kind this process did not construct cannot be put in service anywhere,
// and reporting it as taken on would leave the call to fail with no trace.
func TestTakeOn_AKindWithNoLiveToolkitIsNotReportedAsServed(t *testing.T) {
	tk := newManagerTK("trino")
	store := &fakeStore{persistent: true, records: map[string]record{
		"s3/lake": {config: map[string]any{"bucket": "b"}},
	}}

	New(source{toolkits: []registry.Toolkit{tk}}, storeOf(store)).
		TakeOn(context.Background(), "s3", "lake")

	assert.False(t, tk.HasConnection("lake"))
}

// TestTakeOn_ATookitThatRefusesTheConnectionLeavesItUnserved covers a stored
// configuration the kind cannot build: the call is refused by the handler, and
// nothing half-installed is left behind.
func TestTakeOn_ATookitThatRefusesTheConnectionLeavesItUnserved(t *testing.T) {
	tk := newManagerTK("trino")
	tk.fail = errors.New("the stored configuration names no server")
	store := &fakeStore{persistent: true, records: map[string]record{
		"trino/warehouse": {config: map[string]any{}},
	}}

	New(source{toolkits: []registry.Toolkit{tk}}, storeOf(store)).
		TakeOn(context.Background(), "trino", "warehouse")

	assert.False(t, tk.HasConnection("warehouse"))
}

// TestTakeOn_ABurstForOneConnectionReadsTheStoreOnce: a session that opens with
// several calls against one connection this process has not served would
// otherwise pay for the read once per call.
func TestTakeOn_ABurstForOneConnectionReadsTheStoreOnce(t *testing.T) {
	tk := newManagerTK("trino")
	store := &fakeStore{persistent: true, records: map[string]record{
		"trino/warehouse": {config: map[string]any{"dsn": "trino://example"}},
	}}
	resolver := New(source{toolkits: []registry.Toolkit{tk}}, storeOf(store))

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { resolver.TakeOn(context.Background(), "trino", "warehouse") })
	}
	wg.Wait()

	assert.True(t, tk.HasConnection("warehouse"))
	assert.LessOrEqual(t, store.getCount(), 16,
		"a burst for one connection shares its read rather than paying per call")
}

// TestTakeOn_IgnoresACallThatNamesNoConnection: a platform tool carries no
// toolkit kind, and a connection-routed tool called without one is answered by
// the toolkit's default.
func TestTakeOn_IgnoresACallThatNamesNoConnection(t *testing.T) {
	store := &fakeStore{persistent: true, records: map[string]record{}}
	resolver := New(source{toolkits: []registry.Toolkit{newManagerTK("trino")}}, storeOf(store))

	resolver.TakeOn(context.Background(), "", "warehouse")
	resolver.TakeOn(context.Background(), "trino", "")
	var absent *Resolver
	absent.TakeOn(context.Background(), "trino", "warehouse")

	assert.Zero(t, store.getCount())
}

// TestConnectionConfig_ReportsAStoreThatCannotAnswerSeparately: "no such
// connection" leaves the process serving what it serves; a store outage is a
// different thing and must not be reported as an absent connection.
func TestConnectionConfig_ReportsAStoreThatCannotAnswerSeparately(t *testing.T) {
	outage := errors.New("the database is unreachable")
	store := &fakeStore{persistent: true, err: outage}

	_, err := storeOf(store).ConnectionConfig(context.Background(), "trino", "warehouse")

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, err, outage)
}

// TestTakeOn_AKindThatHoldsNoConnectionsNeverReadsTheStore is the cost contract
// for the tools that dominate a session. The knowledge, portal and memory
// toolkits manage no connections, so a call to one of their tools must not
// reach the store — and unlike a connection that gets taken on, this one would
// never stop: there is nothing to install, so every call would read again.
func TestTakeOn_AKindThatHoldsNoConnectionsNeverReadsTheStore(t *testing.T) {
	store := &fakeStore{persistent: true, records: map[string]record{
		"knowledge/anything": {config: map[string]any{}},
	}}
	resolver := New(source{toolkits: []registry.Toolkit{&noConnTK{kind: "knowledge"}}}, storeOf(store))

	for range 3 {
		resolver.TakeOn(context.Background(), "knowledge", "anything")
	}

	assert.Zero(t, store.getCount(), "a kind that manages no connections is not looked up")
}

// noConnTK is a registry.Toolkit that does not implement
// toolkit.ConnectionManager.
type noConnTK struct{ kind string }

func (n *noConnTK) Kind() string                          { return n.kind }
func (n *noConnTK) Name() string                          { return n.kind }
func (*noConnTK) Connection() string                      { return "" }
func (*noConnTK) RegisterTools(_ *mcp.Server)             {}
func (*noConnTK) Tools() []string                         { return nil }
func (*noConnTK) SetSemanticProvider(_ semantic.Provider) {}
func (*noConnTK) SetQueryProvider(_ query.Provider)       {}
func (*noConnTK) Close() error                            { return nil }

// silentTK accepts an add and serves nothing, which is how a kind that installs
// a connection only once it is complete behaves when the completion fails.
type silentTK struct{ *managerTK }

func (*silentTK) AddConnection(string, map[string]any) error { return nil }

// TestTakeOn_AToolkitThatTookNothingOnIsNotReportedAsServing: a toolkit that
// accepts the configuration and serves nothing leaves the call to be refused by
// the handler, rather than the catch-up reporting a connection installed
// nowhere as in service.
func TestTakeOn_AToolkitThatTookNothingOnIsNotReportedAsServing(t *testing.T) {
	tk := newManagerTK("graphql")
	store := &fakeStore{persistent: true, records: map[string]record{
		"graphql/endpoint": {config: map[string]any{"endpoint_url": "https://example.test"}},
	}}

	New(source{toolkits: []registry.Toolkit{&silentTK{managerTK: tk}}}, storeOf(store)).
		TakeOn(context.Background(), "graphql", "endpoint")

	assert.False(t, tk.HasConnection("endpoint"))
}
