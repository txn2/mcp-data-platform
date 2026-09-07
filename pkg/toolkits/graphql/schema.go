package graphql

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// SchemaInfo is what an operator surface reports about a connection's
// schema: which version the platform holds, where it came from, when,
// how many operations it exposes, and — when there is none — why.
type SchemaInfo struct {
	Connection     string    `json:"connection"`
	Hash           string    `json:"schema_hash,omitempty"`
	Source         string    `json:"source,omitempty"`
	FetchedAt      time.Time `json:"fetched_at,omitzero"`
	OperationCount int       `json:"operation_count"`
	Error          string    `json:"error,omitempty"`
}

// HydrateSchemas brings every registered connection's schema up: the
// stored one when there is one, a fresh introspection otherwise. The
// platform calls it once after wiring the schema store, so a restart
// does not re-read every endpoint and a first start reads them all.
//
// Connections are hydrated concurrently. They are independent network
// reads against different endpoints, and doing them in sequence would
// make startup cost the sum of every upstream's latency rather than the
// slowest one's.
//
// Failures are recorded on the connection and logged, never returned:
// an endpoint that is down at startup must not stop the platform
// serving every other connection. The caller bounds the whole pass with
// the context it passes.
func (t *Toolkit) HydrateSchemas(ctx context.Context) {
	var wg sync.WaitGroup
	for _, name := range t.connectionNames() {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			t.loadOrRefresh(ctx, name)
		}(name)
	}
	wg.Wait()
}

// loadOrRefresh loads a connection's stored schema, falling back to
// introspecting the endpoint when the store holds none.
func (t *Toolkit) loadOrRefresh(ctx context.Context, name string) {
	c, _, ok := t.lookup(name)
	if !ok {
		return
	}
	t.mu.RLock()
	store := t.schemaStore
	t.mu.RUnlock()
	if store != nil {
		stored, err := store.GetSchema(ctx, c.cfg.ConnectionName)
		if err == nil {
			if applyErr := t.applyStored(ctx, c, stored); applyErr == nil {
				return
			}
			slog.Warn("graphql: stored schema did not load; re-reading the endpoint",
				logKeyConnection, logsan.SanitizeForLog(name))
		} else if !errors.Is(err, ErrSchemaNotFound) {
			slog.Warn("graphql: reading the stored schema failed",
				logKeyConnection, logsan.SanitizeForLog(name), logKeyError, err)
		}
	}
	if err := t.RefreshSchema(ctx, name); err != nil {
		slog.Warn("graphql: reading the endpoint's schema failed",
			logKeyConnection, logsan.SanitizeForLog(name), logKeyError, err)
	}
}

// applyStored installs a stored schema on a connection.
func (t *Toolkit) applyStored(ctx context.Context, c *conn, stored StoredSchema) error {
	parsed, err := gqlschema.Load(stored.SDL)
	if err != nil {
		return fmt.Errorf("graphql: the stored schema for %s does not load: %w", stored.Connection, err)
	}
	t.install(ctx, c, parsed, stored.Source, stored.FetchedAt)
	return nil
}

// RefreshSchema reads a connection's schema from its endpoint by
// introspection, stores it, and rebuilds the operation index. It is
// what the admin refresh action calls, and what a connection falls back
// to when nothing is stored.
//
// An endpoint with introspection disabled fails here with a message
// saying so; that message is recorded on the connection and reported by
// SchemaInfo, so the state is a named cause rather than an operation
// index that is silently empty.
func (t *Toolkit) RefreshSchema(ctx context.Context, name string) error {
	c, _, ok := t.lookup(name)
	if !ok {
		return notFound(name)
	}
	res, err := t.execute(ctx, c, graphQLRequest{Query: gqlschema.IntrospectionQuery})
	if err != nil {
		return t.recordSchemaError(c, err)
	}
	if err := introspectionFailure(res); err != nil {
		return t.recordSchemaError(c, err)
	}
	parsed, err := gqlschema.LoadIntrospection(res.body)
	if err != nil {
		return t.recordSchemaError(c, err)
	}
	t.store(ctx, c, parsed, SchemaSourceIntrospection)
	return nil
}

// SetSchema installs a schema an operator supplied, accepting either
// SDL or an introspection result. It is the path for an endpoint that
// disables introspection, where the platform cannot read the schema for
// itself.
func (t *Toolkit) SetSchema(ctx context.Context, name string, payload []byte) error {
	c, _, ok := t.lookup(name)
	if !ok {
		return notFound(name)
	}
	parsed, err := gqlschema.LoadAny(payload)
	if err != nil {
		return t.recordSchemaError(c, err)
	}
	t.store(ctx, c, parsed, SchemaSourceUpload)
	return nil
}

// introspectionFailure turns a transport-level or GraphQL-level refusal
// of the introspection call into an operator-facing error. The
// upstream's own message is carried through: on an endpoint that
// disables introspection it is the sentence that says so.
func introspectionFailure(res *execution) error {
	if res.truncated {
		return errors.New("graphql: the introspection result exceeded this connection's max_response_bytes; raise it or upload the schema")
	}
	if res.status < http.StatusOK || res.status >= http.StatusMultipleChoices {
		return fmt.Errorf("graphql: the endpoint answered HTTP %d to the introspection query: %s",
			res.status, snippet(string(res.body)))
	}
	if res.parsed != nil && len(res.parsed.Errors) > 0 {
		return fmt.Errorf("graphql: the endpoint refused the introspection query: %s",
			res.parsed.Errors[0].Message)
	}
	return nil
}

// store installs a schema and persists it. A store write that fails is
// logged rather than returned: the connection is usable with the schema
// in memory, and refusing the refresh over a storage failure would take
// a working connection down.
func (t *Toolkit) store(ctx context.Context, c *conn, parsed *gqlschema.Schema, source string) {
	now := time.Now().UTC()
	t.install(ctx, c, parsed, source, now)
	t.mu.RLock()
	schemaStore := t.schemaStore
	t.mu.RUnlock()
	if schemaStore == nil {
		return
	}
	err := schemaStore.PutSchema(ctx, StoredSchema{
		Connection: c.cfg.ConnectionName,
		Hash:       parsed.Hash(),
		SDL:        parsed.SDL(),
		Source:     source,
		FetchedAt:  now,
	})
	if err != nil {
		slog.Warn("graphql: persisting the schema failed",
			logKeyConnection, logsan.SanitizeForLog(c.cfg.ConnectionName), logKeyError, err)
	}
}

// install replaces a connection's schema, operation index and vectors
// under one write lock, so a call in flight sees either the old set or
// the new one and never a half-rebuilt index.
func (t *Toolkit) install(ctx context.Context, c *conn, parsed *gqlschema.Schema, source string, at time.Time) {
	ops := gqlschema.Operations(parsed, c.cfg.NamespaceDepth)
	vectors := t.loadVectors(ctx, c.cfg.ConnectionName, parsed.Hash())
	c.schemaMu.Lock()
	c.schema = parsed
	c.operations = ops
	c.vectors = vectors
	c.source = source
	c.fetchedAt = at
	c.schemaErr = ""
	c.schemaMu.Unlock()
}

// loadVectors reads the persisted embeddings for a schema version.
// Absent vectors are not an error: an index that has not run yet leaves
// ranking lexical, which is a working answer rather than a failure.
func (t *Toolkit) loadVectors(ctx context.Context, connection, hash string) map[string][]float32 {
	t.mu.RLock()
	reader := t.vectorReader
	t.mu.RUnlock()
	if reader == nil {
		return nil
	}
	vectors, err := reader.LoadVectors(ctx, connection, hash)
	if err != nil {
		slog.Warn("graphql: reading operation embeddings failed",
			logKeyConnection, logsan.SanitizeForLog(connection), logKeyError, err)
		return nil
	}
	return vectors
}

// ReloadVectors re-reads a connection's persisted embeddings. The
// index-jobs consumer calls it after a successful pass so a connection
// that was ranking lexically starts ranking semantically without a
// restart.
func (t *Toolkit) ReloadVectors(ctx context.Context, name string) {
	c, _, ok := t.lookup(name)
	if !ok {
		return
	}
	c.schemaMu.RLock()
	hash := ""
	if c.schema != nil {
		hash = c.schema.Hash()
	}
	c.schemaMu.RUnlock()
	if hash == "" {
		return
	}
	vectors := t.loadVectors(ctx, c.cfg.ConnectionName, hash)
	c.schemaMu.Lock()
	c.vectors = vectors
	c.schemaMu.Unlock()
}

// recordSchemaError records why a connection has no usable schema and
// returns the error for the caller to report.
func (*Toolkit) recordSchemaError(c *conn, err error) error {
	c.schemaMu.Lock()
	c.schemaErr = err.Error()
	c.schemaMu.Unlock()
	return err
}

// SchemaInfo reports what the platform holds for one connection.
func (t *Toolkit) SchemaInfo(name string) (SchemaInfo, error) {
	c, _, ok := t.lookup(name)
	if !ok {
		return SchemaInfo{}, notFound(name)
	}
	c.schemaMu.RLock()
	defer c.schemaMu.RUnlock()
	info := SchemaInfo{
		Connection:     c.cfg.ConnectionName,
		Source:         c.source,
		FetchedAt:      c.fetchedAt,
		OperationCount: len(c.operations),
		Error:          c.schemaErr,
	}
	if c.schema != nil {
		info.Hash = c.schema.Hash()
	}
	return info, nil
}

// SchemaInfos reports every registered connection's schema state, for
// the admin surface and for the index-jobs source's gap detection.
func (t *Toolkit) SchemaInfos() []SchemaInfo {
	names := t.connectionNames()
	out := make([]SchemaInfo, 0, len(names))
	for _, name := range names {
		if info, err := t.SchemaInfo(name); err == nil {
			out = append(out, info)
		}
	}
	return out
}

// Operations returns a connection's current operation index together
// with the schema hash it belongs to. The index-jobs source reads it to
// build the text it embeds.
func (t *Toolkit) Operations(name string) (ops []gqlschema.Operation, schemaHash string, ok bool) {
	c, _, found := t.lookup(name)
	if !found {
		return nil, "", false
	}
	c.schemaMu.RLock()
	defer c.schemaMu.RUnlock()
	if c.schema == nil {
		return nil, "", false
	}
	return c.operations, c.schema.Hash(), true
}

// ReloadConnection drops and rebuilds a connection so a config change
// takes effect, then brings its schema back up. In-flight OAuth refresh
// tokens persist through the unified connoauth store.
func (t *Toolkit) ReloadConnection(name string) error {
	t.mu.Lock()
	existing, ok := t.connections[name]
	if !ok {
		t.mu.Unlock()
		return notFound(name)
	}
	cfg := existing.cfg
	if existing.client != nil {
		existing.client.CloseIdleConnections()
	}
	delete(t.connections, name)
	t.mu.Unlock()
	if err := t.addParsedConnection(name, cfg); err != nil {
		return err
	}
	t.loadOrRefresh(context.Background(), name)
	return nil
}

// notFound names a connection this toolkit does not hold. One helper
// because the message is the same at every entry point, and an operator
// reading two of them should not find two spellings.
func notFound(name string) error {
	return fmt.Errorf("graphql: %s: %w", name, ErrConnectionNotFound)
}

// snippet bounds an upstream's raw body when it is quoted in an error,
// so an HTML error page does not become the message.
func snippet(s string) string {
	const maxSnippet = 300
	s = strings.TrimSpace(s)
	if len(s) <= maxSnippet {
		return s
	}
	return s[:maxSnippet] + "..."
}

// IndexItems returns the text each of a connection's operations is
// embedded from, keyed by operation id, together with the schema hash
// those operations belong to. It is what the platform's index-jobs
// consumer reads: the toolkit owns what an operation's indexable text
// is, and the consumer owns where the resulting vectors are written.
func (t *Toolkit) IndexItems(name string) (schemaHash string, items map[string]string, ok bool) {
	c, _, found := t.lookup(name)
	if !found {
		return "", nil, false
	}
	c.schemaMu.RLock()
	schema, ops := c.schema, c.operations
	c.schemaMu.RUnlock()
	if schema == nil {
		return "", nil, false
	}
	items = make(map[string]string, len(ops))
	for _, op := range ops {
		items[op.ID] = gqlschema.IndexText(schema, op)
	}
	return schema.Hash(), items, true
}
