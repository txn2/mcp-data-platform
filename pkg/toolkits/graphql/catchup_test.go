package graphql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// What a replica does with a connection another replica saved (#1714): it is
// served complete or not at all, a peer's announcement installs what the store
// holds, a request for a connection this instance does not hold is answered
// from the connection store, and a request that uses a schema answers from the
// version the store holds.

// memoryConnections is a ConnectionStore over a map, counting reads. answers,
// when set, is consumed one read at a time before the map is consulted, so a
// test can script a deletion between two reads.
type memoryConnections struct {
	mu      sync.Mutex
	configs map[string]map[string]any
	answers []error
	err     error
	reads   int
}

func (m *memoryConnections) GetConnection(_ context.Context, name string) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if len(m.answers) > 0 {
		next := m.answers[0]
		m.answers = m.answers[1:]
		if next != nil {
			return nil, next
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	cfg, ok := m.configs[name]
	if !ok {
		return nil, fmt.Errorf("graphql: %s: %w", name, ErrConnectionNotFound)
	}
	return cfg, nil
}

func (m *memoryConnections) readCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reads
}

// introspections counts the introspection queries an endpoint was sent.
func (u *upstream) introspections() int {
	n := 0
	for _, req := range u.calls() {
		if strings.Contains(req.Query, "__schema") {
			n++
		}
	}
	return n
}

// onceOnly runs fn on the first call and does nothing after, for an
// introspection hook that acts on one read and lets the reads it causes
// through without waiting on it.
func onceOnly(fn func()) func() {
	var ran atomic.Bool
	return func() {
		if ran.CompareAndSwap(false, true) {
			fn()
		}
	}
}

// storedUpload writes the named fixture to the store as another replica's
// upload would, and returns its hash.
func storedUpload(t *testing.T, store *memorySchemaStore, u *upstream, fixture string) string {
	t.Helper()
	peer := newToolkit(t, u, "", nil)
	peer.SetSchemaStore(store)
	if err := peer.SetSchema(context.Background(), "gql", fixtureSDL(t, fixture)); err != nil {
		t.Fatalf("peer upload: %v", err)
	}
	info, _ := peer.SchemaInfo("gql")
	return info.Hash
}

func TestAddConnectionServesTheConnectionOnlyOnceItsReadIsOver(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	var servedDuringRead atomic.Bool
	servedDuringRead.Store(true)
	u.onIntrospection = func() { servedDuringRead.Store(tk.HasConnection("gql")) }

	if err := tk.AddConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if servedDuringRead.Load() {
		t.Error("the connection was served while its schema was still being read")
	}
	if info, _ := tk.SchemaInfo("gql"); info.Hash == "" || info.OperationCount == 0 {
		t.Errorf("info = %+v; the added connection is served with the schema its read found", info)
	}
	if err := tk.AddConnection("gql", map[string]any{"endpoint_url": u.server.URL}); !errors.Is(err, ErrConnectionExists) {
		t.Errorf("re-adding gave %v; want ErrConnectionExists", err)
	}
	if err := tk.AddConnection("bad", map[string]any{"endpoint_url": u.server.URL, "auth_mode": "bearer"}); err == nil {
		t.Error("a connection whose authenticator cannot be built was added")
	}
}

func TestAddConnectionTakesTheStoreWhenARequestTookTheConnectionOnDuringTheRead(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	store := newMemorySchemaStore()
	connections := &memoryConnections{configs: map[string]map[string]any{"gql": {"endpoint_url": u.server.URL}}}
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetSchemaStore(store)
	tk.SetConnectionStore(connections)
	var tookOn atomic.Bool
	u.onIntrospection = onceOnly(func() {
		// A request arrives for the connection while the save's read is in
		// flight, takes it on, and an operator uploads a schema to it.
		if !tk.ServesConnection(context.Background(), "gql") {
			t.Error("the saved connection was not taken on")
		}
		if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
			t.Errorf("upload: %v", err)
		}
		tookOn.Store(true)
	})
	if err := tk.AddConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("add: %v", err)
	}
	stored, _ := store.GetSchema(context.Background(), "gql")
	info, _ := tk.SchemaInfo("gql")
	if info.Hash != stored.Hash {
		t.Errorf("serves %q while the store holds %q; the save serves what the store holds", info.Hash, stored.Hash)
	}
	if !tookOn.Load() {
		t.Fatal("the take-on never ran")
	}
}

func TestAdoptConnectionInstallsTheStoredSchemaWithoutReadingTheEndpoint(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	store := newMemorySchemaStore()
	hash := storedUpload(t, store, u, "namespaced")
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetSchemaStore(store)
	before := u.introspections()

	if err := tk.AdoptConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if n := u.introspections() - before; n != 0 {
		t.Errorf("adopting read the endpoint %d times; the store already holds the schema", n)
	}
	info, _ := tk.SchemaInfo("gql")
	if info.Hash != hash || info.Source != SchemaSourceUpload {
		t.Errorf("info = %+v; want the stored upload %q", info, hash)
	}
}

func TestAdoptConnectionReadsTheEndpointWhenTheStoreHoldsNoneAndStoresNothing(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	store := newMemorySchemaStore()
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetSchemaStore(store)

	if err := tk.AdoptConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	info, _ := tk.SchemaInfo("gql")
	if info.Source != SchemaSourceIntrospection || info.OperationCount == 0 {
		t.Errorf("info = %+v; with nothing stored the endpoint is read", info)
	}
	if store.putCalls != 0 {
		t.Errorf("adopting wrote the store %d times; the replica that took the save writes it", store.putCalls)
	}
}

func TestAdoptConnectionKeepsTheHeldSchemaWhenNothingIsStoredAndTheEndpointRefuses(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	tk := newToolkit(t, u, "flat", nil)
	before, _ := tk.SchemaInfo("gql")
	tk.SetSchemaStore(newMemorySchemaStore())

	if err := tk.AdoptConnection("gql", map[string]any{"endpoint_url": u.server.URL, "read_only": true}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	after, _ := tk.SchemaInfo("gql")
	if after.Hash != before.Hash || after.OperationCount != before.OperationCount {
		t.Errorf("the held schema was lost: before %+v, after %+v", before, after)
	}
	if !strings.Contains(after.Error, "introspection is not allowed") {
		t.Errorf("error = %q; the refused read is reported beside the held schema", after.Error)
	}
}

func TestAdoptConnectionRefusesAConfigurationThatCannotBeServed(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	if err := tk.AdoptConnection("gql", map[string]any{}); err == nil {
		t.Error("a configuration with no endpoint was adopted")
	}
	if err := tk.AdoptConnection("gql", map[string]any{"endpoint_url": "http://localhost", "auth_mode": "bearer"}); err == nil {
		t.Error("a configuration whose authenticator cannot be built was adopted")
	}
	if tk.HasConnection("gql") {
		t.Error("a refused adoption left a connection served")
	}
}

// TestAnUploadDuringAnAdoptionIsNotReplacedByIt: the adoption's endpoint read
// is in flight when an upload arrives for the same connection. The upload
// waits for the adoption rather than landing on the connection the adoption is
// about to replace, so what is served afterwards is the upload.
func TestAnUploadDuringAnAdoptionIsNotReplacedByIt(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	store := newMemorySchemaStore()
	tk := newToolkit(t, u, "", nil)
	tk.SetSchemaStore(store)

	var wg sync.WaitGroup
	u.onIntrospection = onceOnly(func() {
		wg.Go(func() {
			if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
				t.Errorf("upload: %v", err)
			}
		})
		// Give the upload the moment it needs to reach the connection's
		// lock; without the lock it would land on the connection now served.
		time.Sleep(50 * time.Millisecond)
	})

	if err := tk.AdoptConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	wg.Wait()
	info, _ := tk.SchemaInfo("gql")
	if info.Source != SchemaSourceUpload || info.OperationCount != namespacedOperationCount(t) {
		t.Errorf("info = %+v; the upload made during the adoption is what is served", info)
	}
}

func TestARequestForAConnectionAnotherReplicaSavedIsServed(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	store := newMemorySchemaStore()
	hash := storedUpload(t, store, u, "namespaced")
	connections := &memoryConnections{configs: map[string]map[string]any{"gql": {"endpoint_url": u.server.URL}}}
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetSchemaStore(store)
	tk.SetConnectionStore(connections)
	before := u.introspections()

	res, _, err := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "gql"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if msg := errorMessage(res); msg != "" {
		t.Fatalf("graphql_discover refused a saved connection: %s", msg)
	}
	info, _ := tk.CurrentSchemaInfo(context.Background(), "gql")
	if info.Hash != hash {
		t.Errorf("info = %+v; want the stored schema %q", info, hash)
	}
	if n := u.introspections() - before; n != 0 {
		t.Errorf("taking the connection on read the endpoint %d times", n)
	}

	// A document too: graphql_query and graphql_export find it the same way.
	out := callQuery(t, tk, QueryInput{Connection: "gql", Query: "{ __typename }"})
	if out.Connection != "gql" {
		t.Errorf("query answered for %q", out.Connection)
	}
}

func TestConcurrentRequestsForOneSavedConnectionShareOneTakeOn(t *testing.T) {
	u := newUpstream(t)
	store := newMemorySchemaStore()
	storedUpload(t, store, u, "flat")
	connections := &memoryConnections{configs: map[string]map[string]any{"gql": {"endpoint_url": u.server.URL}}}
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetSchemaStore(store)
	tk.SetConnectionStore(connections)

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if !tk.ServesConnection(context.Background(), "gql") {
				t.Error("a request did not find the saved connection")
			}
		})
	}
	wg.Wait()
	// One read takes the connection on and one confirms it is still saved;
	// a request that arrives once it is served reads nothing.
	if n := connections.readCount(); n != 2 {
		t.Errorf("the connection store was read %d times; want one take-on read and one confirmation", n)
	}
	reads := connections.readCount()
	tk.ServesConnection(context.Background(), "gql")
	if connections.readCount() != reads {
		t.Error("a request for a served connection read the connection store")
	}
}

func TestATakeOnOfAConnectionAlreadyServedReadsNothing(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	before, _, _ := tk.lookup("gql")
	connections := &memoryConnections{configs: map[string]map[string]any{"gql": {"endpoint_url": u.server.URL}}}

	// A request that missed the connection and reaches the take-on after a
	// save served it.
	tk.catchUp.Resolve(context.Background(), catchUpServer{t: tk}, connections, "gql")

	if connections.readCount() != 0 {
		t.Errorf("the connection store was read %d times for a served connection", connections.readCount())
	}
	if after, _, _ := tk.lookup("gql"); after != before {
		t.Error("a take-on replaced a served connection")
	}
}

func TestARequestForAConnectionNobodySavedFindsNothing(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	if tk.ServesConnection(context.Background(), "gql") {
		t.Error("an instance with no connection store served an unknown connection")
	}

	connections := &memoryConnections{configs: map[string]map[string]any{}}
	tk.SetConnectionStore(connections)
	if tk.ServesConnection(context.Background(), "gql") {
		t.Error("a connection the store does not hold was served")
	}
	if tk.ServesConnection(context.Background(), "") {
		t.Error("an empty name was served")
	}
	res, _, _ := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "gql"})
	if msg := errorMessage(res); !strings.Contains(msg, "not found") {
		t.Errorf("refusal = %q; want not found", msg)
	}
	if _, err := tk.CurrentSchemaInfo(context.Background(), "gql"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("CurrentSchemaInfo gave %v; want ErrConnectionNotFound", err)
	}

	connections.err = errors.New("database is down")
	if tk.ServesConnection(context.Background(), "gql") {
		t.Error("a connection store that cannot answer produced a connection")
	}
}

func TestAStoredConnectionThatCannotBeServedIsNotTakenOn(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetConnectionStore(&memoryConnections{configs: map[string]map[string]any{
		"unparsed": {},
		"unauthed": {"endpoint_url": "http://localhost", "auth_mode": "bearer"},
	}})
	for _, name := range []string{"unparsed", "unauthed"} {
		if tk.ServesConnection(context.Background(), name) {
			t.Errorf("%s was taken on", name)
		}
	}
}

// TestAConnectionDeletedWhileItWasTakenOnIsWithdrawn: the connection store
// held the connection when the take-on read it and no longer does when the
// take-on confirms it, which is a deletion that committed in between and was
// announced while this instance did not serve the connection.
func TestAConnectionDeletedWhileItWasTakenOnIsWithdrawn(t *testing.T) {
	u := newUpstream(t)
	store := newMemorySchemaStore()
	storedUpload(t, store, u, "flat")
	connections := &memoryConnections{
		configs: map[string]map[string]any{"gql": {"endpoint_url": u.server.URL}},
		answers: []error{nil, fmt.Errorf("graphql: gql: %w", ErrConnectionNotFound)},
	}
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetSchemaStore(store)
	tk.SetConnectionStore(connections)

	if tk.ServesConnection(context.Background(), "gql") {
		t.Error("a connection deleted during its take-on was served")
	}
	if tk.HasConnection("gql") {
		t.Error("a connection deleted during its take-on is still registered")
	}
}

func TestATakeOnWhoseConfirmationCannotBeReadKeepsTheConnection(t *testing.T) {
	u := newUpstream(t)
	store := newMemorySchemaStore()
	storedUpload(t, store, u, "flat")
	connections := &memoryConnections{
		configs: map[string]map[string]any{"gql": {"endpoint_url": u.server.URL}},
		answers: []error{nil, errors.New("database is down")},
	}
	tk := NewMulti(MultiConfig{DefaultName: "other"})
	tk.SetSchemaStore(store)
	tk.SetConnectionStore(connections)

	if !tk.ServesConnection(context.Background(), "gql") {
		t.Error("a store blip on the confirmation withdrew a connection the first read found")
	}
}

func TestARequestServesTheSchemaAnotherReplicaStoredBeforeItsAnnouncement(t *testing.T) {
	u := newUpstream(t)
	store := newMemorySchemaStore()
	tk := newToolkit(t, u, "", nil)
	tk.SetSchemaStore(store)
	if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "flat")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Another replica uploads; no announcement reaches this instance.
	hash := storedUpload(t, store, u, "namespaced")

	res, _, _ := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "gql"})
	if msg := errorMessage(res); msg != "" {
		t.Fatalf("discover: %s", msg)
	}
	if info, _ := tk.SchemaInfo("gql"); info.Hash != hash {
		t.Errorf("graphql_discover answered from %q; the store holds %q", info.Hash, hash)
	}

	// A request that finds the version unchanged reads the version alone.
	gets := store.getCount()
	versions := store.versionCount()
	callQuery(t, tk, QueryInput{Connection: "gql", Query: "{ __typename }"})
	if store.getCount() != gets {
		t.Error("a request on an unchanged schema read the whole schema")
	}
	if store.versionCount() != versions+1 {
		t.Errorf("a request read the stored version %d times; want once", store.versionCount()-versions)
	}
}

func TestARequestKeepsWhatItHoldsWhenTheStoreHasNothingToSay(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	before, _ := tk.SchemaInfo("gql")

	tk.SetSchemaStore(newMemorySchemaStore())
	info, err := tk.CurrentSchemaInfo(context.Background(), "gql")
	if err != nil || info != before {
		t.Errorf("an empty store changed what the connection holds: %+v (%v), before %+v", info, err, before)
	}

	down := newMemorySchemaStore()
	down.getErr = errors.New("database is down")
	tk.SetSchemaStore(down)
	if info, _ := tk.CurrentSchemaInfo(context.Background(), "gql"); info != before {
		t.Errorf("a store that cannot answer changed what the connection holds: %+v", info)
	}
}

// TestARefusedRereadIsRecordedBesideTheVersionTheStoreHolds: the re-read runs
// on an instance that has not been told of another replica's upload. The
// refusal is about the version the store holds, so it is recorded there and
// the uploading replica reports it too.
func TestARefusedRereadIsRecordedBesideTheVersionTheStoreHolds(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	store := newMemorySchemaStore()
	reader := newToolkit(t, u, "", nil)
	reader.SetSchemaStore(store)
	if err := reader.SetSchema(context.Background(), "gql", fixtureSDL(t, "flat")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	uploader := newToolkit(t, u, "", nil)
	uploader.SetSchemaStore(store)
	if err := uploader.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("upload: %v", err)
	}

	if err := reader.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query")
	}
	onReader, _ := reader.SchemaInfo("gql")
	onUploader, _ := uploader.CurrentSchemaInfo(context.Background(), "gql")
	if onReader.Hash != onUploader.Hash || onReader.Error == "" || onReader.Error != onUploader.Error {
		t.Errorf("the two instances disagree: reader %+v, uploader %+v", onReader, onUploader)
	}
}

// TestAReadThatFinishesAfterItsConnectionWasReplacedInstallsNothing: an
// announcement replaces the connection while a re-read is in flight. What the
// read found came through a connection that is no longer served, so neither
// what is served nor the store is changed by it.
func TestAReadThatFinishesAfterItsConnectionWasReplacedInstallsNothing(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	store := newMemorySchemaStore()
	hash := storedUpload(t, store, u, "namespaced")
	tk := newToolkit(t, u, "", nil)
	tk.SetSchemaStore(store)
	u.onIntrospection = onceOnly(func() {
		if err := tk.AdoptConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
			t.Errorf("adopt: %v", err)
		}
	})
	puts := store.putCalls

	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if info, _ := tk.SchemaInfo("gql"); info.Hash != hash {
		t.Errorf("serves %q; the replacement installed %q", info.Hash, hash)
	}
	if store.putCalls != puts {
		t.Error("a read through a replaced connection was stored")
	}
}

func TestARefusalThatFinishesAfterItsConnectionWasDeletedIsNotRecorded(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	tk := newToolkit(t, u, "flat", nil)
	c, _, _ := tk.lookup("gql")
	u.onIntrospection = onceOnly(func() {
		if err := tk.RemoveConnection("gql"); err != nil {
			t.Errorf("remove: %v", err)
		}
	})

	if err := tk.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query")
	}
	c.schemaMu.RLock()
	defer c.schemaMu.RUnlock()
	if c.schemaErr != "" {
		t.Errorf("a refusal was recorded on a deleted connection: %q", c.schemaErr)
	}
}

func TestNoSchemaMessageNamesAReadOnlyWhenOneFailed(t *testing.T) {
	pending := noSchemaMessage("gql", "")
	if strings.Contains(pending, "could not read") || !strings.Contains(pending, "has not finished reading") {
		t.Errorf("a connection whose read has not failed is refused with %q", pending)
	}
	failed := noSchemaMessage("gql", "HTTP 302")
	if !strings.Contains(failed, "could not read one from its endpoint (HTTP 302)") {
		t.Errorf("a failed read is refused with %q", failed)
	}
}
