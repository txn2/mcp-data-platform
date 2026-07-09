package platform

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

func TestIsHTTPTransport(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"http":  true,
		"sse":   true,
		"stdio": false,
		"":      false,
		"grpc":  false,
	}
	for transport, want := range cases {
		if got := isHTTPTransport(transport); got != want {
			t.Errorf("isHTTPTransport(%q) = %v; want %v", transport, got, want)
		}
	}
}

// TestWireRuntime_TransportGating proves WireRuntime's transport gate: the
// api-gateway mem budget wires for BOTH transports (the OOM guard is
// process-wide, #535), while the gateway integrations and the admin
// self-connection are HTTP-only. A stdio boot must not seed the self-connection;
// an HTTP boot must.
func TestWireRuntime_TransportGating(t *testing.T) {
	t.Parallel()

	// newP builds a platform with an api-gateway toolkit that already has a
	// catalog store, so the admin self-connection's prerequisites are met
	// independent of the DB-backed wiring — this test isolates the transport
	// gate, not the gateway-before-admin ordering (covered separately below).
	newP := func(t *testing.T) (*Platform, *apigatewaykit.Toolkit) {
		t.Helper()
		tk := apigatewaykit.New("api")
		tk.SetCatalogStore(apicatalog.NewMemoryStore())
		reg := registry.NewRegistry()
		require.NoError(t, reg.Register(tk))
		lc := NewLifecycle()
		// Started, mirroring production: the factory calls Platform.Start
		// before the entry point runs the runtime wiring, so the admin
		// self-connection's OnStart hook late-fires synchronously.
		require.NoError(t, lc.Start(context.Background()))
		return &Platform{toolkitRegistry: reg, lifecycle: lc, config: &Config{}}, tk
	}

	t.Run("stdio skips gateway and admin", func(t *testing.T) {
		t.Parallel()
		p, tk := newP(t)
		p.WireRuntime(RuntimeConfig{Transport: "stdio", Address: ":8080"})
		require.NotNil(t, p.apiMemBudget, "mem budget must wire for stdio too")
		require.False(t, tk.HasConnection(adminSelfConnectionName),
			"stdio must not seed the admin self-connection")
	})

	t.Run("http wires gateway and admin", func(t *testing.T) {
		t.Parallel()
		p, tk := newP(t)
		p.WireRuntime(RuntimeConfig{Transport: "http", Address: ":8080"})
		require.NotNil(t, p.apiMemBudget)
		require.True(t, tk.HasConnection(adminSelfConnectionName),
			"http must seed the admin self-connection")
	})
}

// TestWireRuntime_GatewayIntegrationsBeforeAdminSeed is the assembly-order
// regression guard for #756/#854. It exercises the real dependency the old
// main.go call order encoded only implicitly: the admin self-connection seed
// reads the api-catalog store that WireGatewayIntegrations wires from the DB, so
// WireGatewayIntegrations MUST run first. The toolkit starts with NO catalog
// store, so the only way the seed can register the connection is if
// WireGatewayIntegrations wired the store before WireAdminSelfConnection ran.
//
// Reorder those two steps in WireRuntime (the exact "compiles, unit tests pass,
// fails at runtime" trap #756 calls out) and the seed finds a nil catalog store,
// no-ops, and this test fails on both the missing connection and the unmet
// query expectations.
func TestWireRuntime_GatewayIntegrationsBeforeAdminSeed(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	tk := apigatewaykit.New("api") // deliberately NO catalog store pre-set
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))
	lc := NewLifecycle()
	require.NoError(t, lc.Start(context.Background()))

	// nil embedder ⇒ the embed-jobs queue skips (no worker goroutine); the
	// DB-backed catalog store still wires. So the seed's only DB traffic is
	// the catalog header + spec upsert below.
	p := &Platform{toolkitRegistry: reg, lifecycle: lc, config: &Config{}, db: db}

	// Seed queries, in order: probe the catalog (absent → create it), upsert
	// the embedded admin spec, then AddConnection lists the catalog's specs
	// while loading the new connection (empty is fine — the connection still
	// registers).
	mock.ExpectQuery(`SELECT .* FROM api_catalogs WHERE id`).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO api_catalogs`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO api_catalog_specs`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT .* FROM api_catalog_specs WHERE catalog_id`).
		WillReturnRows(sqlmock.NewRows([]string{
			"spec_name", "content", "source_kind", "source_url", "etag",
			"base_path", "title", "description", "last_fetched_at",
			"created_at", "updated_at", "operation_count",
		}))

	p.WireRuntime(RuntimeConfig{Transport: "http", Address: ":8080"})

	require.True(t, tk.HasConnection(adminSelfConnectionName),
		"admin self-connection must register, proving WireGatewayIntegrations wired the catalog store before the seed ran")
	require.NoError(t, mock.ExpectationsWereMet())
}
