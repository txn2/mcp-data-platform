package platform

import (
	"database/sql"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/session"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// Options configures the platform.
type Options struct {
	// Config is the platform configuration.
	Config *Config

	// Database connection (optional, will be created from config if not provided).
	DB *sql.DB

	// SemanticProvider (optional, will be created from config if not provided).
	SemanticProvider semantic.Provider

	// QueryProvider (optional, will be created from config if not provided).
	QueryProvider query.Provider

	// StorageProvider (optional, will be created from config if not provided).
	StorageProvider storage.Provider

	// Authenticator (optional, will be created from config if not provided).
	Authenticator middleware.Authenticator

	// Authorizer (optional, will be created from config if not provided).
	Authorizer middleware.Authorizer

	// AuditLogger (optional, will be created from config if not provided).
	AuditLogger middleware.AuditLogger

	// PersonaRegistry (optional, will be created from config if not provided).
	PersonaRegistry *persona.Registry

	// ToolkitRegistry (optional, will be created if not provided).
	ToolkitRegistry *registry.Registry

	// RuleEngine (optional, will be created from config if not provided).
	RuleEngine *tuning.RuleEngine

	// SessionStore (optional, will be created from config if not provided).
	SessionStore session.Store
}

// Option is a functional option for configuring the platform.
type Option func(*Options)

// WithConfig sets the configuration.
func WithConfig(cfg *Config) Option {
	return func(o *Options) {
		o.Config = cfg
	}
}

// WithDB sets the database connection.
func WithDB(db *sql.DB) Option {
	return func(o *Options) {
		o.DB = db
	}
}

// WithSemanticProvider sets the semantic provider.
func WithSemanticProvider(provider semantic.Provider) Option {
	return func(o *Options) {
		o.SemanticProvider = provider
	}
}

// WithQueryProvider sets the query provider.
func WithQueryProvider(provider query.Provider) Option {
	return func(o *Options) {
		o.QueryProvider = provider
	}
}

// WithStorageProvider sets the storage provider.
func WithStorageProvider(provider storage.Provider) Option {
	return func(o *Options) {
		o.StorageProvider = provider
	}
}

// WithAuthenticator sets the authenticator.
func WithAuthenticator(auth middleware.Authenticator) Option {
	return func(o *Options) {
		o.Authenticator = auth
	}
}

// WithAuthorizer sets the authorizer.
func WithAuthorizer(authz middleware.Authorizer) Option {
	return func(o *Options) {
		o.Authorizer = authz
	}
}

// WithAuditLogger sets the audit logger.
func WithAuditLogger(logger middleware.AuditLogger) Option {
	return func(o *Options) {
		o.AuditLogger = logger
	}
}

// WithPersonaRegistry sets the persona registry.
func WithPersonaRegistry(reg *persona.Registry) Option {
	return func(o *Options) {
		o.PersonaRegistry = reg
	}
}

// WithToolkitRegistry sets the toolkit registry.
func WithToolkitRegistry(reg *registry.Registry) Option {
	return func(o *Options) {
		o.ToolkitRegistry = reg
	}
}

// WithRuleEngine sets the rule engine.
func WithRuleEngine(engine *tuning.RuleEngine) Option {
	return func(o *Options) {
		o.RuleEngine = engine
	}
}

// WithSessionStore sets the session store.
func WithSessionStore(store session.Store) Option {
	return func(o *Options) {
		o.SessionStore = store
	}
}
