package graphql

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// A connection reads its schema from its endpoint, or is handed one. A
// third source is a catalog: the same named, versioned spec bundle an
// OpenAPI connection references by catalog_id, holding one entry whose
// content is SDL (#1745).
//
// What that buys is what a catalog already gives an OpenAPI endpoint. A
// schema published at a stable address refreshes on its etag through the
// catalog's own URL path, so an endpoint that disables introspection no
// longer has to be re-pasted on every upstream change. Two connections
// against one endpoint — read-only and read-write, prod and sandbox —
// reference one catalog, so the schema is stored, hashed and embedded
// once rather than once each. And the schema becomes an inventoried
// object: listed, versioned and given embedding-job status beside every
// other spec, rather than reachable only through the connection that
// owns it.

var (
	// ErrCatalogSchemaNotFound is returned for a catalog that holds no
	// GraphQL schema.
	ErrCatalogSchemaNotFound = errors.New("graphql: the catalog holds no GraphQL schema")
	// ErrOperationNotFound is returned for an operation id a schema does
	// not expose.
	ErrOperationNotFound = errors.New("graphql: operation not found")
)

// CatalogSchema is the schema a catalog holds, as this kind reads it.
type CatalogSchema struct {
	// SpecName is the entry the schema is stored as, reported so an
	// operator can tell which spec a connection is answering with.
	SpecName string
	// SDL is the schema document.
	SDL string
}

// CatalogStore reads the GraphQL schema a catalog holds, and the
// operation embeddings written for it. Both are addressed by catalog
// alone: a catalog carries one GraphQL schema, which is what lets two
// connections referencing it share one stored copy and one embedding
// pass.
type CatalogStore interface {
	// CatalogSchema returns the schema the named catalog holds,
	// reporting a catalog with none with an error satisfying
	// ErrCatalogSchemaNotFound.
	CatalogSchema(ctx context.Context, catalogID string) (CatalogSchema, error)
	// CatalogVectors returns the operation embeddings written for that
	// schema, keyed by operation id. Empty until an embedding pass has
	// run, which leaves ranking lexical rather than wrong.
	CatalogVectors(ctx context.Context, catalogID string) (map[string][]float32, error)
}

// SetCatalogStore wires the store a catalog-backed connection reads its
// schema from. Passing nil leaves such a connection with no schema,
// reported as the read it could not make.
func (t *Toolkit) SetCatalogStore(s CatalogStore) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.catalogStore = s
}

// readCatalog reads the schema the connection's catalog holds. It
// changes nothing: the caller records what it found.
func (t *Toolkit) readCatalog(ctx context.Context, c *conn) (*gqlschema.Schema, error) {
	t.mu.RLock()
	store := t.catalogStore
	t.mu.RUnlock()
	if store == nil {
		return nil, fmt.Errorf("graphql: this deployment has no catalog store, so catalog %s cannot be read",
			c.cfg.CatalogID)
	}
	held, err := store.CatalogSchema(ctx, c.cfg.CatalogID)
	if err != nil {
		return nil, fmt.Errorf("graphql: reading catalog %s: %w", c.cfg.CatalogID, err)
	}
	parsed, err := gqlschema.Load(held.SDL)
	if err != nil {
		return nil, fmt.Errorf("graphql: the schema in catalog %s does not load: %w", c.cfg.CatalogID, err)
	}
	return parsed, nil
}

// catalogVectors reads the operation embeddings written for a
// catalog-backed connection's schema.
//
// They are keyed on the catalog's spec rather than on the connection, so
// two connections referencing one catalog read the rows one embedding
// pass wrote. A connection that reads its own endpoint keeps the
// per-connection vectors it always had.
func (t *Toolkit) catalogVectors(ctx context.Context, c *conn) map[string][]float32 {
	t.mu.RLock()
	store := t.catalogStore
	t.mu.RUnlock()
	if store == nil {
		return nil
	}
	vectors, err := store.CatalogVectors(ctx, c.cfg.CatalogID)
	if err != nil {
		slog.Warn("graphql: reading a catalog's operation embeddings failed",
			logKeyConnection, logsan.SanitizeForLog(c.cfg.ConnectionName),
			logKeyCatalogID, logsan.SanitizeForLog(c.cfg.CatalogID),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return vectors
}

// ReloadConnectionsByCatalog brings every connection referencing
// catalogID up to the schema that catalog now holds. It is how an edit
// to a catalog's schema — a paste, a refresh from its URL, an embedding
// pass — reaches the connections serving it, on this replica and, over
// the catalog reload announcement, on every other.
//
// A connection whose re-read fails keeps the schema it held, with the
// failure recorded beside it, exactly as a failed endpoint re-read does.
func (t *Toolkit) ReloadConnectionsByCatalog(ctx context.Context, catalogID string) {
	if catalogID == "" {
		return
	}
	for _, name := range t.connectionsOnCatalog(catalogID) {
		if err := t.RefreshSchema(ctx, name); err != nil {
			slog.Warn("graphql: re-reading a catalog's schema failed",
				logKeyConnection, logsan.SanitizeForLog(name),
				logKeyCatalogID, logsan.SanitizeForLog(catalogID),
				logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// connectionsOnCatalog names the connections referencing one catalog.
func (t *Toolkit) connectionsOnCatalog(catalogID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var names []string
	for name, c := range t.connections {
		if c.cfg.CatalogID == catalogID {
			names = append(names, name)
		}
	}
	return names
}

// SchemaOperations returns the operations an SDL document exposes, as
// graphql_discover reports them. It is how the catalog admin surface
// enumerates a spec entry holding a GraphQL schema without a connection
// referencing it — the parallel of apigateway.SpecOperations, so a
// catalog's operations browser renders either format from one shape
// (#1745).
func SchemaOperations(sdl string) ([]OperationSummary, error) {
	schema, err := gqlschema.Load(sdl)
	if err != nil {
		return nil, fmt.Errorf("graphql: the schema does not load: %w", err)
	}
	ops := gqlschema.Operations(schema, gqlschema.DefaultNamespaceDepth)
	out := make([]OperationSummary, 0, len(ops))
	for _, op := range ops {
		out = append(out, summarize(op))
	}
	return out, nil
}

// SchemaOperation returns one operation of an SDL document in the detail
// graphql_discover returns at its operation level: the arguments, the
// input types they reference, the return shape, and a document that
// already calls it. The parallel of apigateway.SpecOperation.
//
// An operation the schema does not expose is reported with
// ErrOperationNotFound.
func SchemaOperation(sdl, operationID string) (*OperationDetail, error) {
	schema, err := gqlschema.Load(sdl)
	if err != nil {
		return nil, fmt.Errorf("graphql: the schema does not load: %w", err)
	}
	op, found := gqlschema.Lookup(gqlschema.Operations(schema, gqlschema.DefaultNamespaceDepth), operationID)
	if !found {
		return nil, fmt.Errorf("graphql: %s: %w", operationID, ErrOperationNotFound)
	}
	detail, err := gqlschema.Describe(schema, op, defaultSelectionDepth)
	if err != nil {
		return nil, fmt.Errorf("graphql: describing %s: %w", operationID, err)
	}
	return &OperationDetail{
		OperationSummary: summarize(op),
		ArgumentDetails:  op.Arguments,
		InputTypes:       detail.InputTypes,
		ReturnShape:      detail.ReturnShape,
		Skeleton:         detail.Skeleton,
		Variables:        detail.Variables,
	}, nil
}
