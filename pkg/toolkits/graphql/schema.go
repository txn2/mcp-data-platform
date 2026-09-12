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
	"github.com/txn2/mcp-data-platform/internal/useragent"
)

// SchemaInfo is what an operator surface reports about a connection's
// schema: which version the platform holds, where it came from, when,
// how many operations it exposes, and why the last read failed when it
// did. A failed read leaves the schema the connection held in place, so
// Error beside a hash is a schema that survived a re-read the endpoint
// refused; Error with no hash is a connection that has never had one.
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
	if loaded, _ := t.loadStored(ctx, c); loaded {
		return
	}
	if err := t.RefreshSchema(ctx, name); err != nil {
		slog.Warn("graphql: reading the endpoint's schema failed",
			logKeyConnection, logsan.SanitizeForLog(name), logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// readOrLoadStored brings a connection's schema up on register and on
// reconcile: the endpoint is read, and when the read fails the schema
// the store holds is installed with the refusal the store records beside
// it, which is this read's when this instance held the stored version. The
// store is the source of truth across replicas, so a replica whose own
// read fails serves what another replica read or was handed rather
// than nothing (#1676). A deployment without a store keeps what the
// connection already held, which is the schema it had before a
// reconcile.
func (t *Toolkit) readOrLoadStored(ctx context.Context, name string) {
	err := t.RefreshSchema(ctx, name)
	if err == nil {
		return
	}
	slog.Warn("graphql: reading the endpoint's schema failed",
		logKeyConnection, logsan.SanitizeForLog(name), logKeyError, logsan.SanitizeForLog(err.Error()))
	c, _, ok := t.lookup(name)
	if !ok {
		return
	}
	// A store with nothing for the connection leaves what it held; any
	// other failure is logged where it happens.
	_, _ = t.loadStored(ctx, c)
}

// LoadStoredSchema installs the schema the store holds for a connection,
// with the refusal the store records beside it, replacing whatever this
// instance holds and whatever failure it recorded. It is how a schema
// stored by another replica, an upload or a re-read, and a re-read
// another replica was refused, reach this one: the reload bus calls it
// when a peer announces one. The store's answer is final because it is
// what every replica shares; this instance's own last read is superseded
// by it. An instance with no store has nothing to load and is left as it
// is.
func (t *Toolkit) LoadStoredSchema(ctx context.Context, name string) error {
	c, _, ok := t.lookup(name)
	if !ok {
		return notFound(name)
	}
	_, err := t.loadStored(ctx, c)
	return err
}

// loadStored installs the stored schema on a connection when the store
// holds one, with the refusal the store records beside it. The store's
// answer replaces this instance's own recorded failure: that failure was
// recorded there if it was about the version the store holds, and a
// failure about an older version, or about no schema at all, is not a
// fact about this one. The first result reports a schema installed; the
// error is the store's, or the stored schema's when it does not parse.
func (t *Toolkit) loadStored(ctx context.Context, c *conn) (bool, error) {
	t.mu.RLock()
	store := t.schemaStore
	t.mu.RUnlock()
	if store == nil {
		return false, nil
	}
	stored, err := store.GetSchema(ctx, c.cfg.ConnectionName)
	if err != nil {
		if !errors.Is(err, ErrSchemaNotFound) {
			slog.Warn("graphql: reading the stored schema failed",
				logKeyConnection, logsan.SanitizeForLog(c.cfg.ConnectionName), logKeyError, logsan.SanitizeForLog(err.Error()))
		}
		return false, fmt.Errorf("graphql: reading the stored schema for %s: %w", c.cfg.ConnectionName, err)
	}
	parsed, err := gqlschema.Load(stored.SDL)
	if err != nil {
		slog.Warn("graphql: the stored schema does not load",
			logKeyConnection, logsan.SanitizeForLog(c.cfg.ConnectionName), logKeyError, logsan.SanitizeForLog(err.Error()))
		return false, fmt.Errorf("graphql: the stored schema for %s does not load: %w", stored.Connection, err)
	}
	t.install(ctx, c, schemaVersion{schema: parsed, source: stored.Source, fetchedAt: stored.FetchedAt, schemaErr: stored.ReadError})
	return true, nil
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
	held := c.heldVersion()
	res, err := t.execute(ctx, c, graphQLRequest{Query: gqlschema.IntrospectionQuery})
	if err != nil {
		return t.recordSchemaError(ctx, c, held, err)
	}
	if err := introspectionFailure(res); err != nil {
		return t.recordSchemaError(ctx, c, held, err)
	}
	parsed, err := gqlschema.LoadIntrospection(res.body)
	if err != nil {
		return t.recordSchemaError(ctx, c, held, err)
	}
	t.store(ctx, c, parsed, SchemaSourceIntrospection)
	return nil
}

// SetSchema installs a schema an operator supplied, accepting either
// SDL or an introspection result. It is the path for an endpoint that
// disables introspection, where the platform cannot read the schema for
// itself. A payload that does not parse is the operator's input and is
// returned to them; it is not recorded on the connection, whose state is
// whatever it held before the attempt.
func (t *Toolkit) SetSchema(ctx context.Context, name string, payload []byte) error {
	c, _, ok := t.lookup(name)
	if !ok {
		return notFound(name)
	}
	parsed, err := gqlschema.LoadAny(payload)
	if err != nil {
		return fmt.Errorf("graphql: %w", err)
	}
	t.store(ctx, c, parsed, SchemaSourceUpload)
	return nil
}

// introspectionFailure turns a transport-level or GraphQL-level refusal
// of the introspection call into an operator-facing error. The
// upstream's own message is carried through: on an endpoint that
// disables introspection it is the sentence that says so.
//
// A 403 carrying an HTML page is the one refusal whose body says
// nothing: it is a web application firewall's block page, and the
// request attribute such a rule most often keys on is the User-Agent
// (#1679). That case names the User-Agent the request went out with and
// the connection key that changes it, in place of the page.
func introspectionFailure(res *execution) error {
	if res.truncated {
		return errors.New("graphql: the introspection result exceeded this connection's max_response_bytes; raise it or upload the schema")
	}
	if res.status == http.StatusForbidden && looksLikeHTML(res.body) {
		return fmt.Errorf("graphql: the endpoint answered HTTP 403 to the introspection query with an HTML page rather than a GraphQL response; "+
			"the request's User-Agent was %q, which a web application firewall may refuse: "+
			"set static_headers {%q: \"<another value>\"} on the connection to send a different one",
			res.userAgent, useragent.Header)
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
	// The store keeps fetched_at at microsecond precision (TIMESTAMPTZ),
	// and what this instance holds must be what every other instance
	// reads back, so the time is written at that precision from the
	// start rather than differing by the nanoseconds the column drops.
	now := time.Now().UTC().Truncate(time.Microsecond)
	t.install(ctx, c, schemaVersion{schema: parsed, source: source, fetchedAt: now})
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

// schemaVersion is one schema as a connection installs it: the parsed
// schema, where it came from and when, and what the connection reports
// beside it. schemaErr is empty for a schema that is the answer, and the
// read's failure for one that stood in for a read that failed.
type schemaVersion struct {
	schema    *gqlschema.Schema
	source    string
	fetchedAt time.Time
	schemaErr string
}

// install replaces a connection's schema, operation index and vectors
// under one write lock, so a call in flight sees either the old set or
// the new one and never a half-rebuilt index.
func (t *Toolkit) install(ctx context.Context, c *conn, v schemaVersion) {
	ops := gqlschema.Operations(v.schema, c.cfg.NamespaceDepth)
	vectors := t.loadVectors(ctx, c.cfg.ConnectionName, v.schema.Hash())
	c.schemaMu.Lock()
	c.schema = v.schema
	c.operations = ops
	c.vectors = vectors
	c.source = v.source
	c.fetchedAt = v.fetchedAt
	c.schemaErr = v.schemaErr
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

// recordSchemaError records why a connection's last read failed and
// returns the error for the caller to report. The schema the connection
// holds, if any, is left in place: the failure is reported beside it, on
// this instance and in the store, so a replica that did not run the read
// and a restart report it too (#1703).
//
// Both are recorded only beside held, the version this instance held when
// the read began. A newer schema installed while the read was in flight,
// a peer's upload arriving on the reload bus, is not what the endpoint
// refused, and reporting the refusal beside it here while the store and
// every other replica report none is the disagreement #1703 was filed
// on. A store write that fails is logged rather than returned, for the
// reason store gives.
func (t *Toolkit) recordSchemaError(ctx context.Context, c *conn, held StoredSchema, err error) error {
	c.schemaMu.Lock()
	now := c.versionLocked()
	current := now.Hash == held.Hash && now.FetchedAt.Equal(held.FetchedAt)
	if current {
		c.schemaErr = err.Error()
	}
	c.schemaMu.Unlock()
	if !current {
		slog.Info("graphql: a refused read was superseded by a schema installed while it ran",
			logKeyConnection, logsan.SanitizeForLog(c.cfg.ConnectionName), logKeyError, logsan.SanitizeForLog(err.Error()))
		return err
	}
	t.mu.RLock()
	schemaStore := t.schemaStore
	t.mu.RUnlock()
	if schemaStore == nil || held.Hash == "" {
		return err
	}
	held.ReadError = err.Error()
	if werr := schemaStore.RecordReadError(ctx, held); werr != nil {
		slog.Warn("graphql: recording the refused read failed",
			logKeyConnection, logsan.SanitizeForLog(c.cfg.ConnectionName), logKeyError, logsan.SanitizeForLog(werr.Error()))
	}
	return err
}

// heldVersion names the schema version a connection holds, by the hash
// and read time the store keys it on. Empty when it holds none.
func (c *conn) heldVersion() StoredSchema {
	c.schemaMu.RLock()
	defer c.schemaMu.RUnlock()
	return c.versionLocked()
}

// versionLocked is heldVersion for a caller already holding schemaMu.
func (c *conn) versionLocked() StoredSchema {
	v := StoredSchema{Connection: c.cfg.ConnectionName, FetchedAt: c.fetchedAt}
	if c.schema != nil {
		v.Hash = c.schema.Hash()
	}
	return v
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

// notFound names a connection this toolkit does not hold. One helper
// because the message is the same at every entry point, and an operator
// reading two of them should not find two spellings.
func notFound(name string) error {
	return fmt.Errorf("graphql: %s: %w", name, ErrConnectionNotFound)
}

// looksLikeHTML reports a body that is an HTML document rather than a
// GraphQL response: a block page, a proxy's error page. It reads the
// opening of the body only, which is where a document declares itself.
func looksLikeHTML(body []byte) bool {
	const opening = 1024
	head := body
	if len(head) > opening {
		head = head[:opening]
	}
	lower := strings.ToLower(string(head))
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html")
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
