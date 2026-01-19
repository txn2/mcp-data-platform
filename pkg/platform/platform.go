package platform

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/server"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// Platform is the main platform facade.
type Platform struct {
	config *Config

	// Core components
	mcpServer       *server.MCPServer
	lifecycle       *Lifecycle
	middlewareChain *middleware.Chain

	// Providers
	semanticProvider semantic.Provider
	queryProvider    query.Provider

	// Registries
	toolkitRegistry *registry.Registry
	personaRegistry *persona.Registry

	// Auth
	authenticator middleware.Authenticator
	authorizer    middleware.Authorizer

	// Audit
	auditLogger middleware.AuditLogger

	// Tuning
	ruleEngine    *tuning.RuleEngine
	promptManager *tuning.PromptManager
	hintManager   *tuning.HintManager
}

// New creates a new platform instance.
func New(opts ...Option) (*Platform, error) {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}

	if options.Config == nil {
		return nil, fmt.Errorf("config is required")
	}

	p := &Platform{
		config:    options.Config,
		lifecycle: NewLifecycle(),
	}

	// Initialize components
	if err := p.initializeComponents(options); err != nil {
		return nil, fmt.Errorf("initializing components: %w", err)
	}

	return p, nil
}

// initializeComponents initializes all platform components.
func (p *Platform) initializeComponents(opts *Options) error {
	if err := p.initProviders(opts); err != nil {
		return err
	}
	if err := p.initRegistries(opts); err != nil {
		return err
	}
	if err := p.initAuth(opts); err != nil {
		return err
	}
	p.initTuning(opts)
	p.finalizeSetup()
	return nil
}

// initProviders initializes semantic and query providers.
func (p *Platform) initProviders(opts *Options) error {
	var err error
	if opts.SemanticProvider != nil {
		p.semanticProvider = opts.SemanticProvider
	} else if p.semanticProvider, err = p.createSemanticProvider(); err != nil {
		return fmt.Errorf("creating semantic provider: %w", err)
	}

	if opts.QueryProvider != nil {
		p.queryProvider = opts.QueryProvider
	} else if p.queryProvider, err = p.createQueryProvider(); err != nil {
		return fmt.Errorf("creating query provider: %w", err)
	}
	return nil
}

// initRegistries initializes persona and toolkit registries.
func (p *Platform) initRegistries(opts *Options) error {
	if opts.PersonaRegistry != nil {
		p.personaRegistry = opts.PersonaRegistry
	} else {
		p.personaRegistry = persona.NewRegistry()
		if err := p.loadPersonas(); err != nil {
			return fmt.Errorf("loading personas: %w", err)
		}
	}

	if opts.ToolkitRegistry != nil {
		p.toolkitRegistry = opts.ToolkitRegistry
	} else {
		p.toolkitRegistry = registry.NewRegistry()
	}
	p.toolkitRegistry.SetSemanticProvider(p.semanticProvider)
	p.toolkitRegistry.SetQueryProvider(p.queryProvider)
	return nil
}

// initAuth initializes authentication and authorization components.
func (p *Platform) initAuth(opts *Options) error {
	if opts.Authenticator != nil {
		p.authenticator = opts.Authenticator
	} else {
		authenticator, err := p.createAuthenticator()
		if err != nil {
			return fmt.Errorf("creating authenticator: %w", err)
		}
		p.authenticator = authenticator
	}

	if opts.Authorizer != nil {
		p.authorizer = opts.Authorizer
	} else {
		p.authorizer = p.createAuthorizer()
	}

	if opts.AuditLogger != nil {
		p.auditLogger = opts.AuditLogger
	} else {
		p.auditLogger = &middleware.NoopAuditLogger{}
	}
	return nil
}

// initTuning initializes tuning components.
func (p *Platform) initTuning(opts *Options) {
	if opts.RuleEngine != nil {
		p.ruleEngine = opts.RuleEngine
	} else {
		rules := &tuning.Rules{
			RequireDataHubCheck: p.config.Tuning.Rules.RequireDataHubCheck,
			WarnOnDeprecated:    p.config.Tuning.Rules.WarnOnDeprecated,
			QualityThreshold:    p.config.Tuning.Rules.QualityThreshold,
		}
		p.ruleEngine = tuning.NewRuleEngine(rules)
	}

	p.promptManager = tuning.NewPromptManager(tuning.PromptConfig{
		PromptsDir: p.config.Tuning.PromptsDir,
	})
	p.hintManager = tuning.NewHintManager()
	p.hintManager.SetHints(tuning.DefaultHints())
}

// finalizeSetup completes platform initialization.
func (p *Platform) finalizeSetup() {
	p.buildMiddlewareChain()
	p.toolkitRegistry.SetMiddleware(p.middlewareChain)
	p.mcpServer = server.NewMCPServer(
		p.config.Server.Name,
		"1.0.0",
		server.WithLogging(),
	)
}

// createSemanticProvider creates the semantic provider based on config.
func (p *Platform) createSemanticProvider() (semantic.Provider, error) {
	switch p.config.Semantic.Provider {
	case "noop", "":
		return semantic.NewNoopProvider(), nil
	default:
		// For real implementations, you would create the actual provider here
		return semantic.NewNoopProvider(), nil
	}
}

// createQueryProvider creates the query provider based on config.
func (p *Platform) createQueryProvider() (query.Provider, error) {
	switch p.config.Query.Provider {
	case "noop", "":
		return query.NewNoopProvider(), nil
	default:
		// For real implementations, you would create the actual provider here
		return query.NewNoopProvider(), nil
	}
}

// loadPersonas loads personas from config.
func (p *Platform) loadPersonas() error {
	for name, def := range p.config.Personas.Definitions {
		persona := &persona.Persona{
			Name:        name,
			DisplayName: def.DisplayName,
			Roles:       def.Roles,
			Tools: persona.ToolRules{
				Allow: def.Tools.Allow,
				Deny:  def.Tools.Deny,
			},
			Prompts: persona.PromptConfig{
				SystemPrefix: def.Prompts.SystemPrefix,
			},
			Hints: def.Hints,
		}
		if err := p.personaRegistry.Register(persona); err != nil {
			return fmt.Errorf("registering persona %s: %w", name, err)
		}
	}

	if p.config.Personas.DefaultPersona != "" {
		p.personaRegistry.SetDefault(p.config.Personas.DefaultPersona)
	}

	return nil
}

// createAuthenticator creates the authenticator based on config.
func (p *Platform) createAuthenticator() (middleware.Authenticator, error) {
	var authenticators []middleware.Authenticator

	// OIDC authenticator
	if p.config.Auth.OIDC.Enabled {
		oidcAuth, err := auth.NewOIDCAuthenticator(auth.OIDCConfig{
			Issuer:        p.config.Auth.OIDC.Issuer,
			ClientID:      p.config.Auth.OIDC.ClientID,
			Audience:      p.config.Auth.OIDC.Audience,
			RoleClaimPath: p.config.Auth.OIDC.RoleClaimPath,
			RolePrefix:    p.config.Auth.OIDC.RolePrefix,
		})
		if err != nil {
			return nil, fmt.Errorf("creating OIDC authenticator: %w", err)
		}
		authenticators = append(authenticators, oidcAuth)
	}

	// API key authenticator
	if p.config.Auth.APIKeys.Enabled {
		var keys []auth.APIKey
		for _, k := range p.config.Auth.APIKeys.Keys {
			keys = append(keys, auth.APIKey{
				Key:   k.Key,
				Name:  k.Name,
				Roles: k.Roles,
			})
		}
		apiKeyAuth := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{Keys: keys})
		authenticators = append(authenticators, apiKeyAuth)
	}

	// If no authenticators configured, use noop
	if len(authenticators) == 0 {
		return &middleware.NoopAuthenticator{
			DefaultUserID: "anonymous",
			DefaultRoles:  []string{},
		}, nil
	}

	// Chain authenticators
	return auth.NewChainedAuthenticator(
		auth.ChainedAuthConfig{AllowAnonymous: true},
		authenticators...,
	), nil
}

// createAuthorizer creates the authorizer.
func (p *Platform) createAuthorizer() middleware.Authorizer {
	// Create role mapper
	mapper := &persona.OIDCRoleMapper{
		ClaimPath:      p.config.Auth.OIDC.RoleClaimPath,
		RolePrefix:     p.config.Auth.OIDC.RolePrefix,
		PersonaMapping: p.config.Personas.RoleMapping.OIDCToPersona,
		Registry:       p.personaRegistry,
	}

	return persona.NewPersonaAuthorizer(p.personaRegistry, mapper)
}

// buildMiddlewareChain builds the middleware chain.
func (p *Platform) buildMiddlewareChain() {
	p.middlewareChain = middleware.NewChain()

	// Before middleware (request processing)
	p.middlewareChain.UseBefore(middleware.AuthMiddleware(p.authenticator))
	p.middlewareChain.UseBefore(middleware.AuthzMiddleware(p.authorizer))

	// After middleware (response processing)
	if p.config.Injection.TrinoSemanticEnrichment || p.config.Injection.DataHubQueryEnrichment {
		p.middlewareChain.UseAfter(middleware.SemanticEnrichmentMiddleware(
			p.semanticProvider,
			p.queryProvider,
			middleware.EnrichmentConfig{
				EnrichTrinoResults:   p.config.Injection.TrinoSemanticEnrichment,
				EnrichDataHubResults: p.config.Injection.DataHubQueryEnrichment,
			},
		))
	}

	if p.config.Audit.Enabled {
		p.middlewareChain.UseAfter(middleware.AuditMiddleware(p.auditLogger))
	}
}

// Start starts the platform.
func (p *Platform) Start(ctx context.Context) error {
	// Load prompts
	if err := p.promptManager.LoadPrompts(); err != nil {
		return fmt.Errorf("loading prompts: %w", err)
	}

	// Register tools from all toolkits
	p.toolkitRegistry.RegisterAllTools(p.mcpServer)

	// Start lifecycle
	return p.lifecycle.Start(ctx)
}

// Stop stops the platform.
func (p *Platform) Stop(ctx context.Context) error {
	return p.lifecycle.Stop(ctx)
}

// MCPServer returns the MCP server.
func (p *Platform) MCPServer() *server.MCPServer {
	return p.mcpServer
}

// Config returns the platform configuration.
func (p *Platform) Config() *Config {
	return p.config
}

// SemanticProvider returns the semantic provider.
func (p *Platform) SemanticProvider() semantic.Provider {
	return p.semanticProvider
}

// QueryProvider returns the query provider.
func (p *Platform) QueryProvider() query.Provider {
	return p.queryProvider
}

// ToolkitRegistry returns the toolkit registry.
func (p *Platform) ToolkitRegistry() *registry.Registry {
	return p.toolkitRegistry
}

// PersonaRegistry returns the persona registry.
func (p *Platform) PersonaRegistry() *persona.Registry {
	return p.personaRegistry
}

// RuleEngine returns the rule engine.
func (p *Platform) RuleEngine() *tuning.RuleEngine {
	return p.ruleEngine
}

// MiddlewareChain returns the middleware chain.
func (p *Platform) MiddlewareChain() *middleware.Chain {
	return p.middlewareChain
}

// closeResource closes a resource and appends any error.
func closeResource(errs *[]error, closer Closer) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		*errs = append(*errs, err)
	}
}

// Close closes all platform resources.
func (p *Platform) Close() error {
	var errs []error

	closeResource(&errs, p.semanticProvider)
	closeResource(&errs, p.queryProvider)
	closeResource(&errs, p.toolkitRegistry)

	if closer, ok := p.auditLogger.(Closer); ok {
		closeResource(&errs, closer)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing platform: %v", errs)
	}
	return nil
}
