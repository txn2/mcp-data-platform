package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestBuildTrinoQueryFunc asserts the closure's error contract. Both failure
// paths return an error, so the observable contract is WHICH stage the failure
// is attributed to: an unknown connection never reaches the query, and must be
// reported as a manager fault rather than as a query fault. Callers surface
// these strings, so conflating them would misdirect an operator to the query
// engine when the real fault is a missing connection name.
func TestBuildTrinoQueryFunc(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	exec := buildTrinoQueryFunc(p)
	if exec == nil {
		t.Fatal("expected non-nil TrinoQueryFunc with a trino toolkit registered")
	}

	t.Run("unknown connection is a manager fault", func(t *testing.T) {
		rows, err := exec(context.Background(), "missing", "SELECT 1")
		if err == nil {
			t.Fatal("expected an error for an unregistered connection name")
		}
		if !strings.Contains(err.Error(), "trino manager:") {
			t.Errorf("error %q missing the 'trino manager:' stage prefix", err.Error())
		}
		if strings.Contains(err.Error(), "trino query:") {
			t.Errorf("error %q attributes a connection-lookup failure to the query stage", err.Error())
		}
		if rows != nil {
			t.Errorf("expected nil rows on error, got %v", rows)
		}
	})

	t.Run("resolved connection reaches the query stage", func(t *testing.T) {
		// The host is unroutable, so the query fails — but it fails AFTER the
		// manager resolved "primary", which is what distinguishes this branch.
		rows, err := exec(context.Background(), "primary", "SELECT 1")
		if err == nil {
			t.Fatal("expected an error against an unreachable trino host")
		}
		if !strings.Contains(err.Error(), "trino query:") {
			t.Errorf("error %q missing the 'trino query:' stage prefix, so the manager lookup did not succeed", err.Error())
		}
		if rows != nil {
			t.Errorf("expected nil rows on error, got %v", rows)
		}
	})
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

// TestBuildDataHubFuncs asserts the closures' contract on failure: each is a
// pass-through to the bound client, so an unreachable upstream must surface the
// client's error rather than be swallowed into a nil-nil result that a caller
// would read as "no such entity".
func TestBuildDataHubFuncs(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	getEntity, getTerm := buildDataHubFuncs(p)
	if getEntity == nil || getTerm == nil {
		t.Fatal("expected non-nil datahub closures with a datahub toolkit registered")
	}

	if _, err := getEntity(context.Background(), "urn:li:dataset:(urn:li:dataPlatform:trino,db.t,PROD)"); err == nil {
		t.Error("getEntity against an unreachable datahub returned nil error, hiding the transport failure")
	}
	if _, err := getTerm(context.Background(), "urn:li:glossaryTerm:pii"); err == nil {
		t.Error("getTerm against an unreachable datahub returned nil error, hiding the transport failure")
	}
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
// produces a bridge, and a non-nil route registrar over it.
func TestBuildDataHubRegistrar(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	bridge := buildDataHubBridge(p)
	if bridge == nil {
		t.Fatal("expected non-nil bridge with a datahub toolkit registered")
	}
	reg := dataHubRegistrar(p, bridge, nil, []string{"admin"})
	if reg == nil {
		t.Fatal("expected non-nil registrar with a datahub toolkit registered")
	}

	// A registrar that mounts nothing is indistinguishable from a nil one at
	// runtime, so assert the catalog route is actually reachable afterwards.
	// Matching the pattern does not invoke the handler, so no request reaches
	// DataHub.
	mux := http.NewServeMux()
	reg(mux)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/datahub/connections", http.NoBody)
	if _, pattern := mux.Handler(req); pattern == "" {
		t.Error("registrar mounted no handler for the datahub connections route")
	}
}

// TestBuildDataHubBridge_NoToolkit covers the nil return when no datahub
// connection is registered (empty bridge), which is what leaves both the REST
// registrar and the catalog labeler unwired.
func TestBuildDataHubBridge_NoToolkit(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
	})
	defer func() { _ = p.Close() }()
	if buildDataHubBridge(p) != nil {
		t.Error("expected nil bridge without a datahub toolkit")
	}
}

// TestRegisterEnrichmentSources asserts that live trino and datahub toolkits
// each land in the registry under the name enrichment rules reference them by.
// Registering under any other name makes every rule that names the source fail
// at evaluation time with "source not registered".
func TestRegisterEnrichmentSources(t *testing.T) {
	p := newToolkitPlatform(t)
	defer func() { _ = p.Close() }()

	reg := enrichment.NewSourceRegistry()
	registerEnrichmentSources(p, reg)

	for _, name := range []string{enrichment.SourceTrino, enrichment.SourceDataHub} {
		src, ok := reg.Get(name)
		if !ok {
			t.Errorf("source %q not registered; rules naming it would fail at evaluation", name)
			continue
		}
		if src.Name() != name {
			t.Errorf("source registered under %q reports Name() = %q", name, src.Name())
		}
	}
}

// TestRegisterEnrichmentSources_NoToolkits asserts the documented behavior when
// the toolkits are absent: no source is registered, rather than a source that
// fails on dispatch.
func TestRegisterEnrichmentSources_NoToolkits(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
	})
	defer func() { _ = p.Close() }()

	reg := enrichment.NewSourceRegistry()
	registerEnrichmentSources(p, reg)

	for _, name := range []string{enrichment.SourceTrino, enrichment.SourceDataHub} {
		if _, ok := reg.Get(name); ok {
			t.Errorf("source %q registered without its toolkit", name)
		}
	}
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

	// The unmounted halves of this gate (portal disabled in config, assets
	// unavailable) are covered by TestMountPortalUI_Disabled and
	// TestMountPortalUI_NoAssets.
	if got := mountedPattern(mux, "/portal/"); got == "" {
		t.Error("portal UI enabled but no handler mounted on /portal/")
	}
}
