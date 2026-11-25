// Package toolschema answers what a registered tool's advertised input schema
// is, read from the MCP server's own tools/list.
//
// The error contract asks it when a call is refused because its arguments do
// not match the tool's schema (#2811): the refusal lists the parameters the
// tool accepts, and only the server knows them for every tool. mcp-trino
// registers its tools upstream, and a gateway connection's tools come from
// another server, so no registration helper the platform owns sees them all.
// The schema read is the one a client is shown, list decorators included, so
// a parameter the platform injects (session_id, purpose) is listed as
// accepted, which it is.
//
// The listing is taken on demand over an in-memory session and cached, so a
// refusal costs one listing at most once per TTL, and a tool added after the
// last listing (a gateway connection) is found by listing again on a miss.
package toolschema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/inprocess"
)

// clientName is what the listing connects as. tools/list is not a tool call,
// so it appears in no audit row.
const clientName = "tool-schema-lookup"

// DefaultTTL is how long a listing answers before the next lookup lists again.
const DefaultTTL = time.Minute

// Lookup reads input schemas from one server's tools/list.
type Lookup struct {
	server *mcp.Server
	ttl    time.Duration
	now    func() time.Time
	list   func(ctx context.Context) (map[string]map[string]any, error)

	mu       sync.Mutex
	schemas  map[string]map[string]any
	listedAt time.Time
}

// New returns a lookup over server's listing, cached for ttl (DefaultTTL when
// zero or negative).
func New(server *mcp.Server, ttl time.Duration) *Lookup {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	l := &Lookup{server: server, ttl: ttl, now: time.Now}
	l.list = l.listServer
	return l
}

// InputSchema returns the named tool's advertised input schema as decoded
// JSON, or nil when the server holds no such tool or cannot be listed. A miss
// in a fresh listing lists again once, so a tool registered since is found.
func (l *Lookup) InputSchema(ctx context.Context, name string) map[string]any {
	if l == nil || name == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fresh := l.schemas != nil && l.now().Sub(l.listedAt) < l.ttl
	if fresh {
		if s, ok := l.schemas[name]; ok {
			return s
		}
	}
	schemas, err := l.list(ctx)
	if err != nil {
		return nil
	}
	l.schemas, l.listedAt = schemas, l.now()
	return schemas[name]
}

// listServer reads tools/list over an in-memory transport. The session
// carries no credential, so the persona half of the visibility filter is
// skipped; a schema is the same whoever is shown it.
func (l *Lookup) listServer(ctx context.Context) (map[string]map[string]any, error) {
	if l.server == nil {
		return nil, errors.New("toolschema: no server")
	}
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := l.server.Connect(ctx, t1, nil)
	if err != nil {
		return nil, fmt.Errorf("server connect: %w", err)
	}
	untrack := inprocess.Track(serverSession)
	defer untrack()
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "v1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("client connect: %w", err)
	}
	defer func() { _ = session.Close() }()

	schemas := make(map[string]map[string]any)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		if s := decode(tool.InputSchema); s != nil {
			schemas[tool.Name] = s
		}
	}
	return schemas, nil
}

// decode turns an input schema of whatever Go type the client decoded it into
// a JSON object, or nil when it is not one.
func decode(schema any) map[string]any {
	if m, ok := schema.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}
