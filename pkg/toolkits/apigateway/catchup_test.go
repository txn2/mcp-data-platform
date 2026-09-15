package apigateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// savedConnections is the connection store another replica's save is read
// out of, counting its reads.
type savedConnections struct {
	mu      sync.Mutex
	configs map[string]map[string]any
	reads   int
	err     error
}

// GetConnection returns the stored configuration of one connection.
func (s *savedConnections) GetConnection(_ context.Context, name string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	config, ok := s.configs[name]
	if !ok {
		return nil, ErrConnectionNotFound
	}
	return config, nil
}

// readCount reports how many times the store was read.
func (s *savedConnections) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// forget drops a connection, as a deletion on another replica does.
func (s *savedConnections) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.configs, name)
}

// savedConfig is what an operator's save of one connection stores.
func savedConfig(baseURL string) map[string]any {
	return map[string]any{
		"base_url":        baseURL,
		"connect_timeout": "5s",
		"call_timeout":    "10s",
	}
}

// storeBackedToolkit returns a toolkit serving nothing, reading a store
// that holds one connection another replica saved.
func storeBackedToolkit(t *testing.T) (*Toolkit, *savedConnections) {
	t.Helper()
	tk := NewMulti(MultiConfig{})
	store := &savedConnections{configs: map[string]map[string]any{
		"saved": savedConfig("https://saved.example.com"),
	}}
	tk.SetConnectionStore(store)
	return tk, store
}

func TestAConnectionSavedOnAnotherReplicaIsServedBeforeItsAnnouncement(t *testing.T) {
	tk, _ := storeBackedToolkit(t)

	if tk.HasConnection("saved") {
		t.Fatal("the connection was served before anything asked for it")
	}
	if !tk.ServesConnection(context.Background(), "saved") {
		t.Fatal("a connection the store holds was not taken on")
	}
	c, ok := tk.lookup("saved")
	if !ok {
		t.Fatal("the connection was reported served but is not in the map")
	}
	if c.cfg.BaseURL != "https://saved.example.com" {
		t.Errorf("the connection was built with base_url %q, not the stored one", c.cfg.BaseURL)
	}
	if c.cfg.ConnectionName != "saved" {
		t.Errorf("the connection carries the name %q", c.cfg.ConnectionName)
	}
}

func TestAToolCallOnAConnectionSavedOnAnotherReplicaIsNotRefused(t *testing.T) {
	tk, _ := storeBackedToolkit(t)

	// The message an operator saw in the window this closes: a call to a
	// connection whose save has returned, told it does not exist.
	res, _, err := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "saved"})
	if err != nil {
		t.Fatalf("api_discover: %v", err)
	}
	if res.IsError {
		t.Fatalf("api_discover was refused for a saved connection: %v", res.Content)
	}
}

func TestAConnectionNobodySavedIsStillNotFound(t *testing.T) {
	tk, _ := storeBackedToolkit(t)

	if tk.ServesConnection(context.Background(), "absent") {
		t.Error("a connection the store does not hold was served")
	}
	res, _, err := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "absent"})
	if err != nil {
		t.Fatalf("api_discover: %v", err)
	}
	if !res.IsError {
		t.Error("api_discover answered for a connection nobody saved")
	}
}

func TestAnInstanceWithNoConnectionStoreServesWhatItWasHanded(t *testing.T) {
	tk := NewMulti(MultiConfig{})

	if tk.ServesConnection(context.Background(), "saved") {
		t.Error("an instance with no connection store served an unknown connection")
	}
}

func TestAServedConnectionDoesNotReadTheConnectionStore(t *testing.T) {
	tk, store := storeBackedToolkit(t)
	if !tk.ServesConnection(context.Background(), "saved") {
		t.Fatal("the connection was not taken on")
	}
	before := store.readCount()

	if !tk.ServesConnection(context.Background(), "saved") {
		t.Fatal("a served connection was reported unserved")
	}
	if store.readCount() != before {
		t.Error("a request for a served connection read the connection store")
	}
}

func TestOneReadServesABurstOfRequestsForOneConnection(t *testing.T) {
	tk, store := storeBackedToolkit(t)

	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if !tk.ServesConnection(context.Background(), "saved") {
				t.Error("a request did not find the saved connection")
			}
		})
	}
	wg.Wait()

	// One read takes the connection on and one confirms it is still saved;
	// a request that arrives once it is served reads nothing.
	if n := store.readCount(); n != 2 {
		t.Errorf("the connection store was read %d times; want one take-on read and one confirmation", n)
	}
}

func TestAConnectionDeletedWhileItWasReadIsNotLeftServed(t *testing.T) {
	tk, store := storeBackedToolkit(t)
	// The deletion commits on another replica between the read that finds
	// the connection and the confirmation that follows it, which is the
	// window where the deletion's announcement removed nothing here.
	tk.SetConnectionStore(deleteAfterFirstRead{store})

	tk.ServesConnection(context.Background(), "saved")

	if tk.HasConnection("saved") {
		t.Error("a connection deleted while it was read stayed in service")
	}
}

// deleteAfterFirstRead is the store as it behaves when a deletion commits
// on another replica during the read that takes the connection on.
type deleteAfterFirstRead struct{ store *savedConnections }

// GetConnection answers the first read and forgets the connection, so the
// confirmation that follows reports it deleted.
func (d deleteAfterFirstRead) GetConnection(ctx context.Context, name string) (map[string]any, error) {
	config, err := d.store.GetConnection(ctx, name)
	d.store.forget(name)
	return config, err
}

func TestAStoreThatCannotAnswerServesNothing(t *testing.T) {
	tk, store := storeBackedToolkit(t)
	store.err = errors.New("the database is unreachable")

	if tk.ServesConnection(context.Background(), "saved") {
		t.Error("a connection was served on a store failure")
	}
}

func TestAStoredConnectionThatDoesNotParseIsNotServed(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	tk.SetConnectionStore(&savedConnections{configs: map[string]map[string]any{
		"broken": {"base_url": "https://broken.example.com", "auth_mode": "weird"},
	}})

	if tk.ServesConnection(context.Background(), "broken") {
		t.Error("a stored connection that does not parse was served")
	}
}

func TestAnAnnouncementReplacesTheConnectionACatchUpTookOn(t *testing.T) {
	tk, _ := storeBackedToolkit(t)
	if !tk.ServesConnection(context.Background(), "saved") {
		t.Fatal("the connection was not taken on")
	}

	// The peer's announcement arrives after the catch-up already served
	// the connection; adopting it replaces rather than refusing, because
	// both build from the same stored configuration.
	if err := tk.AdoptConnection("saved", savedConfig("https://moved.example.com")); err != nil {
		t.Fatalf("adopting the announced connection: %v", err)
	}
	c, ok := tk.lookup("saved")
	if !ok {
		t.Fatal("the announced connection is not served")
	}
	if c.cfg.BaseURL != "https://moved.example.com" {
		t.Errorf("the announcement left base_url %q", c.cfg.BaseURL)
	}
}

func TestAnAnnouncementThatDoesNotParseIsRefused(t *testing.T) {
	tk := NewMulti(MultiConfig{})

	if err := tk.AdoptConnection("broken", map[string]any{"auth_mode": "weird"}); err == nil {
		t.Error("an announcement that does not parse was adopted")
	}
}

func TestAReloadRebuildsAConnectionWithoutLosingIt(t *testing.T) {
	tk, _ := storeBackedToolkit(t)
	if !tk.ServesConnection(context.Background(), "saved") {
		t.Fatal("the connection was not taken on")
	}
	before, _ := tk.lookup("saved")

	if err := tk.ReloadConnection("saved"); err != nil {
		t.Fatalf("reloading: %v", err)
	}
	after, ok := tk.lookup("saved")
	if !ok {
		t.Fatal("the connection is gone after a reload")
	}
	if after == before {
		t.Error("the reload did not rebuild the connection")
	}
	if after.cfg.BaseURL != before.cfg.BaseURL {
		t.Errorf("the reload changed base_url to %q", after.cfg.BaseURL)
	}
}

func TestReloadingAConnectionNobodyServesIsNotFound(t *testing.T) {
	tk := NewMulti(MultiConfig{})

	if err := tk.ReloadConnection("absent"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("reloading an unknown connection gave %v; want ErrConnectionNotFound", err)
	}
}

// A catalog may hold a spec for another kind: a GraphQL schema is SDL, read
// by a graphql connection referencing the same catalog and by nothing here.
// The gateway must recognize it by format rather than discover it as a parse
// failure, so a mixed catalog still serves its OpenAPI specs (#1745).
func TestACatalogsGraphQLSpecIsNotServedByTheHTTPGateway(t *testing.T) {
	const openAPI = `
openapi: 3.0.3
info:
  title: Orders
  version: "1"
servers:
  - url: https://orders.example.com/v1
paths:
  /orders:
    get:
      operationId: listOrders
      responses:
        '200':
          description: OK
`
	const sdl = "type Query { product(id: ID!): Product }\ntype Product { id: ID! }"

	store := catalog.NewMemoryStore()
	ctx := context.Background()
	if err := store.CreateCatalog(ctx, catalog.Catalog{ID: "mixed", Name: "mixed", Version: "v1"}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	for _, spec := range []catalog.SpecEntry{
		{SpecName: "rest", Content: openAPI, SourceKind: catalog.SourceInline},
		{SpecName: "schema", Content: sdl, SourceKind: catalog.SourceInline, SpecFormat: catalog.FormatGraphQL},
	} {
		if err := store.UpsertSpec(ctx, "mixed", spec); err != nil {
			t.Fatalf("UpsertSpec %s: %v", spec.SpecName, err)
		}
	}

	tk := New("api")
	tk.SetCatalogStore(store)
	if err := tk.AddConnection("orders", map[string]any{
		"base_url": "https://orders.example.com", "catalog_id": "mixed",
	}); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}

	c, ok := tk.lookup("orders")
	if !ok {
		t.Fatal("the connection is not served")
	}
	if len(c.operations) != 1 {
		t.Errorf("the connection holds %d operations; want only the OpenAPI spec's", len(c.operations))
	}
	if _, served := c.specs["schema"]; served {
		t.Error("the GraphQL spec was loaded as an OpenAPI document")
	}
	if _, served := c.specs["rest"]; !served {
		t.Error("a catalog holding a GraphQL spec cost the connection its OpenAPI one")
	}
}

// hookedCatalog is a catalog store that runs a hook on its first spec read,
// which is the slow step of building a connection and therefore the window a
// catch-up can land in.
type hookedCatalog struct {
	catalog.Store
	once sync.Once
	on   func()
}

// ListSpecs runs the hook once, then answers as the wrapped store does.
func (h *hookedCatalog) ListSpecs(ctx context.Context, catalogID string) ([]catalog.SpecEntry, error) {
	h.once.Do(func() {
		if h.on != nil {
			h.on()
		}
	})
	entries, err := h.Store.ListSpecs(ctx, catalogID)
	if err != nil {
		return nil, fmt.Errorf("hookedCatalog: %w", err)
	}
	return entries, nil
}

// The save that reaches AddConnection wrote the connection store before it
// got here, so a request on this same replica can take the connection on from
// the store while the save is still building it. What the save carries is the
// newer configuration and replaces what the catch-up installed; refusing it as
// a duplicate would report a save that succeeded as a failure (#1746).
func TestASaveSurvivesACatchUpThatLandsWhileItBuilds(t *testing.T) {
	tk := New("api")
	saved := &savedConnections{configs: map[string]map[string]any{
		"orders": savedConfig("https://stored.example.com"),
	}}
	tk.SetConnectionStore(saved)

	memory := catalog.NewMemoryStore()
	if err := memory.CreateCatalog(context.Background(), catalog.Catalog{
		ID: "orders-v1", Name: "orders-v1", Version: "v1",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	// The catch-up runs inside the save's catalog read, which is the only
	// window left once AddConnection has found the name unserved.
	tk.SetCatalogStore(&hookedCatalog{Store: memory, on: func() {
		tk.ServesConnection(context.Background(), "orders")
	}})

	config := savedConfig("https://saved.example.com")
	config["catalog_id"] = "orders-v1"
	if err := tk.AddConnection("orders", config); err != nil {
		t.Fatalf("the save that wrote the store was refused: %v", err)
	}
	c, ok := tk.lookup("orders")
	if !ok {
		t.Fatal("the connection is not served after the save")
	}
	if c.cfg.BaseURL != "https://saved.example.com" {
		t.Errorf("the save left base_url %q; want the configuration it carried", c.cfg.BaseURL)
	}
}

// A second save of a connection this instance already serves is still a
// duplicate: the guard is checked before the build, which is where the
// window a catch-up can slip through opens.
func TestASecondAddOfAServedConnectionIsStillADuplicate(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	if err := tk.AddConnection("orders", savedConfig("https://orders.example.com")); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}

	if err := tk.AddConnection("orders", savedConfig("https://orders.example.com")); !errors.Is(err, ErrConnectionExists) {
		t.Errorf("a duplicate add gave %v; want ErrConnectionExists", err)
	}
}

// A removal takes the name's lock, and the catch-up's own withdrawal runs
// under a lock the caller already holds; calling RemoveConnection from there
// would deadlock rather than remove.
func TestRemovingAConnectionTwiceIsNotFoundRatherThanAHang(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	if err := tk.AddConnection("orders", savedConfig("https://orders.example.com")); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}

	if err := tk.RemoveConnection("orders"); err != nil {
		t.Fatalf("removing the connection: %v", err)
	}
	if err := tk.RemoveConnection("orders"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("removing it again gave %v; want ErrConnectionNotFound", err)
	}
}
