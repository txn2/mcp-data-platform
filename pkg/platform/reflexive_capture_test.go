package platform

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

func TestReflexiveURNMapping(t *testing.T) {
	p := &Platform{config: &Config{}}
	p.config.Query.URNMapping = URNMappingConfig{
		Platform:       "trino",
		CatalogMapping: map[string]string{"raw": "warehouse"},
	}

	// No connection sources: falls back to the query-provider mapping.
	platform, mapping := p.reflexiveURNMapping("primary")
	if platform != "trino" || mapping["raw"] != "warehouse" {
		t.Errorf("fallback mapping = %q/%v", platform, mapping)
	}

	// A known connection resolves to its own platform + catalog mapping.
	p.connectionSources = NewConnectionSourceMap()
	p.connectionSources.Add(ConnectionSource{
		Kind:              "trino",
		Name:              "pg",
		DataHubSourceName: "postgres",
		CatalogMapping:    map[string]string{"rdbms": "warehouse"},
	})
	platform, mapping = p.reflexiveURNMapping("pg")
	if platform != "postgres" || mapping["rdbms"] != "warehouse" {
		t.Errorf("per-connection mapping = %q/%v", platform, mapping)
	}

	// An unknown connection falls back to the query-provider mapping.
	if platform, _ := p.reflexiveURNMapping("nope"); platform != "trino" {
		t.Errorf("unknown connection should fall back, got %q", platform)
	}
}

func TestReflexivePersonaAllowsTool(t *testing.T) {
	p := &Platform{config: &Config{}}
	if p.reflexivePersonaAllowsTool() != nil {
		t.Error("no authorizer should yield a nil predicate (no persona gating)")
	}
}

func TestAddReflexiveCaptureMiddleware_Gating(t *testing.T) {
	newP := func() *Platform {
		tk, _ := memorykit.New("test", nil, nil)
		p := &Platform{config: &Config{}}
		p.memoryToolkit = tk
		p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
		return p
	}

	t.Run("enabled by default wires the tracker", func(t *testing.T) {
		p := newP()
		p.addReflexiveCaptureMiddleware()
		if p.reflexiveErrors == nil {
			t.Fatal("expected reflexive tracker to be constructed")
		}
		p.reflexiveErrors.Stop()
	})

	t.Run("explicitly disabled is a no-op", func(t *testing.T) {
		p := newP()
		p.config.Knowledge.ReflexiveCapture.Enabled = new(false)
		p.addReflexiveCaptureMiddleware()
		if p.reflexiveErrors != nil {
			t.Error("disabled config should not construct a tracker")
		}
	})

	t.Run("no memory toolkit is a no-op", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
		p.addReflexiveCaptureMiddleware()
		if p.reflexiveErrors != nil {
			t.Error("missing memory toolkit should not construct a tracker")
		}
	})
}
