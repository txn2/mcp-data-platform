package platform

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

func TestUtilConnectionEnabled(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name       string
		enabled    *bool
		prereqsMet bool
		want       bool
	}{
		{"nil auto-on when prereqs met", nil, true, true},
		{"nil auto-off when prereqs unmet", nil, false, false},
		{"explicit on with prereqs", &on, true, true},
		{"explicit on cannot force without prereqs", &on, false, false},
		{"explicit off", &off, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := apiGatewayUtilConnectionConfig{Enabled: tt.enabled}
			if got := c.UtilConnectionEnabled(tt.prereqsMet); got != tt.want {
				t.Errorf("UtilConnectionEnabled(%v) = %v; want %v", tt.prereqsMet, got, tt.want)
			}
		})
	}
}

func TestWireUtilConnection_NoToolkitNoop(_ *testing.T) {
	p := &Platform{
		toolkitRegistry: registry.NewRegistry(),
		lifecycle:       NewLifecycle(),
		config:          &Config{},
	}
	// No api-gateway toolkit -> prerequisites unmet -> no hook, no panic.
	wireUtilConnection(p)
}

func TestWireUtilConnection_DisabledNoop(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register: %v", err)
	}
	disabled := false
	p := &Platform{
		toolkitRegistry: reg,
		lifecycle:       NewLifecycle(),
		config:          &Config{APIGateway: APIGatewayConfig{UtilConnection: apiGatewayUtilConnectionConfig{Enabled: &disabled}}},
	}
	wireUtilConnection(p)
	if err := p.lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	if tk.HasConnection("util") {
		t.Error("util connection should not register when disabled")
	}
}

func TestWireUtilConnection_EnabledRegistersOnStart(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register: %v", err)
	}
	p := &Platform{
		toolkitRegistry: reg,
		lifecycle:       NewLifecycle(),
		config:          &Config{},
	}
	wireUtilConnection(p)
	if err := p.lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	if !tk.HasConnection("util") {
		t.Fatal("util connection not registered after OnStart fired")
	}
	specs, err := store.ListSpecs(context.Background(), "util")
	if err != nil || len(specs) != 1 {
		t.Fatalf("seeded specs = %d (err %v); want 1", len(specs), err)
	}
}

func TestWireUtilConnection_BadCIDRNonFatal(t *testing.T) {
	store := apicatalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatalf("register: %v", err)
	}
	p := &Platform{
		toolkitRegistry: reg,
		lifecycle:       NewLifecycle(),
		config: &Config{APIGateway: APIGatewayConfig{UtilConnection: apiGatewayUtilConnectionConfig{
			AllowPrivateCIDRs: []string{"bogus"},
		}}},
	}
	// Must not panic and must not register a connection that could
	// never have been built.
	wireUtilConnection(p)
	if err := p.lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	if tk.HasConnection("util") {
		t.Error("util connection must not register with an unparseable allow_private_cidrs")
	}
}
