package platform

import (
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

func TestWithConfig(t *testing.T) {
	cfg := &Config{Server: ServerConfig{Name: "test"}}
	opt := WithConfig(cfg)

	opts := &Options{}
	opt(opts)

	if opts.Config != cfg {
		t.Error("WithConfig did not set Config")
	}
}

func TestWithDB(t *testing.T) {
	// We can't easily create a real sql.DB, so just test nil case
	opt := WithDB(nil)

	opts := &Options{}
	opt(opts)

	if opts.DB != nil {
		t.Error("WithDB should set nil DB")
	}
}

func TestWithSemanticProvider(t *testing.T) {
	provider := semantic.NewNoopProvider()
	opt := WithSemanticProvider(provider)

	opts := &Options{}
	opt(opts)

	if opts.SemanticProvider != provider {
		t.Error("WithSemanticProvider did not set provider")
	}
}

func TestWithQueryProvider(t *testing.T) {
	provider := query.NewNoopProvider()
	opt := WithQueryProvider(provider)

	opts := &Options{}
	opt(opts)

	if opts.QueryProvider != provider {
		t.Error("WithQueryProvider did not set provider")
	}
}

func TestWithStorageProvider(t *testing.T) {
	provider := storage.NewNoopProvider()
	opt := WithStorageProvider(provider)

	opts := &Options{}
	opt(opts)

	if opts.StorageProvider != provider {
		t.Error("WithStorageProvider did not set provider")
	}
}

func TestWithAuthenticator(t *testing.T) {
	auth := &middleware.NoopAuthenticator{}
	opt := WithAuthenticator(auth)

	opts := &Options{}
	opt(opts)

	if opts.Authenticator != auth {
		t.Error("WithAuthenticator did not set authenticator")
	}
}

func TestWithAuthorizer(t *testing.T) {
	authz := &middleware.NoopAuthorizer{}
	opt := WithAuthorizer(authz)

	opts := &Options{}
	opt(opts)

	if opts.Authorizer != authz {
		t.Error("WithAuthorizer did not set authorizer")
	}
}

func TestWithAuditLogger(t *testing.T) {
	logger := &middleware.NoopAuditLogger{}
	opt := WithAuditLogger(logger)

	opts := &Options{}
	opt(opts)

	if opts.AuditLogger != logger {
		t.Error("WithAuditLogger did not set logger")
	}
}

func TestWithPersonaRegistry(t *testing.T) {
	reg := persona.NewRegistry()
	opt := WithPersonaRegistry(reg)

	opts := &Options{}
	opt(opts)

	if opts.PersonaRegistry != reg {
		t.Error("WithPersonaRegistry did not set registry")
	}
}

func TestWithToolkitRegistry(t *testing.T) {
	reg := registry.NewRegistry()
	opt := WithToolkitRegistry(reg)

	opts := &Options{}
	opt(opts)

	if opts.ToolkitRegistry != reg {
		t.Error("WithToolkitRegistry did not set registry")
	}
}

func TestWithRuleEngine(t *testing.T) {
	engine := tuning.NewRuleEngine(&tuning.Rules{})
	opt := WithRuleEngine(engine)

	opts := &Options{}
	opt(opts)

	if opts.RuleEngine != engine {
		t.Error("WithRuleEngine did not set engine")
	}
}

func TestOptionsStruct(t *testing.T) {
	// Test that Options can hold all fields
	opts := Options{
		Config:           &Config{},
		DB:               nil,
		SemanticProvider: semantic.NewNoopProvider(),
		QueryProvider:    query.NewNoopProvider(),
		StorageProvider:  storage.NewNoopProvider(),
		Authenticator:    &middleware.NoopAuthenticator{},
		Authorizer:       &middleware.NoopAuthorizer{},
		AuditLogger:      &middleware.NoopAuditLogger{},
		PersonaRegistry:  persona.NewRegistry(),
		ToolkitRegistry:  registry.NewRegistry(),
		RuleEngine:       tuning.NewRuleEngine(&tuning.Rules{}),
	}

	if opts.Config == nil {
		t.Error("Config is nil")
	}
	if opts.SemanticProvider == nil {
		t.Error("SemanticProvider is nil")
	}
}
