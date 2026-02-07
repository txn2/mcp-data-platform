package platform

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/crypto/bcrypt"

	// PostgreSQL driver for database/sql.
	_ "github.com/lib/pq"

	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/mcpapps"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/oauth"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/query"
	trinoquery "github.com/txn2/mcp-data-platform/pkg/query/trino"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	s3storage "github.com/txn2/mcp-data-platform/pkg/storage/s3"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// providerNoop is the provider name for no-op (disabled) providers.
const providerNoop = "noop"

// minSigningKeyLength is the minimum length in bytes for an OAuth signing key.
const minSigningKeyLength = 32

// defaultTrinoPort is the default port for Trino connections.
const defaultTrinoPort = 8080

// defaultTrinoQueryLimit is the default query limit for Trino connections.
const defaultTrinoQueryLimit = 1000

// defaultTrinoMaxLimit is the maximum query limit for Trino connections.
const defaultTrinoMaxLimit = 10000

// Platform is the main platform facade.
type Platform struct {
	config *Config

	// Core components
	mcpServer *mcp.Server
	lifecycle *Lifecycle

	// Database
	db         *sql.DB
	auditStore *auditpostgres.Store

	// Providers
	semanticProvider semantic.Provider
	queryProvider    query.Provider
	storageProvider  storage.Provider

	// Registries
	toolkitRegistry *registry.Registry
	personaRegistry *persona.Registry

	// Auth
	authenticator middleware.Authenticator
	authorizer    middleware.Authorizer

	// OAuth
	oauthServer     *oauth.Server
	oauthSigningKey []byte

	// Audit
	auditLogger middleware.AuditLogger

	// Session dedup
	sessionCache *middleware.SessionEnrichmentCache

	// Tuning
	ruleEngine    *tuning.RuleEngine
	promptManager *tuning.PromptManager
	hintManager   *tuning.HintManager

	// MCP Apps
	mcpAppsRegistry *mcpapps.Registry
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
	// Initialize database first (required for audit logging)
	if err := p.initDatabase(); err != nil {
		return err
	}
	if err := p.initProviders(opts); err != nil {
		return err
	}
	if err := p.initRegistries(opts); err != nil {
		return err
	}
	// Parse OAuth signing key early so auth can use it
	if err := p.initOAuthSigningKey(); err != nil {
		return err
	}
	if err := p.initAuth(opts); err != nil {
		return err
	}
	// Initialize audit logging after auth
	if err := p.initAudit(opts); err != nil {
		return err
	}
	if err := p.initOAuth(); err != nil {
		return err
	}
	p.initTuning(opts)
	if err := p.initMCPApps(); err != nil {
		return err
	}
	p.finalizeSetup()
	return nil
}

// initDatabase initializes the database connection and runs migrations if configured.
func (p *Platform) initDatabase() error {
	if p.config.Database.DSN == "" {
		return nil
	}

	db, err := sql.Open("postgres", p.config.Database.DSN)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}

	db.SetMaxOpenConns(p.config.Database.MaxOpenConns)
	if p.config.Database.MaxOpenConns == 0 {
		db.SetMaxOpenConns(defaultMaxOpenConns)
	}

	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}

	p.db = db
	slog.Info("database connected", "max_open_conns", p.config.Database.MaxOpenConns)

	// Run database migrations
	if err := migrate.Run(db); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	return nil
}

// initOAuthSigningKey parses or generates the OAuth signing key.
// This must be called before initAuth so the OAuth authenticator can be configured.
func (p *Platform) initOAuthSigningKey() error {
	if !p.config.OAuth.Enabled {
		return nil
	}

	signingKey, err := p.parseOrGenerateSigningKey()
	if err != nil {
		return fmt.Errorf("configuring OAuth signing key: %w", err)
	}
	p.oauthSigningKey = signingKey
	return nil
}

// initProviders initializes semantic, query, and storage providers.
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

	if opts.StorageProvider != nil {
		p.storageProvider = opts.StorageProvider
	} else if p.storageProvider, err = p.createStorageProvider(); err != nil {
		return fmt.Errorf("creating storage provider: %w", err)
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
		// Register built-in toolkit factories
		registry.RegisterBuiltinFactories(p.toolkitRegistry)
	}

	// Inject providers for cross-injection
	p.toolkitRegistry.SetSemanticProvider(p.semanticProvider)
	p.toolkitRegistry.SetQueryProvider(p.queryProvider)

	// Load toolkits from configuration
	if p.config.Toolkits != nil {
		loader := registry.NewLoader(p.toolkitRegistry)
		if err := loader.LoadFromMap(p.config.Toolkits); err != nil {
			return fmt.Errorf("loading toolkits: %w", err)
		}
	}

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

	return nil
}

// initAudit initializes audit logging.
func (p *Platform) initAudit(opts *Options) error {
	// Use provided audit logger if available
	if opts.AuditLogger != nil {
		p.auditLogger = opts.AuditLogger
		return nil
	}

	// If audit logging is disabled, use noop logger
	if !p.config.Audit.Enabled {
		p.auditLogger = &middleware.NoopAuditLogger{}
		return nil
	}

	// Audit logging requires a database connection
	if p.db == nil {
		slog.Warn("audit logging enabled but no database configured, using noop logger")
		p.auditLogger = &middleware.NoopAuditLogger{}
		return nil
	}

	// Create PostgreSQL audit store
	store := auditpostgres.New(p.db, auditpostgres.Config{
		RetentionDays: p.config.Audit.RetentionDays,
	})

	// Start background cleanup routine
	store.StartCleanupRoutine(context.Background(), 24*time.Hour)

	p.auditStore = store
	p.auditLogger = middleware.NewAuditStoreAdapter(store)

	slog.Info("audit logging enabled",
		"retention_days", p.config.Audit.RetentionDays,
		"log_tool_calls", p.config.Audit.LogToolCalls,
	)
	return nil
}

// initOAuth initializes the OAuth server if enabled.
func (p *Platform) initOAuth() error {
	if !p.config.OAuth.Enabled {
		return nil
	}

	// Create in-memory storage
	oauthStorage := oauth.NewMemoryStorage()

	// Pre-register clients from config
	for _, clientCfg := range p.config.OAuth.Clients {
		hashedSecret, err := bcrypt.GenerateFromPassword([]byte(clientCfg.Secret), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hashing client secret for %s: %w", clientCfg.ID, err)
		}

		client := &oauth.Client{
			ID:           clientCfg.ID,
			ClientID:     clientCfg.ID,
			ClientSecret: string(hashedSecret),
			Name:         clientCfg.ID,
			RedirectURIs: clientCfg.RedirectURIs,
			GrantTypes:   []string{"authorization_code", "refresh_token"},
			RequirePKCE:  true,
			Active:       true,
		}

		if err := oauthStorage.CreateClient(context.Background(), client); err != nil {
			return fmt.Errorf("creating client %s: %w", clientCfg.ID, err)
		}
	}

	// Build OAuth server config
	serverConfig := oauth.ServerConfig{
		Issuer:         p.config.OAuth.Issuer,
		AccessTokenTTL: 1 * time.Hour,
		SigningKey:     p.oauthSigningKey,
		DCR: oauth.DCRConfig{
			Enabled:                 p.config.OAuth.DCR.Enabled,
			AllowedRedirectPatterns: p.config.OAuth.DCR.AllowedRedirectPatterns,
			RequirePKCE:             true,
		},
	}

	// Configure upstream IdP if present
	if p.config.OAuth.Upstream != nil {
		serverConfig.Upstream = &oauth.UpstreamConfig{
			Issuer:       p.config.OAuth.Upstream.Issuer,
			ClientID:     p.config.OAuth.Upstream.ClientID,
			ClientSecret: p.config.OAuth.Upstream.ClientSecret,
			RedirectURI:  p.config.OAuth.Upstream.RedirectURI,
		}
	}

	// Create OAuth server
	server, err := oauth.NewServer(serverConfig, oauthStorage)
	if err != nil {
		return fmt.Errorf("creating OAuth server: %w", err)
	}

	p.oauthServer = server
	return nil
}

// parseOrGenerateSigningKey parses the configured signing key or generates a random one.
func (p *Platform) parseOrGenerateSigningKey() ([]byte, error) {
	if p.config.OAuth.SigningKey != "" {
		// Decode base64-encoded key from config
		key, err := base64.StdEncoding.DecodeString(p.config.OAuth.SigningKey)
		if err != nil {
			return nil, fmt.Errorf("decoding signing key: %w", err)
		}
		if len(key) < minSigningKeyLength {
			return nil, fmt.Errorf("signing key must be at least %d bytes", minSigningKeyLength)
		}
		return key, nil
	}

	// Generate random key if not configured (not recommended for production)
	key := make([]byte, minSigningKeyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating random key: %w", err)
	}
	slog.Warn("OAuth signing key not configured, generated random key (tokens won't survive restart)")
	return key, nil
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

	// Initialize hint manager with defaults
	p.hintManager = tuning.NewHintManager()
	p.hintManager.SetHints(tuning.DefaultHints())

	// Load persona-specific hints
	for _, pers := range p.personaRegistry.All() {
		if pers.Hints != nil {
			p.hintManager.SetHints(pers.Hints)
		}
	}
}

// initMCPApps initializes MCP Apps support.
func (p *Platform) initMCPApps() error {
	if !p.config.MCPApps.Enabled {
		return nil
	}

	p.mcpAppsRegistry = mcpapps.NewRegistry()

	// Register apps based on config
	for appName, appCfg := range p.config.MCPApps.Apps {
		if !appCfg.Enabled {
			continue
		}

		// Create app definition directly from config
		app := &mcpapps.AppDefinition{
			Name:       appName,
			ToolNames:  appCfg.Tools,
			AssetsPath: appCfg.AssetsPath,
			EntryPoint: appCfg.EntryPoint,
			Config:     appCfg.Config,
		}

		// Apply defaults
		if app.EntryPoint == "" {
			app.EntryPoint = "index.html"
		}

		// Set ResourceURI from config or generate default
		if appCfg.ResourceURI != "" {
			app.ResourceURI = appCfg.ResourceURI
		} else {
			app.ResourceURI = fmt.Sprintf("ui://%s", appName)
		}

		// Convert CSP config
		if appCfg.CSP != nil {
			app.CSP = convertCSP(appCfg.CSP)
		}

		// Validate basic fields first
		if err := app.Validate(); err != nil {
			return fmt.Errorf("app %s: %w", appName, err)
		}

		// Validate assets exist on filesystem
		if err := app.ValidateAssets(); err != nil {
			return fmt.Errorf("app %s: %w", appName, err)
		}

		if err := p.mcpAppsRegistry.Register(app); err != nil {
			return fmt.Errorf("registering %s app: %w", appName, err)
		}

		slog.Info("registered MCP app", "app", appName, "resource_uri", app.ResourceURI)
	}

	return nil
}

// convertCSP converts platform CSPAppConfig to mcpapps.CSPConfig.
func convertCSP(cfg *CSPAppConfig) *mcpapps.CSPConfig {
	if cfg == nil {
		return nil
	}

	csp := &mcpapps.CSPConfig{
		ResourceDomains: cfg.ResourceDomains,
		ConnectDomains:  cfg.ConnectDomains,
		FrameDomains:    cfg.FrameDomains,
	}

	if cfg.ClipboardWrite {
		csp.Permissions = &mcpapps.PermissionsConfig{
			ClipboardWrite: &struct{}{},
		}
	}

	return csp
}

// finalizeSetup completes platform initialization.
func (p *Platform) finalizeSetup() {
	p.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    p.config.Server.Name,
		Version: p.config.Server.Version,
	}, nil)

	// Add MCP protocol-level middleware.
	//
	// IMPORTANT: AddReceivingMiddleware wraps the current handler, so each
	// call makes its middleware the new outermost layer. The LAST middleware
	// added runs FIRST. We add innermost middleware first and outermost last.
	//
	// Desired execution order (outermost → innermost → handler):
	//   Apps metadata → Auth/Authz → Audit → Rules → Enrichment → handler
	//
	// Therefore we add in reverse (innermost first):

	// 1. Semantic enrichment (innermost) - enriches responses with cross-service context
	needsEnrichment := p.config.Injection.TrinoSemanticEnrichment ||
		p.config.Injection.DataHubQueryEnrichment ||
		p.config.Injection.S3SemanticEnrichment ||
		p.config.Injection.DataHubStorageEnrichment

	if needsEnrichment {
		enrichCfg := p.buildEnrichmentConfig()
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPSemanticEnrichmentMiddleware(
				p.semanticProvider,
				p.queryProvider,
				p.storageProvider,
				enrichCfg,
			),
		)
	}

	// 2. Rule enforcement - adds operational guidance to responses
	if p.ruleEngine != nil {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPRuleEnforcementMiddleware(p.ruleEngine, p.hintManager),
		)
	}

	// 3. Audit - logs tool calls (reads PlatformContext set by Auth/Authz above)
	if p.config.Audit.Enabled && p.config.Audit.LogToolCalls {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPAuditMiddleware(p.auditLogger),
		)
	}

	// 4. Auth/Authz (outermost for tools/call) - authenticates and authorizes
	// users, creates PlatformContext. Must be outer to Audit so PlatformContext
	// is available in the ctx that Audit receives.
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPToolCallMiddleware(p.authenticator, p.authorizer, p.toolkitRegistry),
	)

	// 5. MCP Apps metadata (overall outermost) - injects _meta.ui into tools/list
	if p.mcpAppsRegistry != nil && p.mcpAppsRegistry.HasApps() {
		p.mcpServer.AddReceivingMiddleware(
			mcpapps.ToolMetadataMiddleware(p.mcpAppsRegistry),
		)
		// Register UI resources for each app
		p.mcpAppsRegistry.RegisterResources(p.mcpServer)
	}
}

// buildEnrichmentConfig creates the enrichment middleware config, including
// optional session dedup cache setup.
func (p *Platform) buildEnrichmentConfig() middleware.EnrichmentConfig {
	cfg := middleware.EnrichmentConfig{
		EnrichTrinoResults:          p.config.Injection.TrinoSemanticEnrichment,
		EnrichDataHubResults:        p.config.Injection.DataHubQueryEnrichment,
		EnrichS3Results:             p.config.Injection.S3SemanticEnrichment,
		EnrichDataHubStorageResults: p.config.Injection.DataHubStorageEnrichment,
	}

	if p.config.Injection.SessionDedup.IsEnabled() {
		p.sessionCache = middleware.NewSessionEnrichmentCache(
			p.config.Injection.SessionDedup.EntryTTL,
			p.config.Injection.SessionDedup.SessionTimeout,
		)
		p.sessionCache.StartCleanup(1 * time.Minute)
		cfg.SessionCache = p.sessionCache
		cfg.DedupMode = middleware.DedupMode(p.config.Injection.SessionDedup.EffectiveMode())

		slog.Info("session metadata dedup enabled",
			"mode", p.config.Injection.SessionDedup.EffectiveMode(),
			"entry_ttl", p.config.Injection.SessionDedup.EntryTTL,
			"session_timeout", p.config.Injection.SessionDedup.SessionTimeout,
		)
	}

	return cfg
}

// createSemanticProvider creates the semantic provider based on config.
func (p *Platform) createSemanticProvider() (semantic.Provider, error) {
	switch p.config.Semantic.Provider {
	case "datahub":
		// Get DataHub config from toolkits
		datahubCfg := p.getDataHubConfig(p.config.Semantic.Instance)
		if datahubCfg == nil {
			return nil, fmt.Errorf("datahub instance %q not found in toolkits config", p.config.Semantic.Instance)
		}

		// Determine platform for URN building
		platform := p.config.Semantic.URNMapping.Platform
		if platform == "" {
			platform = "trino" // Default platform if not configured
		}

		adapter, err := datahubsemantic.New(datahubsemantic.Config{
			URL:            datahubCfg.URL,
			Token:          datahubCfg.Token,
			Platform:       platform,
			Timeout:        datahubCfg.Timeout,
			Debug:          datahubCfg.Debug,
			CatalogMapping: p.config.Semantic.URNMapping.CatalogMapping,
			Lineage:        p.config.Semantic.Lineage,
		})
		if err != nil {
			return nil, fmt.Errorf("creating datahub semantic provider: %w", err)
		}

		// Wrap with caching if enabled
		if p.config.Semantic.Cache.Enabled {
			return semantic.NewCachedProvider(adapter, semantic.CacheConfig{
				TTL: p.config.Semantic.Cache.TTL,
			}), nil
		}
		return adapter, nil

	case providerNoop, "":
		return semantic.NewNoopProvider(), nil

	default:
		return nil, fmt.Errorf("unknown semantic provider: %s", p.config.Semantic.Provider)
	}
}

// createQueryProvider creates the query provider based on config.
func (p *Platform) createQueryProvider() (query.Provider, error) {
	switch p.config.Query.Provider {
	case "trino":
		// Get Trino config from toolkits
		trinoCfg := p.getTrinoConfig(p.config.Query.Instance)
		if trinoCfg == nil {
			return nil, fmt.Errorf("trino instance %q not found in toolkits config", p.config.Query.Instance)
		}

		adapter, err := trinoquery.New(trinoquery.Config{
			Host:           trinoCfg.Host,
			Port:           trinoCfg.Port,
			User:           trinoCfg.User,
			Password:       trinoCfg.Password,
			Catalog:        trinoCfg.Catalog,
			Schema:         trinoCfg.Schema,
			SSL:            trinoCfg.SSL,
			SSLVerify:      trinoCfg.SSLVerify,
			Timeout:        trinoCfg.Timeout,
			DefaultLimit:   trinoCfg.DefaultLimit,
			MaxLimit:       trinoCfg.MaxLimit,
			ReadOnly:       trinoCfg.ReadOnly,
			ConnectionName: trinoCfg.ConnectionName,
			CatalogMapping: p.config.Query.URNMapping.CatalogMapping,
		})
		if err != nil {
			return nil, fmt.Errorf("creating trino query provider: %w", err)
		}
		return adapter, nil

	case providerNoop, "":
		return query.NewNoopProvider(), nil

	default:
		return nil, fmt.Errorf("unknown query provider: %s", p.config.Query.Provider)
	}
}

// createStorageProvider creates the storage provider based on config.
func (p *Platform) createStorageProvider() (storage.Provider, error) {
	switch p.config.Storage.Provider {
	case "s3":
		// Get S3 config from toolkits
		s3Cfg := p.getS3Config(p.config.Storage.Instance)
		if s3Cfg == nil {
			return nil, fmt.Errorf("s3 instance %q not found in toolkits config", p.config.Storage.Instance)
		}

		adapter, err := s3storage.NewFromConfig(s3storage.Config{
			Region:         s3Cfg.Region,
			Endpoint:       s3Cfg.Endpoint,
			AccessKeyID:    s3Cfg.AccessKeyID,
			SecretKey:      s3Cfg.SecretKey,
			BucketPrefix:   s3Cfg.BucketPrefix,
			ConnectionName: s3Cfg.ConnectionName,
		})
		if err != nil {
			return nil, fmt.Errorf("creating s3 storage provider: %w", err)
		}
		return adapter, nil

	case providerNoop, "":
		return storage.NewNoopProvider(), nil

	default:
		return nil, fmt.Errorf("unknown storage provider: %s", p.config.Storage.Provider)
	}
}

// loadPersonas loads personas from config.
func (p *Platform) loadPersonas() error {
	for name, def := range p.config.Personas.Definitions {
		personaDef := &persona.Persona{
			Name:        name,
			DisplayName: def.DisplayName,
			Description: def.Description,
			Roles:       def.Roles,
			Tools: persona.ToolRules{
				Allow: def.Tools.Allow,
				Deny:  def.Tools.Deny,
			},
			Prompts: persona.PromptConfig{
				SystemPrefix: def.Prompts.SystemPrefix,
				SystemSuffix: def.Prompts.SystemSuffix,
				Instructions: def.Prompts.Instructions,
			},
			Hints:    def.Hints,
			Priority: def.Priority,
		}
		if err := p.personaRegistry.Register(personaDef); err != nil {
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

	// OAuth JWT authenticator (for tokens issued by our OAuth server)
	// This is checked first because OAuth tokens from Claude Desktop will use this
	if p.config.OAuth.Enabled && len(p.oauthSigningKey) > 0 {
		oauthAuth, err := auth.NewOAuthJWTAuthenticator(auth.OAuthJWTConfig{
			Issuer:        p.config.OAuth.Issuer,
			SigningKey:    p.oauthSigningKey,
			RoleClaimPath: p.config.Auth.OIDC.RoleClaimPath,
			RolePrefix:    p.config.Auth.OIDC.RolePrefix,
		})
		if err != nil {
			return nil, fmt.Errorf("creating OAuth JWT authenticator: %w", err)
		}
		authenticators = append(authenticators, oauthAuth)
	}

	// OIDC authenticator (for tokens from external IdPs like Keycloak)
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

	// Chain authenticators - anonymous access disabled by default
	return auth.NewChainedAuthenticator(
		auth.ChainedAuthConfig{AllowAnonymous: p.config.Auth.AllowAnonymous},
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

	return persona.NewAuthorizer(p.personaRegistry, mapper)
}

// Start starts the platform.
func (p *Platform) Start(ctx context.Context) error {
	// Load prompts from prompts_dir
	if err := p.promptManager.LoadPrompts(); err != nil {
		return fmt.Errorf("loading prompts: %w", err)
	}

	// Register tools from all toolkits
	p.toolkitRegistry.RegisterAllTools(p.mcpServer)

	// Register platform info tool
	p.registerInfoTool()

	// Register platform-level prompts from config
	p.registerPlatformPrompts()

	// Register hints resource
	p.registerHintsResource()

	// Start lifecycle
	return p.lifecycle.Start(ctx)
}

// Stop stops the platform.
func (p *Platform) Stop(ctx context.Context) error {
	return p.lifecycle.Stop(ctx)
}

// MCPServer returns the MCP server.
func (p *Platform) MCPServer() *mcp.Server {
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

// StorageProvider returns the storage provider.
func (p *Platform) StorageProvider() storage.Provider {
	return p.storageProvider
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

// HintManager returns the hint manager.
func (p *Platform) HintManager() *tuning.HintManager {
	return p.hintManager
}

// OAuthServer returns the OAuth server, or nil if not enabled.
func (p *Platform) OAuthServer() *oauth.Server {
	return p.oauthServer
}

// datahubConfig holds extracted DataHub configuration.
type datahubConfig struct {
	URL     string
	Token   string
	Timeout time.Duration
	Debug   bool
}

// trinoConfig holds extracted Trino configuration.
type trinoConfig struct {
	Host           string
	Port           int
	User           string
	Password       string
	Catalog        string
	Schema         string
	SSL            bool
	SSLVerify      bool
	Timeout        time.Duration
	DefaultLimit   int
	MaxLimit       int
	ReadOnly       bool
	ConnectionName string
}

// s3Config holds extracted S3 configuration.
type s3Config struct {
	Region         string
	Endpoint       string
	AccessKeyID    string
	SecretKey      string
	BucketPrefix   string
	ConnectionName string
}

// getDataHubConfig extracts DataHub configuration from toolkits config.
func (p *Platform) getDataHubConfig(instanceName string) *datahubConfig {
	instanceCfg := p.getInstanceConfig("datahub", instanceName)
	if instanceCfg == nil {
		return nil
	}

	cfg := &datahubConfig{
		URL:     cfgString(instanceCfg, "url"),
		Token:   cfgString(instanceCfg, "token"),
		Timeout: cfgDuration(instanceCfg, "timeout", 30*time.Second),
		Debug:   cfgBoolDefault(instanceCfg, "debug", false),
	}

	// Support both "url" and "endpoint" keys
	if cfg.URL == "" {
		cfg.URL = cfgString(instanceCfg, "endpoint")
	}

	return cfg
}

// getTrinoConfig extracts Trino configuration from toolkits config.
func (p *Platform) getTrinoConfig(instanceName string) *trinoConfig {
	instanceCfg := p.getInstanceConfig("trino", instanceName)
	if instanceCfg == nil {
		return nil
	}

	return &trinoConfig{
		Host:           cfgString(instanceCfg, "host"),
		Port:           cfgInt(instanceCfg, "port", defaultTrinoPort),
		User:           cfgString(instanceCfg, "user"),
		Password:       cfgString(instanceCfg, "password"),
		Catalog:        cfgString(instanceCfg, "catalog"),
		Schema:         cfgString(instanceCfg, "schema"),
		SSL:            cfgBool(instanceCfg, "ssl"),
		SSLVerify:      cfgBoolDefault(instanceCfg, "ssl_verify", true),
		Timeout:        cfgDuration(instanceCfg, "timeout", 120*time.Second),
		DefaultLimit:   cfgInt(instanceCfg, "default_limit", defaultTrinoQueryLimit),
		MaxLimit:       cfgInt(instanceCfg, "max_limit", defaultTrinoMaxLimit),
		ReadOnly:       cfgBool(instanceCfg, "read_only"),
		ConnectionName: cfgString(instanceCfg, "connection_name"),
	}
}

// getS3Config extracts S3 configuration from toolkits config.
func (p *Platform) getS3Config(instanceName string) *s3Config {
	instanceCfg := p.getInstanceConfig("s3", instanceName)
	if instanceCfg == nil {
		return nil
	}

	cfg := &s3Config{
		Region:         cfgString(instanceCfg, "region"),
		Endpoint:       cfgString(instanceCfg, "endpoint"),
		AccessKeyID:    cfgString(instanceCfg, "access_key_id"),
		SecretKey:      cfgString(instanceCfg, "secret_access_key"),
		BucketPrefix:   cfgString(instanceCfg, "bucket_prefix"),
		ConnectionName: cfgString(instanceCfg, "connection_name"),
	}

	if cfg.ConnectionName == "" {
		cfg.ConnectionName = instanceName
	}

	return cfg
}

// getInstanceConfig retrieves instance configuration from toolkits config.
func (p *Platform) getInstanceConfig(toolkitKind, instanceName string) map[string]any {
	toolkitsCfg, ok := p.config.Toolkits[toolkitKind]
	if !ok {
		return nil
	}

	kindCfg, ok := toolkitsCfg.(map[string]any)
	if !ok {
		return nil
	}

	instances, ok := kindCfg["instances"].(map[string]any)
	if !ok {
		return nil
	}

	// If no instance name specified, try to get the default
	if instanceName == "" {
		instanceName = resolveDefaultInstance(kindCfg, instances)
	}

	instanceCfg, ok := instances[instanceName].(map[string]any)
	if !ok {
		return nil
	}

	return instanceCfg
}

// resolveDefaultInstance determines which instance to use.
func resolveDefaultInstance(kindCfg, instances map[string]any) string {
	if defaultName, ok := kindCfg["default"].(string); ok {
		return defaultName
	}
	// Use the first instance
	for name := range instances {
		return name
	}
	return ""
}

// Configuration extraction helpers.

func cfgString(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

func cfgInt(cfg map[string]any, key string, defaultVal int) int {
	if v, ok := cfg[key].(int); ok {
		return v
	}
	if v, ok := cfg[key].(float64); ok {
		return int(v)
	}
	return defaultVal
}

func cfgBool(cfg map[string]any, key string) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return false
}

func cfgBoolDefault(cfg map[string]any, key string, defaultVal bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return defaultVal
}

func cfgDuration(cfg map[string]any, key string, defaultVal time.Duration) time.Duration {
	if v, ok := cfg[key].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if v, ok := cfg[key].(int); ok {
		return time.Duration(v) * time.Second
	}
	if v, ok := cfg[key].(float64); ok {
		return time.Duration(v) * time.Second
	}
	return defaultVal
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

	if p.sessionCache != nil {
		p.sessionCache.Stop()
	}

	closeResource(&errs, p.semanticProvider)
	closeResource(&errs, p.queryProvider)
	closeResource(&errs, p.storageProvider)
	closeResource(&errs, p.toolkitRegistry)

	if closer, ok := p.auditLogger.(Closer); ok {
		closeResource(&errs, closer)
	}

	// Close audit store
	if p.auditStore != nil {
		closeResource(&errs, p.auditStore)
	}

	// Close database connection
	if p.db != nil {
		if err := p.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing database: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing platform: %v", errs)
	}
	return nil
}
