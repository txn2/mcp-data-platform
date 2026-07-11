package httpserver

import (
	"context"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

// newToolkitPlatform builds a memory-backed platform with live trino, datahub,
// and apigateway toolkits registered. The toolkits construct their clients
// eagerly but do not connect, so the wiring helpers under test exercise their
// real (non-early-return) paths without needing a live upstream.
func newToolkitPlatform(t *testing.T) *platform.Platform {
	t.Helper()
	return newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
		Toolkits: map[string]any{
			"trino": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"primary": map[string]any{
						"host": "trino.example.com", "port": 443,
						"catalog": "iceberg", "user": "u",
					},
				},
			},
			"datahub": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"primary": map[string]any{
						"url": "https://datahub.example.com/api/graphql", "token": "t",
					},
				},
			},
			"api": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"acme": map[string]any{"base_url": "https://api.example.com", "auth_mode": "none"},
				},
			},
		},
	})
}

// TestBuildTrinoQueryFunc exercises the closure bound to the live trino manager.
// The query attempt fails (no reachable server) but the closure body still runs.
func TestBuildTrinoQueryFunc(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	exec := buildTrinoQueryFunc(p)
	if exec == nil {
		t.Fatal("expected non-nil TrinoQueryFunc with a trino toolkit registered")
	}
	// The closure runs against an unreachable host, so an error is expected;
	// the point is to execute the manager-lookup + query body.
	_, _ = exec(context.Background(), "primary", "SELECT 1")
	// Unknown connection exercises the manager-error branch.
	_, _ = exec(context.Background(), "missing", "SELECT 1")
}

// TestBuildTrinoQueryFunc_NoToolkit covers the nil return when no trino toolkit
// is registered.
func TestBuildTrinoQueryFunc_NoToolkit(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
	})
	defer func() { _ = p.Close() }()
	if buildTrinoQueryFunc(p) != nil {
		t.Error("expected nil TrinoQueryFunc without a trino toolkit")
	}
}

// TestBuildDataHubFuncs exercises the get-entity and get-glossary-term closures
// bound to the live datahub client.
func TestBuildDataHubFuncs(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	getEntity, getTerm := buildDataHubFuncs(p)
	if getEntity == nil || getTerm == nil {
		t.Fatal("expected non-nil datahub closures with a datahub toolkit registered")
	}
	_, _ = getEntity(context.Background(), "urn:li:dataset:(urn:li:dataPlatform:trino,db.t,PROD)")
	_, _ = getTerm(context.Background(), "urn:li:glossaryTerm:pii")
}

// TestBuildDataHubFuncs_NoToolkit covers the nil,nil return.
func TestBuildDataHubFuncs_NoToolkit(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
	})
	defer func() { _ = p.Close() }()
	if ge, gt := buildDataHubFuncs(p); ge != nil || gt != nil {
		t.Error("expected nil datahub closures without a datahub toolkit")
	}
}

// TestBuildDataHubRegistrar covers the happy path: a live datahub toolkit
// produces a non-nil route registrar over the bridge.
func TestBuildDataHubRegistrar(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	reg := buildDataHubRegistrar(p, nil, []string{"admin"})
	if reg == nil {
		t.Fatal("expected non-nil registrar with a datahub toolkit registered")
	}
	// The registrar mounts routes on a mux without contacting DataHub.
	reg(http.NewServeMux())
}

// TestBuildDataHubRegistrar_NoToolkit covers the nil return when no datahub
// connection is registered (empty bridge).
func TestBuildDataHubRegistrar_NoToolkit(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
	})
	defer func() { _ = p.Close() }()
	if buildDataHubRegistrar(p, nil, nil) != nil {
		t.Error("expected nil registrar without a datahub toolkit")
	}
}

// TestRegisterEnrichmentSources covers source registration for both the trino
// and datahub adapters when their toolkits are live.
func TestRegisterEnrichmentSources(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	reg := enrichment.NewSourceRegistry()
	registerEnrichmentSources(p, reg)
}

// TestWireEnrichmentEngine_NoStore covers the nil return when no enrichment
// store is available (memory mode).
func TestWireEnrichmentEngine_NoStore(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()
	if wireEnrichmentEngine(p) != nil {
		t.Error("expected nil engine without an enrichment store")
	}
}

// TestBuildOAuthKindHandlers covers the api-gateway switch arm.
func TestBuildOAuthKindHandlers(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	handlers := buildOAuthKindHandlers(p)
	if len(handlers) == 0 {
		t.Error("expected at least the api-gateway OAuth kind handler")
	}
}

// TestStartConnOAuthRefresher_NoStore covers the early return when the shared
// connoauth store is not wired (memory mode).
func TestStartConnOAuthRefresher_NoStore(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()
	startConnOAuthRefresher(p) // no ConnOAuthStore → early return, must not panic
}

// TestBuildPersonaResolver exercises the resolver closure for both a matched
// and an unmatched role set.
func TestBuildPersonaResolver(t *testing.T) {
	pr := persona.NewRegistry()
	_ = pr.Register(&persona.Persona{Name: "analyst", Roles: []string{"dp_analyst"}})
	tr := registry.NewRegistry()

	resolve := buildPersonaResolver(pr, tr)
	if resolve == nil {
		t.Fatal("expected non-nil resolver")
	}
	if info := resolve([]string{"dp_analyst"}); info == nil || info.Name != "analyst" {
		t.Errorf("expected analyst persona info, got %+v", info)
	}
	if info := resolve([]string{"dp_unknown"}); info != nil {
		t.Errorf("expected nil for unmatched roles, got %+v", info)
	}
}

// TestWirePortalOptionalDeps drives the optional-dependency wiring against a
// memory-backed platform: the nil-guarded branches are skipped, and the
// persona-resolver and datahub-registrar paths run.
func TestWirePortalOptionalDeps(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	var deps portal.Deps
	wirePortalOptionalDeps(&deps, p)
	if deps.PersonaResolver == nil {
		t.Error("expected a persona resolver to be wired from the persona registry")
	}
	if deps.DataHubRegistrar == nil {
		t.Error("expected a datahub registrar to be wired from the datahub toolkit")
	}
}

// TestMountPortalUI_AssetsAvailable covers the mount happy path when the config
// gate is on and assets are reported available (the SPA handler is registered).
func TestMountPortalUI_AssetsAvailable(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Portal: platform.PortalConfig{Enabled: new(true)},
	})
	defer func() { _ = p.Close() }()

	// ui.Available() is false with no embedded build, so pass the gate
	// explicitly to exercise the registration path; ui.Handler() serves a
	// not-found page without embedded assets rather than panicking.
	mux := http.NewServeMux()
	mountPortalUI(mux, p, true)
}
