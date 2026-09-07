package platform

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/portalstore"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// graphQLRegistry builds a registry holding one graphql toolkit whose
// endpoint is never dialed: what these tests exercise is the assembly of
// the dependencies the facade hands over, not the schema read.
func graphQLRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": "https://unreached.invalid/graphql"})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.ConnectionName = "erp"
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: "erp", Instances: map[string]graphqlkit.Config{"erp": cfg},
	})
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register: %v", err)
	}
	return reg
}

func TestGraphQLToolkitsReadsTheLiveRegistry(t *testing.T) {
	p := &Platform{toolkitRegistry: graphQLRegistry(t)}
	if got := p.GraphQLToolkits(); len(got) != 1 {
		t.Errorf("toolkits = %+v", got)
	}
	empty := &Platform{toolkitRegistry: registry.NewRegistry()}
	if got := empty.GraphQLToolkits(); len(got) != 0 {
		t.Errorf("toolkits = %+v", got)
	}
}

func TestGraphQLRoutePolicyNeedsThePersonaAuthorizer(t *testing.T) {
	p := &Platform{}
	if p.graphQLRoutePolicy() != nil {
		t.Error("a platform with no authorizer produced a route policy")
	}
	reg := persona.NewRegistry()
	p = &Platform{authorizer: persona.NewAuthorizer(reg, &persona.OIDCRoleMapper{Registry: reg})}
	if p.graphQLRoutePolicy() == nil {
		t.Error("the persona authorizer did not produce a route policy")
	}
}

func TestGraphQLExportDepsRequireThePortal(t *testing.T) {
	disabled := false
	p := &Platform{config: &Config{Portal: PortalConfig{Export: PortalExportConfig{Enabled: &disabled}}}}
	if p.graphQLExportDeps() != nil {
		t.Error("export was assembled for a deployment that turned it off")
	}

	// A portal with no blob backend cannot hold an exported result.
	p = &Platform{config: &Config{}, portalStore: portalstore.NewFromStores(
		portalstore.Stores{Asset: stubExportAssetStore{}}, nil, portalstore.Config{})}
	if p.graphQLExportDeps() != nil {
		t.Error("export was assembled with no object storage")
	}
}

func TestGraphQLExportDepsCarryThePlatformsOwnLimitsAndIdentity(t *testing.T) {
	p := &Platform{
		config: &Config{Portal: PortalConfig{
			S3Bucket: "assets", S3Prefix: "exports", PublicBaseURL: "https://portal.example.com",
		}},
		portalStore: portalstore.NewFromStores(portalstore.Stores{
			Asset: stubExportAssetStore{}, S3Client: stubExportS3{},
		}, nil, portalstore.Config{}),
	}
	deps := p.graphQLExportDeps()
	if deps == nil {
		t.Fatal("export was not assembled")
	}
	if deps.S3Bucket != "assets" || deps.S3Prefix != "exports" || deps.BaseURL != "https://portal.example.com" {
		t.Errorf("deps = %+v", deps)
	}
	// The same caps trino_export and api_export honor: an operator sets
	// them once.
	want := p.parseExportConfig()
	if deps.Config.MaxBytes != want.MaxBytes || deps.Config.MaxTimeout != want.MaxTimeout {
		t.Errorf("limits = %+v; want the platform's own", deps.Config)
	}
	if deps.GetUserContext == nil {
		t.Fatal("no identity callback")
	}
	if got := deps.GetUserContext(context.Background()); got != nil {
		t.Errorf("an anonymous context resolved to %+v", got)
	}
}

func TestWireGraphQLAttachesWhatThePlatformHas(t *testing.T) {
	reg := graphQLRegistry(t)
	p := &Platform{
		config:          &Config{},
		toolkitRegistry: reg,
		portalStore: portalstore.NewFromStores(portalstore.Stores{
			Asset: stubExportAssetStore{}, S3Client: stubExportS3{},
		}, nil, portalstore.Config{}),
	}

	// The endpoint is unreachable, which is the ordinary startup case for
	// a connection whose upstream is down: wiring completes and the
	// connection carries its cause.
	p.WireGraphQL(context.Background())

	tk := p.GraphQLToolkits()[0]
	if len(tk.Tools()) != 3 {
		t.Errorf("tools = %v; export was not attached", tk.Tools())
	}
	info, err := tk.SchemaInfo("erp")
	if err != nil {
		t.Fatalf("schema info: %v", err)
	}
	if info.Error == "" {
		t.Error("an unreachable endpoint left no recorded cause")
	}
}

// stubExportAssetStore and stubExportS3 satisfy the portal contracts the
// export assembly checks for. Their behavior is covered by their own
// packages; here they only have to be non-nil.
type stubExportAssetStore struct{ portal.AssetStore }

type stubExportS3 struct{ portal.S3Client }
