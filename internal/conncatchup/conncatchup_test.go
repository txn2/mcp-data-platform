package conncatchup

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// errNotFound is the kind sentinel a test store reports an absent
// connection with.
var errNotFound = errors.New("test: connection not found")

// memoryStore is a connection store over a map, counting its reads so a
// test can assert the store is touched once for a burst and not at all
// for a connection the toolkit already serves.
type memoryStore struct {
	mu      sync.Mutex
	configs map[string]map[string]any
	reads   int
	err     error
	onRead  func()
}

// GetConnection returns the stored configuration of one connection.
func (s *memoryStore) GetConnection(_ context.Context, name string) (map[string]any, error) {
	s.mu.Lock()
	s.reads++
	onRead := s.onRead
	s.onRead = nil
	err := s.err
	config, ok := s.configs[name]
	s.mu.Unlock()
	if onRead != nil {
		onRead()
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotFound
	}
	return config, nil
}

// readCount reports how many times the store was read.
func (s *memoryStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// forget drops a connection, as a deletion on another replica does.
func (s *memoryStore) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.configs, name)
}

// memoryServer is a toolkit's connection map as the catch-up changes it.
type memoryServer struct {
	mu        sync.Mutex
	served    map[string]map[string]any
	installs  int
	installer func(name string) error
}

// newServer returns a server holding the named connections.
func newServer(names ...string) *memoryServer {
	s := &memoryServer{served: map[string]map[string]any{}}
	for _, n := range names {
		s.served[n] = map[string]any{}
	}
	return s
}

// HasConnection reports whether the server holds name.
func (s *memoryServer) HasConnection(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.served[name]
	return ok
}

// Install puts a stored connection in service.
func (s *memoryServer) Install(_ context.Context, name string, config map[string]any) error {
	if s.installer != nil {
		if err := s.installer(name); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installs++
	s.served[name] = config
	return nil
}

// Withdraw takes a connection out of service.
func (s *memoryServer) Withdraw(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.served, name)
}

// installCount reports how many connections were installed.
func (s *memoryServer) installCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installs
}

// newResolver returns a resolver over its own locks.
func newResolver() *Resolver {
	return New(&Locks{}, "test", errNotFound)
}

func TestAConnectionSavedOnAnotherReplicaIsTakenOn(t *testing.T) {
	server := newServer()
	store := &memoryStore{configs: map[string]map[string]any{"c": {"endpoint_url": "http://example.test"}}}

	if !newResolver().Resolve(context.Background(), server, store, "c") {
		t.Fatal("a connection the store holds was not taken on")
	}
	if !server.HasConnection("c") {
		t.Error("the connection was reported served but is not in the map")
	}
	if got := server.served["c"]["endpoint_url"]; got != "http://example.test" {
		t.Errorf("the connection was installed with %v, not the stored config", got)
	}
}

func TestAConnectionNobodySavedIsNotServed(t *testing.T) {
	server := newServer()
	store := &memoryStore{configs: map[string]map[string]any{}}

	if newResolver().Resolve(context.Background(), server, store, "c") {
		t.Error("a connection the store does not hold was served")
	}
	if server.installCount() != 0 {
		t.Error("a connection the store does not hold was installed")
	}
}

func TestAStoreThatCannotAnswerServesNothing(t *testing.T) {
	server := newServer()
	store := &memoryStore{err: errors.New("the database is unreachable")}

	if newResolver().Resolve(context.Background(), server, store, "c") {
		t.Error("a connection was served on a store error")
	}
}

func TestAnInstanceWithNoStoreServesNothing(t *testing.T) {
	server := newServer()

	if newResolver().Resolve(context.Background(), server, nil, "c") {
		t.Error("an instance with no connection store served an unknown connection")
	}
}

func TestAnEmptyNameReadsNothing(t *testing.T) {
	store := &memoryStore{configs: map[string]map[string]any{"": {}}}

	if newResolver().Resolve(context.Background(), newServer(), store, "") {
		t.Error("the empty connection name was served")
	}
	if store.readCount() != 0 {
		t.Error("the empty connection name read the connection store")
	}
}

func TestAConnectionAlreadyServedReadsNothing(t *testing.T) {
	server := newServer("c")
	store := &memoryStore{configs: map[string]map[string]any{"c": {}}}

	if !newResolver().Resolve(context.Background(), server, store, "c") {
		t.Error("a served connection was reported unserved")
	}
	if store.readCount() != 0 {
		t.Errorf("a served connection read the store %d times", store.readCount())
	}
	if server.installCount() != 0 {
		t.Error("a served connection was installed again")
	}
}

func TestOneReadServesABurstOfRequests(t *testing.T) {
	server := newServer()
	store := &memoryStore{configs: map[string]map[string]any{"c": {}}}
	resolver := newResolver()

	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if !resolver.Resolve(context.Background(), server, store, "c") {
				t.Error("a request did not find the saved connection")
			}
		})
	}
	wg.Wait()

	// One read takes the connection on and one confirms it is still
	// saved; a request that arrives once it is served reads nothing.
	if n := store.readCount(); n != 2 {
		t.Errorf("the store was read %d times; want one take-on read and one confirmation", n)
	}
	if server.installCount() != 1 {
		t.Errorf("the connection was installed %d times", server.installCount())
	}
}

func TestAConnectionDeletedWhileItWasReadIsWithdrawn(t *testing.T) {
	server := newServer()
	store := &memoryStore{configs: map[string]map[string]any{"c": {}}}
	// The deletion commits on another replica between the read that finds
	// the connection and the confirmation that follows it, which is the
	// window where the deletion's announcement removed nothing here.
	store.onRead = func() { store.forget("c") }

	newResolver().Resolve(context.Background(), server, store, "c")

	if server.HasConnection("c") {
		t.Error("a connection deleted while it was read stayed in service")
	}
}

func TestAConnectionStillSavedIsNotWithdrawn(t *testing.T) {
	server := newServer()
	store := &memoryStore{configs: map[string]map[string]any{"c": {}}}

	newResolver().Resolve(context.Background(), server, store, "c")

	if !server.HasConnection("c") {
		t.Error("a connection the store still holds was withdrawn")
	}
}

func TestAnInstallThatFailsServesNothing(t *testing.T) {
	server := newServer()
	server.installer = func(string) error { return errors.New("the config does not parse") }
	store := &memoryStore{configs: map[string]map[string]any{"c": {}}}

	if newResolver().Resolve(context.Background(), server, store, "c") {
		t.Error("a connection that could not be installed was reported served")
	}
	// The confirmation read belongs to a connection that was installed;
	// one that was not is not confirmed.
	if store.readCount() != 1 {
		t.Errorf("the store was read %d times for a connection that could not be installed", store.readCount())
	}
}

func TestACancelledRequestDoesNotEndTheSharedRead(t *testing.T) {
	server := newServer()
	store := &memoryStore{configs: map[string]map[string]any{"c": {}}}
	ctx, cancel := context.WithCancel(context.Background())
	// The request that started the read is gone by the time the store is
	// asked; the read is shared, so it must not travel on that deadline.
	store.onRead = cancel

	if !newResolver().Resolve(ctx, server, store, "c") {
		t.Error("a shared read ended with the request that started it")
	}
}

func TestLocksForgetANameNobodyHolds(t *testing.T) {
	var locks Locks
	unlockA := locks.Lock("a")
	unlockB := locks.Lock("b")
	done := make(chan struct{})
	go func() {
		defer close(done)
		locks.Lock("a")()
	}()
	unlockA()
	<-done
	unlockB()
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if len(locks.names) != 0 {
		t.Errorf("locks held for names nobody holds: %v", locks.names)
	}
}

func TestLocksSerializeOneNameAndNotAnother(t *testing.T) {
	var locks Locks
	unlockA := locks.Lock("a")

	// A different name does not wait on the one that is held.
	locks.Lock("b")()

	held := make(chan struct{})
	go func() {
		defer close(held)
		locks.Lock("a")()
	}()
	select {
	case <-held:
		t.Fatal("a second holder took a name's lock while it was held")
	default:
	}
	unlockA()
	<-held
}
