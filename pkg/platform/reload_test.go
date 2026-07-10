package platform

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// fakeConnStore returns a fixed config for every Get so the reloader's
// AddConnection branch is exercised.
type fakeConnStore struct{ cfg map[string]any }

func (fakeConnStore) List(context.Context) ([]ConnectionInstance, error) { return nil, nil }
func (f fakeConnStore) Get(_ context.Context, kind, name string) (*ConnectionInstance, error) {
	return &ConnectionInstance{Kind: kind, Name: name, Config: f.cfg}, nil
}
func (fakeConnStore) Set(context.Context, ConnectionInstance) error { return nil }
func (fakeConnStore) Delete(context.Context, string, string) error  { return nil }
func (fakeConnStore) Persistent() bool                              { return false }

// fakeAPIKeyStore returns a single DB key so reloadAPIKeyLocal's
// definition-to-APIKey mapping loop is exercised.
type fakeAPIKeyStore struct{}

func (fakeAPIKeyStore) List(context.Context) ([]APIKeyDefinition, error) {
	return []APIKeyDefinition{{Name: "db-key", KeyHash: "$2a$hash", Roles: []string{"analyst"}}}, nil
}
func (fakeAPIKeyStore) Set(context.Context, APIKeyDefinition) error { return nil }
func (fakeAPIKeyStore) Delete(context.Context, string) error        { return nil }

// TestPlatform_ReloadWiring exercises the platform-level reload surface: the
// sessions-handle assembly (memory fallback, no db) that carries the injected
// reload handlers, the connection/catalog reloaders against a live api-gateway
// toolkit, the publish delegators, and shutdown. The bus-core behavior (cross-
// replica dispatch, self-origin skip) is covered in the sessionsync package.
func TestPlatform_ReloadWiring(t *testing.T) {
	reg := registry.NewRegistry()
	apiTk := apigatewaykit.New("api")
	if err := reg.Register(apiTk); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	p := &Platform{
		config:          &Config{},
		toolkitRegistry: reg,
		connectionStore: fakeConnStore{cfg: map[string]any{"base_url": "https://x"}},
		personaStore:    &personastore.NoopStore{},
		apiKeyStore:     fakeAPIKeyStore{},
		apiKeyAuth:      auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{}),
	}

	if err := p.initSessions(&Options{}); err != nil { // no db -> in-memory store + broadcaster
		t.Fatalf("initSessions: %v", err)
	}
	if p.sessions == nil || p.sessions.Broadcaster() == nil {
		t.Fatal("initSessions did not assemble the session/reload layer")
	}

	// Reloaders against the live toolkit (rebuild from the fake store).
	p.reloadConnectionLocal("api", "c1", ReloadUpsert.String())
	if !apiTk.HasConnection("c1") {
		t.Error("reloadConnectionLocal did not add the connection")
	}
	// A delete op removes the connection from the real toolkit (end-to-end
	// through Platform + the live apigateway toolkit, not a fake).
	p.reloadConnectionLocal("api", "c1", ReloadDelete.String())
	if apiTk.HasConnection("c1") {
		t.Error("reloadConnectionLocal(delete) did not remove the connection")
	}
	p.reloadConnectionLocal("mcp", "ignored", ReloadUpsert.String()) // wrong kind: no-op, exercises the skip
	p.reloadCatalogLocal("cat-1")                                    // ReloadConnectionsByCatalog on the api toolkit
	p.reloadPersonaLocal()                                           // reconcile personas from store
	p.reloadAPIKeyLocal()                                            // re-sync api keys from store

	// Publish delegators (memory bus; no subscriber needed for coverage).
	p.PublishConnectionReload("api", "c1", ReloadUpsert)
	p.PublishCatalogReload("cat-1")
	p.PublishPersonaReload()
	p.PublishAPIKeyReload()

	if err := p.sessions.Close(); err != nil { // cancel subscriber + close broadcaster/store
		t.Fatalf("sessions close: %v", err)
	}

	// Publish after close must be safe (broadcaster closed).
	p.PublishConnectionReload("api", "c1", ReloadDelete)
}
