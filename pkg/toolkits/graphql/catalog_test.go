package graphql

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// catalogStore is the catalog a connection takes its schema from, over a
// map, counting its reads.
type catalogStore struct {
	mu        sync.Mutex
	schemas   map[string]CatalogSchema
	vectors   map[string]map[string][]float32
	schemaErr error
	vectorErr error
	reads     int
}

// CatalogSchema returns the schema one catalog holds.
func (s *catalogStore) CatalogSchema(_ context.Context, catalogID string) (CatalogSchema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.schemaErr != nil {
		return CatalogSchema{}, s.schemaErr
	}
	held, ok := s.schemas[catalogID]
	if !ok {
		return CatalogSchema{}, ErrCatalogSchemaNotFound
	}
	return held, nil
}

// CatalogVectors returns the embeddings written for that schema.
func (s *catalogStore) CatalogVectors(_ context.Context, catalogID string) (map[string][]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vectorErr != nil {
		return nil, s.vectorErr
	}
	return s.vectors[catalogID], nil
}

// setSchema replaces what the catalog holds, as an edit on its page does.
func (s *catalogStore) setSchema(catalogID string, held CatalogSchema) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schemas[catalogID] = held
}

// readCount reports how many times the catalog's schema was read.
func (s *catalogStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// catalogBackedToolkit returns a toolkit whose one connection takes its
// schema from a catalog holding the flat fixture, with the endpoint
// answering nothing: a connection that reached for it would fail, which
// is what proves the catalog was the source.
func catalogBackedToolkit(t *testing.T) (*Toolkit, *catalogStore) {
	t.Helper()
	u := newUpstream(t)
	u.respond = answer(`{"errors":[{"message":"introspection is disabled"}]}`)
	store := &catalogStore{schemas: map[string]CatalogSchema{
		"erp": {SpecName: "schema", SDL: string(fixtureSDL(t, "flat"))},
	}}
	tk := newToolkit(t, u, "", map[string]any{"catalog_id": "erp"})
	tk.SetCatalogStore(store)
	return tk, store
}

func TestAConnectionOnACatalogReadsTheCatalogAndNotItsEndpoint(t *testing.T) {
	tk, store := catalogBackedToolkit(t)

	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("reading the catalog's schema: %v", err)
	}
	if store.readCount() == 0 {
		t.Fatal("the catalog was never read")
	}
	info, err := tk.SchemaInfo("gql")
	if err != nil {
		t.Fatalf("reading the schema state: %v", err)
	}
	if info.Source != SchemaSourceCatalog {
		t.Errorf("the schema reports source %q; want the catalog it came from", info.Source)
	}
	if info.OperationCount == 0 {
		t.Error("the catalog's schema exposed no operations")
	}
	if info.Error != "" {
		t.Errorf("a refusal was recorded for a read that succeeded: %q", info.Error)
	}
}

func TestAConnectionOnACatalogWithNoSchemaReportsWhyItHasNone(t *testing.T) {
	tk, store := catalogBackedToolkit(t)
	store.schemas = map[string]CatalogSchema{}

	err := tk.RefreshSchema(context.Background(), "gql")
	if err == nil {
		t.Fatal("a catalog holding no schema was read without error")
	}
	if !strings.Contains(err.Error(), "erp") {
		t.Errorf("the refusal does not name the catalog: %v", err)
	}
}

func TestADeploymentWithNoCatalogStoreSaysSoRatherThanIntrospecting(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"errors":[{"message":"introspection is disabled"}]}`)
	tk := newToolkit(t, u, "", map[string]any{"catalog_id": "erp"})

	err := tk.RefreshSchema(context.Background(), "gql")
	if err == nil || !strings.Contains(err.Error(), "no catalog store") {
		t.Errorf("a deployment with no catalog store gave %v", err)
	}
}

func TestACatalogHoldingSomethingThatIsNotASchemaIsRefused(t *testing.T) {
	tk, store := catalogBackedToolkit(t)
	store.setSchema("erp", CatalogSchema{SpecName: "schema", SDL: "this is not a schema"})

	if err := tk.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Error("a catalog holding something that is not a schema was installed")
	}
}

func TestACatalogBackedConnectionRanksOnTheCatalogsVectors(t *testing.T) {
	tk, store := catalogBackedToolkit(t)
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("reading the catalog's schema: %v", err)
	}
	c, _, ok := tk.lookup("gql")
	if !ok {
		t.Fatal("the connection is not served")
	}
	var anyOperation string
	c.schemaMu.RLock()
	if len(c.operations) > 0 {
		anyOperation = c.operations[0].ID
	}
	c.schemaMu.RUnlock()
	if anyOperation == "" {
		t.Fatal("the schema exposed no operations")
	}

	// The embedding pass writes the catalog's rows; the connection must
	// read them back rather than the per-connection ones it would have
	// written for a schema it read itself.
	store.vectors = map[string]map[string][]float32{"erp": {anyOperation: {0.5, 0.5}}}
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("re-reading the catalog's schema: %v", err)
	}
	c, _, _ = tk.lookup("gql")
	c.schemaMu.RLock()
	got := len(c.vectors)
	c.schemaMu.RUnlock()
	if got != 1 {
		t.Errorf("the connection holds %d vectors; want the catalog's one", got)
	}
}

func TestVectorsTheCatalogCannotAnswerForLeaveRankingLexical(t *testing.T) {
	tk, store := catalogBackedToolkit(t)
	store.vectorErr = errors.New("the database is unreachable")

	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("reading the catalog's schema: %v", err)
	}
	c, _, _ := tk.lookup("gql")
	c.schemaMu.RLock()
	defer c.schemaMu.RUnlock()
	if len(c.vectors) != 0 {
		t.Errorf("a failed vector read installed %d vectors", len(c.vectors))
	}
	if c.schema == nil {
		t.Error("a failed vector read cost the connection its schema")
	}
}

func TestAnUploadToACatalogBackedConnectionIsRefusedAndNamesTheCatalog(t *testing.T) {
	tk, _ := catalogBackedToolkit(t)

	err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "flat"))
	if err == nil {
		t.Fatal("a schema was uploaded onto a connection whose catalog owns it")
	}
	if !strings.Contains(err.Error(), "erp") {
		t.Errorf("the refusal does not say where to make the edit: %v", err)
	}
}

func TestACatalogEditReachesEveryConnectionOnIt(t *testing.T) {
	tk, store := catalogBackedToolkit(t)
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("reading the catalog's schema: %v", err)
	}
	before, err := tk.SchemaInfo("gql")
	if err != nil {
		t.Fatalf("reading the schema state: %v", err)
	}

	store.setSchema("erp", CatalogSchema{SpecName: "schema", SDL: string(fixtureSDL(t, "namespaced"))})
	tk.ReloadConnectionsByCatalog(context.Background(), "erp")

	after, err := tk.SchemaInfo("gql")
	if err != nil {
		t.Fatalf("reading the schema state: %v", err)
	}
	if after.Hash == before.Hash {
		t.Error("an edit to the catalog did not reach the connection serving it")
	}
}

func TestAReloadOfACatalogNoConnectionNamesChangesNothing(t *testing.T) {
	tk, store := catalogBackedToolkit(t)
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("reading the catalog's schema: %v", err)
	}
	before := store.readCount()

	tk.ReloadConnectionsByCatalog(context.Background(), "other")
	tk.ReloadConnectionsByCatalog(context.Background(), "")

	if store.readCount() != before {
		t.Error("a reload of another catalog read this one")
	}
}

func TestACatalogBackedConnectionIsNotIndexedPerConnection(t *testing.T) {
	tk, _ := catalogBackedToolkit(t)
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("reading the catalog's schema: %v", err)
	}

	// Its operations are embedded once, as the catalog's spec. Offering
	// them here would write a second copy of every vector that nothing
	// reads.
	if _, _, ok := tk.IndexItems("gql"); ok {
		t.Error("a catalog-backed connection offered items to the per-connection indexer")
	}
}

func TestAConnectionReportsTheCatalogItAnswersFrom(t *testing.T) {
	tk, _ := catalogBackedToolkit(t)

	details := tk.ListConnections()
	if len(details) != 1 {
		t.Fatalf("the toolkit lists %d connections", len(details))
	}
	if details[0].CatalogID != "erp" {
		t.Errorf("list_connections reports catalog_id %q", details[0].CatalogID)
	}
}

func TestAConnectionReadingItsOwnEndpointReportsNoCatalog(t *testing.T) {
	tk := newToolkit(t, newUpstream(t), "flat", nil)

	details := tk.ListConnections()
	if len(details) != 1 || details[0].CatalogID != "" {
		t.Errorf("a connection with no catalog reports %+v", details)
	}
}

func TestSchemaOperationsEnumeratesAnSDLWithoutAConnection(t *testing.T) {
	ops, err := SchemaOperations(string(fixtureSDL(t, "flat")))
	if err != nil {
		t.Fatalf("enumerating the schema: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("the schema enumerated no operations")
	}
	for _, op := range ops {
		if op.OperationID == "" || op.Kind == "" || op.Path == "" {
			t.Errorf("an operation is missing what a page renders it by: %+v", op)
		}
	}
}

func TestSchemaOperationsRefusesSomethingThatIsNotASchema(t *testing.T) {
	if _, err := SchemaOperations("this is not a schema"); err == nil {
		t.Error("something that is not a schema enumerated operations")
	}
}

func TestSchemaOperationDescribesOneOperationAsDiscoverDoes(t *testing.T) {
	sdl := string(fixtureSDL(t, "flat"))
	ops, err := SchemaOperations(sdl)
	if err != nil {
		t.Fatalf("enumerating the schema: %v", err)
	}

	detail, err := SchemaOperation(sdl, ops[0].OperationID)
	if err != nil {
		t.Fatalf("describing %s: %v", ops[0].OperationID, err)
	}
	if detail.OperationID != ops[0].OperationID {
		t.Errorf("the detail describes %q", detail.OperationID)
	}
	// The skeleton is the load-bearing part of the operation level: it is
	// the difference between a correct call and several spent on
	// validation errors.
	if detail.Skeleton == "" {
		t.Error("the operation detail carries no document that calls it")
	}
}

func TestSchemaOperationRefusesAnOperationTheSchemaDoesNotExpose(t *testing.T) {
	_, err := SchemaOperation(string(fixtureSDL(t, "flat")), "query:nothing.like.this")
	if !errors.Is(err, ErrOperationNotFound) {
		t.Errorf("an unknown operation gave %v", err)
	}
	if _, err := SchemaOperation("this is not a schema", "query:x"); err == nil {
		t.Error("something that is not a schema described an operation")
	}
}
