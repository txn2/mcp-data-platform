package platform

import (
	"database/sql"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	_ "github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/platform/memorylayer"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

func TestReflexiveURNMapping(t *testing.T) {
	reg := registry.NewRegistry()
	if err := reg.Register(&mockToolkit{kind: "trino", name: "primary", tools: []string{"trino_query"}}); err != nil {
		t.Fatal(err)
	}
	p := &Platform{toolkitRegistry: reg, config: &Config{}}

	// The table is named as the catalog names it: no catalog mapping applies.
	if got, want := p.datasetURNFor("trino", "primary", "raw", "sch", "tbl"), "mcp:dataset:(trino,primary,raw.sch.tbl)"; got != want {
		t.Errorf("URN = %q, want %q", got, want)
	}
	// An unknown connection names no dataset.
	if got := p.datasetURNFor("trino", "nope", "raw", "sch", "tbl"); got != "" {
		t.Errorf("unknown connection should name nothing, got %q", got)
	}
	// An unqualified name the connection has no default for names no dataset.
	if got := p.datasetURNFor("trino", "primary", "", "sch", "tbl"); got != "" {
		t.Errorf("unqualified name should name nothing, got %q", got)
	}
}

// TestReflexiveURNMapping_SharedNameAcrossKinds proves a Trino table's
// reference names the Trino connection even where the deployment carries that
// connection name under another kind too, and answers the same on every call
// (#1384).
func TestReflexiveURNMapping_SharedNameAcrossKinds(t *testing.T) {
	reg := registry.NewRegistry()
	for _, kind := range []string{"trino", "s3"} {
		if err := reg.Register(&mockToolkit{kind: kind, name: "acme", tools: []string{kind + "_x"}}); err != nil {
			t.Fatal(err)
		}
	}
	p := &Platform{toolkitRegistry: reg, config: &Config{}}
	const want = "mcp:dataset:(trino,acme,warehouse.public.regions)"
	for range 20 {
		if got := p.datasetURNFor("trino", "acme", "warehouse", "public", "regions"); got != want {
			t.Fatalf("URN = %q, want %q", got, want)
		}
	}
	if got, want := p.datasetURNFor("s3", "acme", "warehouse", "public", "regions"), "mcp:dataset:(s3,acme,warehouse.public.regions)"; got != want {
		t.Errorf("s3 URN = %q, want %q", got, want)
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
		// A non-connecting *sql.DB is enough: memorylayer.New builds the store
		// wrapper and toolkit without touching the database, so Toolkit() is
		// non-nil (the reflexive-capture gate's precondition).
		db, err := sql.Open("postgres", "postgres://localhost:5432/test?sslmode=disable")
		if err != nil {
			t.Fatalf("open dummy db: %v", err)
		}
		h, err := memorylayer.New(db, nil, memorylayer.Config{ToolkitName: "test"})
		if err != nil {
			t.Fatalf("build memory handle: %v", err)
		}
		p := &Platform{config: &Config{}}
		p.memory = h
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
