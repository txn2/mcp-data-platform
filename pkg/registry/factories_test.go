package registry

import (
	"testing"
)

func TestValidateConnectionConfig(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		cfg     map[string]any
		wantErr bool
	}{
		{
			name:    "trino valid",
			kind:    "trino",
			cfg:     map[string]any{"host": "trino.example.com"},
			wantErr: false,
		},
		{
			name:    "trino missing host",
			kind:    "trino",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "s3 empty config valid",
			kind:    "s3",
			cfg:     map[string]any{},
			wantErr: false,
		},
		{
			name:    "datahub missing url",
			kind:    "datahub",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "datahub valid",
			kind:    "datahub",
			cfg:     map[string]any{"url": "http://datahub.example.com"},
			wantErr: false,
		},
		{
			name:    "mcp gateway missing endpoint",
			kind:    "mcp",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "mcp gateway valid",
			kind:    "mcp",
			cfg:     map[string]any{"endpoint": "http://upstream.example.com/mcp"},
			wantErr: false,
		},
		{
			name:    "api gateway missing base_url",
			kind:    "api",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "api gateway valid",
			kind:    "api",
			cfg:     map[string]any{"base_url": "http://api.example.com"},
			wantErr: false,
		},
		{
			name:    "graphql missing endpoint_url",
			kind:    "graphql",
			cfg:     map[string]any{},
			wantErr: true,
		},
		{
			name:    "graphql valid",
			kind:    "graphql",
			cfg:     map[string]any{"endpoint_url": "https://api.example.com/graphql"},
			wantErr: false,
		},
		{
			name:    "unknown kind passes",
			kind:    "custom",
			cfg:     map[string]any{},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConnectionConfig(tc.kind, tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateConnectionConfig(%q) error = %v, wantErr %v",
					tc.kind, err, tc.wantErr)
			}
		})
	}
}

// TestGraphQLAggregateFactoryBuildsTheOneToolkit proves the kind is
// reachable from the registry the platform builds its toolkits through,
// and that one bad instance does not take the others down with it.
func TestGraphQLAggregateFactoryBuildsTheOneToolkit(t *testing.T) {
	tk, err := GraphQLAggregateFactory("erp", map[string]map[string]any{
		"erp":    {"endpoint_url": "https://erp.example.com/graphql"},
		"broken": {"schema_validation": "nonsense"},
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if tk.Kind() != "graphql" || tk.Name() != "erp" {
		t.Errorf("toolkit = %s/%s", tk.Kind(), tk.Name())
	}
	manager, ok := tk.(interface{ HasConnection(string) bool })
	if !ok {
		t.Fatal("the toolkit does not manage connections")
	}
	if !manager.HasConnection("erp") {
		t.Error("the valid instance did not register")
	}
	if manager.HasConnection("broken") {
		t.Error("the invalid instance registered")
	}
}

// TestRegisterBuiltinFactoriesIncludesGraphQL proves an operator saving a
// graphql connection through the admin UI lands in a live toolkit.
func TestRegisterBuiltinFactoriesIncludesGraphQL(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinFactories(r)
	factory, ok := r.GetAggregateFactory("graphql")
	if !ok {
		t.Fatal("the graphql kind has no factory; an admin save would land nowhere")
	}
	tk, err := factory("erp", map[string]map[string]any{
		"erp": {"endpoint_url": "https://erp.example.com/graphql"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tk.Kind() != "graphql" {
		t.Errorf("kind = %q", tk.Kind())
	}
}
