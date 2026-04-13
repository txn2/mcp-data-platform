package platform

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	// PostgreSQL driver for database/sql.
	_ "github.com/lib/pq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	s3client "github.com/txn2/mcp-s3/pkg/client"
	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/apps"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	configpostgres "github.com/txn2/mcp-data-platform/pkg/configstore/postgres"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/mcpapps"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/oauth"
	oauthpostgres "github.com/txn2/mcp-data-platform/pkg/oauth/postgres"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	promptpostgres "github.com/txn2/mcp-data-platform/pkg/prompt/postgres"
	"github.com/txn2/mcp-data-platform/pkg/query"
	trinoquery "github.com/txn2/mcp-data-platform/pkg/query/trino"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
	"github.com/txn2/mcp-data-platform/pkg/session"
	sessionpostgres "github.com/txn2/mcp-data-platform/pkg/session/postgres"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	s3storage "github.com/txn2/mcp-data-platform/pkg/storage/s3"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// providerNoop is the provider name for no-op (disabled) providers.
const providerNoop = "noop"

// toolkitKindTrino is the toolkit kind name for Trino.
const toolkitKindTrino = "trino"

// cfgKeyEnabled is the config map key for the "enabled" flag.
const cfgKeyEnabled = "enabled"

// cfgKeyInstances is the config map key for toolkit instances.
const cfgKeyInstances = "instances"

// minSigningKeyLength is the minimum length in bytes for an OAuth signing key.
const minSigningKeyLength = 32

// logKeyCount is the slog key for item counts in log messages.
const logKeyCount = "count"

// builtinPlatformInfoName is the canonical name for the built-in platform-info MCP app.
const builtinPlatformInfoName = "platform-info"

// defaultTrinoPort is the default port for Trino connections.
const defaultTrinoPort = 8080

// defaultTrinoQueryLimit is the default query limit for Trino connections.
const defaultTrinoQueryLimit = 1000

// defaultTrinoMaxLimit is the maximum query limit for Trino connections.
const defaultTrinoMaxLimit = 10000

// logKeyError is the slog key for error values in log messages.
const logKeyError = "error"

// Source constants for personas and other config resources.
const (
	SourceFile     = "file"
	SourceDatabase = "database" //nolint:goconst // same value as SessionStoreDatabase but different semantic domain
	SourceBoth     = "both"
)

// Platform is the main platform facade.
type Platform struct {
	config *Config

	// Core components
	mcpServer *mcp.Server
	lifecycle *Lifecycle

	// Database
	db         *sql.DB
	auditStore *auditpostgres.Store

	// Config store
	configStore       configstore.Store
	fileDefaults      map[string]string
	connectionStore   ConnectionStore
	connectionSources *ConnectionSourceMap
	personaStore      PersonaStore
	apiKeyStore       APIKeyStore

	// Providers
	semanticProvider semantic.Provider
	queryProvider    query.Provider
	storageProvider  storage.Provider

	// Registries
	toolkitRegistry  *registry.Registry
	personaRegistry  *persona.Registry
	filePersonaNames map[string]bool // names of personas loaded from config file

	// Auth
	authenticator middleware.Authenticator
	authorizer    middleware.Authorizer
	apiKeyAuth    *auth.APIKeyAuthenticator

	// OAuth
	oauthServer      *oauth.Server
	oauthSigningKey  []byte
	oauthStoreCloser interface{ Close() error }

	// Browser session (OIDC login flow + cookie-based auth)
	browserSessionFlow *browsersession.Flow
	browserSessionAuth *browsersession.Authenticator

	// Audit
	auditLogger middleware.AuditLogger

	// Session management
	sessionStore session.Store
	sessionCache *middleware.SessionEnrichmentCache

	// Tuning
	ruleEngine    *tuning.RuleEngine
	promptManager *tuning.PromptManager

	// Knowledge stores (exposed for admin API)
	knowledgeInsightStore   knowledgekit.InsightStore
	knowledgeChangesetStore knowledgekit.ChangesetStore
	knowledgeDataHubWriter  knowledgekit.DataHubWriter

	// Memory layer
	memoryStore      memory.Store
	embeddingProv    embedding.Provider
	stalenessWatcher *memory.StalenessWatcher
	memoryAdapter    middleware.MemoryProvider

	// Portal stores (exposed for REST API in Phase 3)
	portalAssetStore        portal.AssetStore
	portalShareStore        portal.ShareStore
	portalVersionStore      portal.VersionStore
	portalCollectionStore   portal.CollectionStore
	portalS3Client          portal.S3Client
	provenanceTracker       *middleware.ProvenanceTracker
	resolvedBrandLogoSVG    string // cached SVG from portal.logo or mcpapps config
	resolvedBrandURL        string // cached brand_url from mcpapps platform-info config
	resolvedImplementorLogo string // cached SVG fetched from portal.implementor.logo

	// Workflow gating
	workflowTracker *middleware.SessionWorkflowTracker

	// Session gate
	sessionGate *middleware.SessionGate

	// Resource store (managed resources)
	resourceStore    resource.Store
	resourceS3Client resource.S3Client

	// Prompt store + metadata collected during registration
	promptStore   prompt.Store
	promptInfosMu sync.RWMutex
	promptInfos   []registry.PromptInfo

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
	// Initialize data infrastructure first (database + config store)
	if err := p.initDataInfra(); err != nil {
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
	p.loadDBAPIKeys()
	// Initialize audit logging after auth
	if err := p.initAudit(opts); err != nil {
		return err
	}
	if err := p.initSessions(opts); err != nil {
		return err
	}
	if err := p.initOAuth(); err != nil {
		return err
	}
	p.initTuning(opts)
	p.initWorkflow()
	p.initSessionGate()
	if err := p.initExtensions(); err != nil {
		return err
	}
	p.finalizeSetup()
	p.LoadManagedResources()
	return nil
}

// initDataInfra initializes the database and config store.
func (p *Platform) initDataInfra() error {
	if err := p.initDatabase(); err != nil {
		return err
	}
	if err := p.initConnectionStore(); err != nil {
		return err
	}
	p.initPersonaStore()
	p.initAPIKeyStore()
	p.initPromptStore()
	return p.initConfigStore()
}

// initExtensions initializes optional extension toolkits and apps.
func (p *Platform) initExtensions() error {
	// Memory must init before knowledge so the memory store is available
	// for the knowledge toolkit's memory adapter.
	if err := p.initMemory(); err != nil {
		return err
	}
	if err := p.initKnowledge(); err != nil {
		return err
	}
	if err := p.initPortal(); err != nil {
		return err
	}
	if err := p.initManagedResources(); err != nil {
		return err
	}
	return p.initMCPApps()
}

// initMemory initializes the memory layer: store, embedder, toolkit, staleness watcher.
func (p *Platform) initMemory() error {
	if isExplicitlyDisabled(p.config.Memory.Enabled) || p.db == nil {
		return nil
	}

	// 1. Create memory store.
	p.memoryStore = memory.NewPostgresStore(p.db)

	// 2. Create embedding provider.
	switch p.config.Memory.Embedding.Provider {
	case "ollama":
		p.embeddingProv = embedding.NewOllamaProvider(embedding.OllamaConfig{
			URL:     p.config.Memory.Embedding.Ollama.URL,
			Model:   p.config.Memory.Embedding.Ollama.Model,
			Timeout: p.config.Memory.Embedding.Ollama.Timeout,
		})
	default:
		p.embeddingProv = embedding.NewNoopProvider(embedding.DefaultDimension)
	}

	// 3. Create and register memory toolkit.
	tk, err := memorykit.New("default", p.memoryStore, p.embeddingProv)
	if err != nil {
		return fmt.Errorf("creating memory toolkit: %w", err)
	}
	if err := p.toolkitRegistry.Register(tk); err != nil {
		return fmt.Errorf("registering memory toolkit: %w", err)
	}

	// 4. Create middleware adapter for cross-injection.
	p.memoryAdapter = &memoryMiddlewareBridge{store: p.memoryStore}

	// 5. Start staleness watcher if configured.
	if p.config.Memory.Staleness.Enabled && p.semanticProvider != nil {
		p.stalenessWatcher = memory.NewStalenessWatcher(
			p.memoryStore, p.semanticProvider,
			memory.StalenessConfig{
				Interval:  p.config.Memory.Staleness.Interval,
				BatchSize: p.config.Memory.Staleness.BatchSize,
			},
		)
		p.stalenessWatcher.Start(context.Background())
	}

	slog.Info("memory layer enabled",
		"embedding_provider", p.config.Memory.Embedding.Provider,
		"staleness_enabled", p.config.Memory.Staleness.Enabled)
	return nil
}

// memoryMiddlewareBridge adapts memory.Store to middleware.MemoryProvider,
// converting between memory.Snippet and middleware.MemorySnippet.
type memoryMiddlewareBridge struct {
	store memory.Store
}

// RecallForEntities converts memory snippets to middleware format.
func (b *memoryMiddlewareBridge) RecallForEntities(ctx context.Context, urns []string, personaName string, limit int) ([]middleware.MemorySnippet, error) {
	adapter := memory.NewMiddlewareAdapter(b.store)
	memSnippets, err := adapter.RecallForEntities(ctx, urns, personaName, limit)
	if err != nil {
		return nil, fmt.Errorf("recalling memories for entities: %w", err)
	}

	snippets := make([]middleware.MemorySnippet, len(memSnippets))
	for i, ms := range memSnippets {
		snippets[i] = middleware.MemorySnippet{
			ID:         ms.ID,
			Content:    ms.Content,
			Dimension:  ms.Dimension,
			Category:   ms.Category,
			Confidence: ms.Confidence,
			CreatedAt:  ms.CreatedAt,
		}
	}
	return snippets, nil
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

// initConnectionStore initializes the connection instance store.
// When a database is available, it uses PostgreSQL with field-level encryption
// for sensitive config values. The encryption key comes from ENCRYPTION_KEY env var.
func (p *Platform) initConnectionStore() error {
	if p.db == nil {
		p.connectionStore = &NoopConnectionStore{}
		slog.Info("connection store: noop (no database)")
		return nil
	}

	encryptor, err := buildFieldEncryptor()
	if err != nil {
		return err
	}
	p.connectionStore = NewPostgresConnectionStore(p.db, encryptor)
	return nil
}

// initPersonaStore initializes the persona definition store.
func (p *Platform) initPersonaStore() {
	if p.db != nil {
		p.personaStore = NewPostgresPersonaStore(p.db)
		slog.Info("persona store: postgres")
	} else {
		p.personaStore = &NoopPersonaStore{}
		slog.Info("persona store: noop (no database)")
	}
}

// initAPIKeyStore initializes the API key definition store.
func (p *Platform) initAPIKeyStore() {
	if p.db != nil {
		p.apiKeyStore = NewPostgresAPIKeyStore(p.db)
		slog.Info("api key store: postgres")
	} else {
		p.apiKeyStore = &NoopAPIKeyStore{}
		slog.Info("api key store: noop (no database)")
	}
}

// initPromptStore initializes the prompt definition store.
func (p *Platform) initPromptStore() {
	if p.db != nil {
		p.promptStore = promptpostgres.New(p.db)
		slog.Info("prompt store: postgres")
	}
}

// buildFieldEncryptor creates a FieldEncryptor from the ENCRYPTION_KEY env var.
// The key can be provided as hex (64 hex chars), base64 (44 chars), or raw bytes (32 bytes).
// Returns nil encryptor (encryption disabled) if the env var is not set.
func buildFieldEncryptor() (*FieldEncryptor, error) {
	keyStr := os.Getenv("ENCRYPTION_KEY")
	if keyStr == "" {
		slog.Warn("connection store: ENCRYPTION_KEY not set — sensitive fields stored in plain text")
		return nil, nil //nolint:nilnil // nil encryptor = encryption disabled
	}

	key := decodeEncryptionKey(keyStr)

	encryptor, err := NewFieldEncryptor(key)
	if err != nil {
		return nil, fmt.Errorf("initializing field encryptor: %w", err)
	}
	slog.Info("connection store: encryption enabled")
	return encryptor, nil
}

// decodeEncryptionKey tries hex, then base64, then raw bytes to decode the key.
func decodeEncryptionKey(keyStr string) []byte {
	// Try hex first (64 hex chars = 32 bytes).
	if key, err := hex.DecodeString(keyStr); err == nil && len(key) == aes256KeyLength {
		return key
	}

	// Try base64 (44 chars = 32 bytes).
	if key, err := base64.StdEncoding.DecodeString(keyStr); err == nil && len(key) == aes256KeyLength {
		return key
	}

	// Fall back to raw bytes.
	return []byte(keyStr)
}

// initConfigStore initializes the config store. When a database is available,
// DB entries override file config for whitelisted keys with hot-reload support.
func (p *Platform) initConfigStore() error {
	// Capture file defaults before any DB overlay.
	p.fileDefaults = p.buildConfigEntryMap()

	if p.db != nil {
		store := configpostgres.New(p.db)
		entries, err := store.List(context.Background())
		if err != nil {
			return fmt.Errorf("loading config entries from database: %w", err)
		}
		for _, e := range entries {
			p.applyConfigEntry(e.Key, e.Value)
		}
		p.configStore = store
		if len(entries) > 0 {
			slog.Info("config store: applied database overrides", logKeyCount, len(entries))
		}
	} else {
		p.configStore = configstore.NewFileStore(p.fileDefaults)
	}
	return nil
}

// buildConfigEntryMap extracts the current whitelisted config values as a key/value map.
func (p *Platform) buildConfigEntryMap() map[string]string {
	return map[string]string{
		"server.description":        p.config.Server.Description,
		"server.agent_instructions": p.config.Server.AgentInstructions,
	}
}

// applyConfigEntry updates a live config field for a whitelisted key.
func (p *Platform) applyConfigEntry(key, value string) {
	p.config.ApplyConfigEntry(key, value)
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
		p.loadDBPersonas()
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

	// Inject platform-level config into toolkit instances before loading.
	p.injectToolkitPlatformConfig()

	// Merge DB connection instances into toolkit config before loading.
	p.mergeDBConnectionsIntoConfig()

	// Load toolkits from configuration (file + DB merged)
	if p.config.Toolkits != nil {
		loader := registry.NewLoader(p.toolkitRegistry)
		if err := loader.LoadFromMap(p.config.Toolkits); err != nil {
			return fmt.Errorf("loading toolkits: %w", err)
		}
	}

	// Build the connection→DataHub source mapping for semantic enrichment.
	p.connectionSources = p.buildConnectionSourceMap()

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

	// Initialize browser session (OIDC login + cookie auth) when enabled.
	if err := p.initBrowserSession(); err != nil {
		return fmt.Errorf("initializing browser session: %w", err)
	}

	return nil
}

// initBrowserSession sets up OIDC login flow and cookie authenticator.
func (p *Platform) initBrowserSession() error {
	bsCfg := p.config.Auth.BrowserSession
	if !bsCfg.Enabled || !p.config.Auth.OIDC.Enabled {
		return nil
	}

	keyBytes, err := base64.StdEncoding.DecodeString(bsCfg.SigningKey)
	if err != nil {
		return fmt.Errorf("decoding browser session signing key: %w", err)
	}

	cookieCfg := browsersession.CookieConfig{
		Name:   bsCfg.CookieName,
		Domain: bsCfg.Domain,
		Secure: bsCfg.Secure,
		TTL:    bsCfg.TTL,
		Key:    keyBytes,
	}

	oidcCfg := p.config.Auth.OIDC

	// Build redirect URI from portal public base URL.
	redirectURI := p.config.Portal.PublicBaseURL + "/portal/auth/callback"

	flowCfg := browsersession.FlowConfig{
		Issuer:             oidcCfg.Issuer,
		ClientID:           oidcCfg.ClientID,
		ClientSecret:       oidcCfg.ClientSecret,
		RedirectURI:        redirectURI,
		Scopes:             oidcCfg.Scopes,
		RoleClaim:          oidcCfg.RoleClaimPath,
		RolePrefix:         oidcCfg.RolePrefix,
		Cookie:             cookieCfg,
		PostLoginRedirect:  browsersession.DefaultPortalPath,
		PostLogoutRedirect: p.config.Portal.PublicBaseURL + browsersession.DefaultPortalPath,
	}

	flow, err := browsersession.NewFlow(context.Background(), flowCfg)
	if err != nil {
		return fmt.Errorf("creating OIDC flow: %w", err)
	}

	p.browserSessionFlow = flow
	p.browserSessionAuth = browsersession.NewAuthenticator(cookieCfg)

	slog.Info("browser session enabled",
		"issuer", oidcCfg.Issuer,
		"redirect_uri", redirectURI,
	)

	return nil
}

// initAudit initializes audit logging.
func (p *Platform) initAudit(opts *Options) error {
	// Use provided audit logger if available
	if opts.AuditLogger != nil {
		p.auditLogger = opts.AuditLogger
		return nil
	}

	// Audit is enabled by default when a database is available.
	if isExplicitlyDisabled(p.config.Audit.Enabled) || p.db == nil {
		p.auditLogger = &middleware.NoopAuditLogger{}
		return nil
	}

	// Create PostgreSQL audit store
	store := auditpostgres.New(p.db, auditpostgres.Config{
		RetentionDays: p.config.Audit.RetentionDays,
	})

	// Start background cleanup routine
	store.StartCleanupRoutine(24 * time.Hour)

	p.auditStore = store
	p.auditLogger = middleware.NewAuditStoreAdapter(store)

	slog.Info("audit logging enabled",
		"retention_days", p.config.Audit.RetentionDays,
		"log_tool_calls", p.config.Audit.LogToolCalls,
	)
	return nil
}

// initSessions initializes the session store based on configuration.
func (p *Platform) initSessions(opts *Options) error {
	if opts.SessionStore != nil {
		p.sessionStore = opts.SessionStore
		return nil
	}

	ttl := p.config.Sessions.TTL
	if ttl == 0 {
		ttl = defaultSessionTimeout
	}
	cleanupInterval := p.config.Sessions.CleanupInterval
	if cleanupInterval == 0 {
		cleanupInterval = defaultCleanupInterval
	}

	switch p.config.Sessions.Store {
	case SessionStoreDatabase:
		if p.db == nil {
			return fmt.Errorf("sessions.store is \"database\" but no database configured")
		}
		store := sessionpostgres.New(p.db, sessionpostgres.Config{TTL: ttl})
		store.StartCleanupRoutine(cleanupInterval)
		p.sessionStore = store
		// Force stateless mode so the SDK skips its built-in session map.
		p.config.Server.Streamable.Stateless = true
		slog.Info("session store: database (stateless mode enabled)",
			"ttl", ttl, "cleanup_interval", cleanupInterval)
	case SessionStoreMemory, "":
		store := session.NewMemoryStore(ttl)
		store.StartCleanupRoutine(cleanupInterval)
		p.sessionStore = store
		slog.Info("session store: memory",
			"ttl", ttl, "cleanup_interval", cleanupInterval)
	default:
		return fmt.Errorf("unknown session store: %q", p.config.Sessions.Store)
	}

	return nil
}

// initOAuth initializes the OAuth server if enabled.
func (p *Platform) initOAuth() error {
	if !p.config.OAuth.Enabled {
		return nil
	}

	// Create storage: use PostgreSQL if database is available, otherwise in-memory.
	var oauthStorage oauth.Storage
	if p.db != nil {
		pgStore := oauthpostgres.New(p.db)
		pgStore.StartCleanupRoutine(time.Minute)
		p.oauthStoreCloser = pgStore
		oauthStorage = pgStore
		slog.Info("OAuth storage: database")
	} else {
		oauthStorage = oauth.NewMemoryStorage()
		slog.Info("OAuth storage: memory")
	}

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
			CreatedAt:    time.Now(),
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
}

// initWorkflow initializes the session workflow tracker if configured.
func (p *Platform) initWorkflow() {
	if !p.config.Workflow.RequireDiscoveryBeforeQuery {
		return
	}

	sessionTimeout := p.config.Server.Streamable.SessionTimeout
	if sessionTimeout == 0 {
		sessionTimeout = defaultSessionTimeout
	}

	p.workflowTracker = middleware.NewSessionWorkflowTracker(
		p.config.Workflow.DiscoveryTools,
		p.config.Workflow.QueryTools,
		sessionTimeout,
	)
	p.workflowTracker.StartCleanup(1 * time.Minute)

	slog.Info("workflow gating enabled",
		"discovery_tools", len(p.workflowTracker.DiscoveryToolNames()),
		"query_tools", len(p.workflowTracker.QueryToolNames()),
		"escalation_after", p.config.Workflow.Escalation.AfterWarnings,
	)
}

// initSessionGate initializes the session initialization gate if configured.
func (p *Platform) initSessionGate() {
	if !p.config.SessionGate.Enabled {
		return
	}

	sessionTTL := p.config.Sessions.TTL
	if sessionTTL == 0 {
		sessionTTL = p.config.Server.Streamable.SessionTimeout
	}

	p.sessionGate = middleware.NewSessionGate(middleware.SessionGateConfig{
		InitTool:        p.config.SessionGate.InitTool,
		ExemptTools:     p.config.SessionGate.ExemptTools,
		SessionTTL:      sessionTTL,
		CleanupInterval: p.config.Sessions.CleanupInterval,
	})
	p.sessionGate.StartCleanup(p.config.Sessions.CleanupInterval)

	slog.Info("session gate enabled",
		"init_tool", p.config.SessionGate.InitTool,
		"exempt_tools", p.config.SessionGate.ExemptTools,
		"session_ttl", sessionTTL,
	)
}

// initKnowledge initializes the knowledge capture toolkit if enabled.
// Knowledge tools require database persistence — without a database the
// toolkit is not registered and its tools won't appear in tools/list.
func (p *Platform) initKnowledge() error {
	if isExplicitlyDisabled(p.config.Knowledge.Enabled) || p.db == nil {
		return nil
	}

	// Use memory-backed adapter when memory store is available (migration
	// drops knowledge_insights in favor of memory_records).
	var store knowledgekit.InsightStore
	if p.memoryStore != nil {
		store = knowledgekit.NewMemoryInsightAdapter(p.memoryStore)
	} else {
		store = knowledgekit.NewPostgresStore(p.db)
	}
	p.knowledgeInsightStore = store

	tk, err := knowledgekit.New("default", store)
	if err != nil {
		return fmt.Errorf("creating knowledge toolkit: %w", err)
	}

	// Wire memory store for embedding generation on capture_insight.
	if p.memoryStore != nil && p.embeddingProv != nil {
		tk.SetMemoryStore(p.memoryStore, p.embeddingProv)
	}

	// Configure apply_knowledge tool if enabled.
	if err := p.configureKnowledgeApply(tk); err != nil {
		return err
	}

	// Wire prompt creator for add_prompt change type
	if p.promptStore != nil {
		tk.SetPromptCreator(&platformPromptCreator{store: p.promptStore, platform: p})
	}

	if err := p.toolkitRegistry.Register(tk); err != nil {
		return fmt.Errorf("registering knowledge toolkit: %w", err)
	}

	slog.Info("knowledge capture enabled")
	return nil
}

// configureKnowledgeApply sets up the apply_knowledge tool dependencies if enabled.
func (p *Platform) configureKnowledgeApply(tk *knowledgekit.Toolkit) error {
	if !p.config.Knowledge.Apply.Enabled {
		return nil
	}

	csStore := knowledgekit.NewPostgresChangesetStore(p.db)
	p.knowledgeChangesetStore = csStore

	writer, err := p.createDataHubWriter()
	if err != nil {
		return fmt.Errorf("creating datahub writer: %w", err)
	}
	p.knowledgeDataHubWriter = writer

	tk.SetApplyConfig(knowledgekit.ApplyConfig{
		Enabled:             true,
		DataHubConnection:   p.config.Knowledge.Apply.DataHubConnection,
		RequireConfirmation: p.config.Knowledge.Apply.RequireConfirmation,
	}, csStore, writer)

	slog.Info("knowledge apply enabled",
		"datahub_connection", p.config.Knowledge.Apply.DataHubConnection,
		"require_confirmation", p.config.Knowledge.Apply.RequireConfirmation,
	)
	return nil
}

// createDataHubWriter creates a DataHubWriter backed by a real DataHub client
// when a datahub_connection is configured, or falls back to a noop writer.
func (p *Platform) createDataHubWriter() (knowledgekit.DataHubWriter, error) {
	connName := p.config.Knowledge.Apply.DataHubConnection
	dhCfg := p.getDataHubConfig(connName)
	if dhCfg == nil {
		slog.Warn("knowledge apply: datahub connection not found, using noop writer",
			"connection", connName)
		return &knowledgekit.NoopDataHubWriter{}, nil
	}

	clientCfg := dhclient.DefaultConfig()
	clientCfg.URL = dhCfg.URL
	clientCfg.Token = dhCfg.Token
	clientCfg.Timeout = dhCfg.Timeout
	clientCfg.Debug = dhCfg.Debug

	c, err := dhclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating datahub client for connection %q: %w", connName, err)
	}

	slog.Info("knowledge apply: using datahub writer", "connection", connName)
	return knowledgekit.NewDataHubClientWriter(c), nil
}

// initPortal initializes the asset portal toolkit if enabled.
// The portal requires a database for metadata and an S3 connection for content storage.
func (p *Platform) initPortal() error {
	if isExplicitlyDisabled(p.config.Portal.Enabled) || p.db == nil {
		return nil
	}

	// Create stores
	p.portalAssetStore = portal.NewPostgresAssetStore(p.db)
	p.portalShareStore = portal.NewPostgresShareStore(p.db)
	p.portalVersionStore = portal.NewPostgresVersionStore(p.db)
	p.portalCollectionStore = portal.NewPostgresCollectionStore(p.db)

	// Create S3 client from referenced S3 connection
	var s3Client portal.S3Client
	if p.config.Portal.S3Connection != "" {
		var clientErr error
		s3Client, clientErr = p.createPortalS3Client()
		if clientErr != nil {
			return fmt.Errorf("creating portal S3 client: %w", clientErr)
		}
		p.portalS3Client = s3Client
	} else {
		slog.Warn("portal: no s3_connection configured; artifacts will be saved to database only")
	}

	// Create provenance tracker
	p.provenanceTracker = middleware.NewProvenanceTracker()

	// Create and register toolkit
	tk := portalkit.New(portalkit.Config{
		Name:            "default",
		AssetStore:      p.portalAssetStore,
		ShareStore:      p.portalShareStore,
		VersionStore:    p.portalVersionStore,
		CollectionStore: p.portalCollectionStore,
		S3Client:        s3Client,
		S3Bucket:        p.config.Portal.S3Bucket,
		S3Prefix:        p.config.Portal.S3Prefix,
		BaseURL:         p.config.Portal.PublicBaseURL,
		MaxContentSize:  p.config.Portal.MaxContentSize,
	})

	if err := p.toolkitRegistry.Register(tk); err != nil {
		return fmt.Errorf("registering portal toolkit: %w", err)
	}

	slog.Info("portal enabled",
		"s3_connection", p.config.Portal.S3Connection,
		"s3_bucket", p.config.Portal.S3Bucket,
	)
	return nil
}

// createPortalS3Client creates an S3Client from the referenced S3 connection config.
func (p *Platform) createPortalS3Client() (portal.S3Client, error) {
	connName := p.config.Portal.S3Connection
	s3Cfg := p.getS3Config(connName)
	if s3Cfg == nil {
		return nil, fmt.Errorf("s3 connection %q not found in toolkits config", connName)
	}

	clientCfg := &s3client.Config{
		Region:          s3Cfg.Region,
		Endpoint:        s3Cfg.Endpoint,
		AccessKeyID:     s3Cfg.AccessKeyID,
		SecretAccessKey: s3Cfg.SecretKey,
		Name:            s3Cfg.ConnectionName,
		UsePathStyle:    s3Cfg.UsePathStyle,
	}

	c, err := s3client.New(context.Background(), clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating s3 client for connection %q: %w", connName, err)
	}

	slog.Info("portal: using s3 connection", "connection", connName)
	return portal.NewS3ClientAdapter(c), nil
}

// initManagedResources initializes the managed resources subsystem (human-uploaded
// reference material stored in S3 with metadata in PostgreSQL).
func (p *Platform) initManagedResources() error {
	if isExplicitlyDisabled(p.config.Resources.Managed.Enabled) || p.db == nil {
		return nil
	}

	p.resourceStore = resource.NewPostgresStore(p.db)

	// Create S3 client from referenced or default S3 connection.
	if connName := p.managedResourceS3Connection(); connName != "" {
		s3Cfg := p.getS3Config(connName)
		if s3Cfg == nil {
			return fmt.Errorf("resource s3 connection %q not found in toolkits config", connName)
		}

		clientCfg := &s3client.Config{
			Region:          s3Cfg.Region,
			Endpoint:        s3Cfg.Endpoint,
			AccessKeyID:     s3Cfg.AccessKeyID,
			SecretAccessKey: s3Cfg.SecretKey,
			Name:            s3Cfg.ConnectionName,
			UsePathStyle:    s3Cfg.UsePathStyle,
		}

		c, err := s3client.New(context.Background(), clientCfg)
		if err != nil {
			return fmt.Errorf("creating resource s3 client for connection %q: %w", connName, err)
		}

		p.resourceS3Client = portal.NewS3ClientAdapter(c)
	} else {
		slog.Warn("managed resources: no s3_connection configured; blob storage disabled")
	}

	slog.Info("managed resources enabled",
		"s3_connection", p.managedResourceS3Connection(),
		"s3_bucket", p.config.Resources.Managed.S3Bucket,
		"uri_scheme", p.managedResourceURIScheme(),
	)
	return nil
}

// managedResourceURIScheme returns the configured URI scheme or the default.
func (p *Platform) managedResourceURIScheme() string {
	if s := p.config.Resources.Managed.URIScheme; s != "" {
		return s
	}
	return resource.DefaultURIScheme
}

// managedResourceS3Connection returns the S3 connection name for managed
// resources. Returns the explicit config value if set, otherwise falls back
// to the default/first S3 toolkit instance.
func (p *Platform) managedResourceS3Connection() string {
	name := p.config.Resources.Managed.S3Connection
	if name != "" {
		return name
	}
	// No explicit s3_connection — resolve the default S3 toolkit instance
	// so managed resources automatically use an available S3 backend.
	resolved := p.resolveDefaultS3Instance()
	if resolved == "" {
		slog.Debug("managed resources: no S3 toolkit available for default resolution")
		return ""
	}
	slog.Debug("managed resources: using default S3 connection", "s3_connection", resolved)
	return resolved
}

// resolveDefaultS3Instance returns the name of the default/first S3 toolkit
// instance, or "" if no S3 toolkit is configured.
func (p *Platform) resolveDefaultS3Instance() string {
	toolkitsCfg, ok := p.config.Toolkits["s3"]
	if !ok {
		return ""
	}
	kindCfg, ok := toolkitsCfg.(map[string]any)
	if !ok {
		return ""
	}
	instances, ok := kindCfg[cfgKeyInstances].(map[string]any)
	if !ok {
		return ""
	}
	return resolveDefaultInstance(kindCfg, instances)
}

// ResourceStore returns the managed resource store (nil if not enabled).
func (p *Platform) ResourceStore() resource.Store {
	return p.resourceStore
}

// ResourceS3Client returns the S3 client for managed resources (nil if not configured).
func (p *Platform) ResourceS3Client() resource.S3Client {
	return p.resourceS3Client
}

// RegisterManagedResource registers a managed resource with the MCP server
// so it appears in the SDK's native resource list. The handler is a no-op —
// the middleware handles the actual resources/read with auth and S3 fetch.
// This also triggers notifications/resources/list_changed for connected clients.
func (p *Platform) RegisterManagedResource(res *resource.Resource) {
	if p.mcpServer == nil || res == nil {
		slog.Debug("RegisterManagedResource: skipping", "server_nil", p.mcpServer == nil, "res_nil", res == nil)
		return
	}
	slog.Debug("RegisterManagedResource: registering with SDK", "uri", res.URI, "name", res.DisplayName)
	p.mcpServer.AddResource(&mcp.Resource{
		URI:         res.URI,
		Name:        res.DisplayName,
		Description: res.Description,
		MIMEType:    res.MIMEType,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Fallback handler — the middleware normally intercepts resources/read
		// before this runs. If we get here, the middleware fell through (auth
		// failure or config issue). Return a placeholder instead of nil to
		// avoid the SDK's "nil information" error.
		slog.Warn("managed resource: SDK fallback handler called (middleware did not intercept)", "uri", req.Params.URI)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: res.MIMEType,
				Text:     "(resource content unavailable — authentication required)",
			}},
		}, nil
	})
}

// UnregisterManagedResource removes a managed resource from the MCP server's
// resource list. This also triggers notifications/resources/list_changed.
func (p *Platform) UnregisterManagedResource(uri string) {
	if p.mcpServer == nil {
		slog.Debug("UnregisterManagedResource: skipping, no server")
		return
	}
	slog.Debug("UnregisterManagedResource: removing from SDK", "uri", uri)
	p.mcpServer.RemoveResources(uri)
}

// LoadManagedResources registers all existing managed resources from the
// database with the MCP server so they're visible on the first resources/list
// call. Called during platform initialization.
func (p *Platform) LoadManagedResources() {
	if p.resourceStore == nil {
		slog.Debug("LoadManagedResources: no resource store, skipping")
		return
	}
	if p.mcpServer == nil {
		slog.Debug("LoadManagedResources: no MCP server, skipping")
		return
	}
	resources, _, err := p.resourceStore.List(context.Background(), resource.Filter{
		Scopes: []resource.ScopeFilter{{Scope: resource.ScopeGlobal}},
		Limit:  1000,
	})
	if err != nil {
		slog.Warn("managed resources: failed to load existing resources", "error", err)
		return
	}
	for i := range resources {
		p.RegisterManagedResource(&resources[i])
	}
	if len(resources) > 0 {
		slog.Info("managed resources: registered existing resources", logKeyCount, len(resources))
	}
}

// initMCPApps initializes MCP Apps support.
func (p *Platform) initMCPApps() error {
	if !p.config.MCPApps.IsEnabled() {
		return nil
	}

	p.mcpAppsRegistry = mcpapps.NewRegistry()

	if err := p.registerBuiltinPlatformInfo(); err != nil {
		return err
	}

	for appName, appCfg := range p.config.MCPApps.Apps {
		if appName == builtinPlatformInfoName {
			// Already registered as built-in (possibly with operator branding applied).
			continue
		}
		if !appCfg.Enabled {
			continue
		}
		if err := p.registerMCPApp(appName, appCfg); err != nil {
			return err
		}
	}

	return nil
}

// registerBuiltinPlatformInfo registers the embedded platform-info app.
// If the operator has a builtinPlatformInfoName entry in config, branding config is
// merged in; an explicit assets_path overrides the embedded HTML entirely.
func (p *Platform) registerBuiltinPlatformInfo() error {
	subFS, err := fs.Sub(apps.PlatformInfo, builtinPlatformInfoName)
	if err != nil {
		return fmt.Errorf("embed %s: %w", builtinPlatformInfoName, err)
	}

	app := &mcpapps.AppDefinition{
		Name:        builtinPlatformInfoName,
		ToolNames:   []string{"platform_info"},
		Content:     subFS,
		EntryPoint:  "index.html",
		ResourceURI: "ui://platform-info",
		CSP: &mcpapps.CSPConfig{
			Permissions: &mcpapps.PermissionsConfig{
				ClipboardWrite: &struct{}{},
			},
		},
	}

	// Merge operator config (branding) if present.
	if cfg, ok := p.config.MCPApps.Apps[builtinPlatformInfoName]; ok {
		if cfg.Config != nil {
			app.Config = cfg.Config
		}
		if cfg.AssetsPath != "" {
			// Operator wants custom HTML — fall back to filesystem.
			app.Content = nil
			app.AssetsPath = cfg.AssetsPath
		}
	}

	// Auto-inject portal logo when the operator hasn't set one explicitly.
	app.Config = p.injectPortalLogo(app.Config)

	if app.AssetsPath != "" {
		if err := app.ValidateAssets(); err != nil {
			return fmt.Errorf("app %s: %w", builtinPlatformInfoName, err)
		}
	}

	if err := p.mcpAppsRegistry.Register(app); err != nil {
		return fmt.Errorf("registering %s app: %w", builtinPlatformInfoName, err)
	}

	slog.Info("registered MCP app", "app", builtinPlatformInfoName, "resource_uri", app.ResourceURI)
	return nil
}

// injectPortalLogo auto-populates the logo in the platform-info app config
// from portal.logo when the operator hasn't set logo_svg or logo_url
// explicitly. When the logo is an SVG URL, it is fetched and inlined as
// logo_svg so the logo renders in sandboxed contexts (MCP App iframes)
// that block external resource loading.
//
// Also caches brand_url from the app config for use by BrandURL().
func (p *Platform) injectPortalLogo(cfg any) any {
	m, ok := cfg.(map[string]any)
	if !ok {
		m = make(map[string]any)
	}

	// Cache brand_url from the mcpapps platform-info config.
	if brandURL, _ := m["brand_url"].(string); brandURL != "" {
		p.resolvedBrandURL = brandURL
	}

	portalLogo := p.config.Portal.Logo
	if portalLogo == "" {
		// Still cache logo_svg if present in the app config.
		if svg, _ := m["logo_svg"].(string); svg != "" {
			p.resolvedBrandLogoSVG = svg
		}
		return m
	}

	if svg, _ := m["logo_svg"].(string); svg != "" {
		p.resolvedBrandLogoSVG = svg
		return m
	}
	if m["logo_url"] != nil {
		return m
	}

	// Fetch SVG content for inline rendering; fall back to URL on failure.
	if svg, err := fetchLogoSVG(portalLogo); err == nil {
		m["logo_svg"] = svg
		p.resolvedBrandLogoSVG = svg
	} else {
		slog.Debug("portal logo fetch failed, using URL", "url", portalLogo, "err", err)
		m["logo_url"] = portalLogo
	}
	return m
}

// logoFetchTimeout is the maximum duration for fetching a portal logo SVG.
const logoFetchTimeout = 10 * time.Second

// logoMaxBytes is the maximum size of fetched logo content (1 MB).
const logoMaxBytes = 1 << 20

// fetchLogoSVG downloads an SVG from the given URL and returns its content.
// Returns an error if the URL is unreachable, returns a non-SVG content type,
// or exceeds the size limit.
func fetchLogoSVG(url string) (string, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("unsupported scheme")
	}

	client := &http.Client{Timeout: logoFetchTimeout}
	resp, err := client.Get(url) //nolint:gosec,noctx // URL comes from operator config, not user input
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "svg") {
		return "", fmt.Errorf("not SVG: %s", ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, logoMaxBytes))
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	return string(body), nil
}

// registerMCPApp creates, validates, and registers a single MCP app.
func (p *Platform) registerMCPApp(appName string, appCfg AppConfig) error {
	app := &mcpapps.AppDefinition{
		Name:       appName,
		ToolNames:  appCfg.Tools,
		AssetsPath: appCfg.AssetsPath,
		EntryPoint: appCfg.EntryPoint,
		Config:     appCfg.Config,
	}

	if app.EntryPoint == "" {
		app.EntryPoint = "index.html"
	}

	if appCfg.ResourceURI != "" {
		app.ResourceURI = appCfg.ResourceURI
	} else {
		app.ResourceURI = fmt.Sprintf("ui://%s", appName)
	}

	if appCfg.CSP != nil {
		app.CSP = convertCSP(appCfg.CSP)
	}

	if err := app.Validate(); err != nil {
		return fmt.Errorf("app %s: %w", appName, err)
	}

	if err := app.ValidateAssets(); err != nil {
		return fmt.Errorf("app %s: %w", appName, err)
	}

	if err := p.mcpAppsRegistry.Register(app); err != nil {
		return fmt.Errorf("registering %s app: %w", appName, err)
	}

	slog.Info("registered MCP app", "app", appName, "resource_uri", app.ResourceURI)
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
	}, &mcp.ServerOptions{
		SchemaCache:  mcp.NewSchemaCache(),
		Capabilities: p.buildServerCapabilities(),
	})

	// Add MCP protocol-level middleware.
	//
	// IMPORTANT: AddReceivingMiddleware wraps the current handler, so each
	// call makes its middleware the new outermost layer. The LAST middleware
	// added runs FIRST. We add innermost middleware first and outermost last.
	//
	// Desired execution order (outermost → innermost → handler):
	//   Tool visibility → Apps metadata → Auth/Authz → Session gate → Audit → Rules → Client logging → Enrichment → handler
	//
	// Therefore we add in reverse (innermost first):

	// 0. Unwrap JSON default (innermost) — injects unwrap_json=true into trino_query
	// and trino_execute arguments so single-row VARCHAR-of-JSON results are returned
	// as parsed objects. Must be innermost so the modified arguments reach the handler.
	if p.config.Injection.IsUnwrapJSONEnabled() {
		p.mcpServer.AddReceivingMiddleware(middleware.MCPUnwrapJSONMiddleware())
	}

	// 1. Semantic enrichment - enriches responses with cross-service context.
	p.addEnrichmentMiddleware()

	// 1.5. Provenance tracking - accumulates tool calls per session for save_artifact
	p.addProvenanceMiddleware()

	// 1.6. Managed resources - injects database-backed resources into resources/list and resources/read
	p.addManagedResourceMiddleware()

	// 2. Client logging - sends enrichment info to client via session.Log()
	if p.config.ClientLogging.Enabled {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPClientLoggingMiddleware(middleware.ClientLoggingConfig{
				Enabled: true,
			}),
		)
	}

	// 3. Rule enforcement - adds operational guidance to responses
	if p.ruleEngine != nil {
		ruleCfg := middleware.RuleEnforcementConfig{
			Engine:          p.ruleEngine,
			WorkflowTracker: p.workflowTracker,
			WorkflowConfig: middleware.WorkflowRulesConfig{
				RequireDiscoveryBeforeQuery: p.config.Workflow.RequireDiscoveryBeforeQuery,
				WarningMessage:              p.config.Workflow.WarningMessage,
				EscalationAfterWarnings:     p.config.Workflow.Escalation.AfterWarnings,
				EscalationMessage:           p.config.Workflow.Escalation.EscalationMessage,
			},
		}
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPRuleEnforcementMiddleware(ruleCfg),
		)
	}

	// 4. Audit - logs tool calls (reads PlatformContext set by Auth/Authz above)
	if !isExplicitlyDisabled(p.config.Audit.Enabled) && p.config.Audit.LogToolCalls {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPAuditMiddleware(p.auditLogger),
		)
	}

	// 5. Session gate - blocks non-exempt tools until platform_info is called.
	// Inner to Auth/Authz so PlatformContext is available; outer to Audit so
	// gated calls don't produce audit events.
	if p.sessionGate != nil {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPSessionGateMiddleware(p.sessionGate),
		)
	}

	// 6. Auth/Authz (outermost for tools/call) - authenticates and authorizes
	// users, creates PlatformContext. Must be outer to Audit so PlatformContext
	// is available in the ctx that Audit receives.
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPToolCallMiddleware(p.authenticator, p.authorizer, p.toolkitRegistry, middleware.ToolCallConfig{
			Transport:       p.config.Server.Transport,
			AdminPersona:    p.config.Admin.Persona,
			WorkflowTracker: p.workflowTracker,
		}),
	)

	// 7. MCP Apps metadata - injects _meta.ui into tools/list
	p.addMCPAppsMiddleware()

	// 8. Tool visibility - reduces tools/list for token savings
	p.addToolVisibilityMiddleware()

	// 8.5 Description overrides - replaces tool descriptions with workflow guidance
	p.addDescriptionOverrideMiddleware()

	// 9. Icons (outermost list decoration) - injects icons into list responses
	p.addIconMiddleware()
}

// addManagedResourceMiddleware registers managed resources middleware when enabled.
func (p *Platform) addManagedResourceMiddleware() {
	if p.resourceStore == nil {
		return
	}
	cfg := middleware.ManagedResourceConfig{
		Store:         p.resourceStore,
		S3Client:      p.resourceS3Client,
		S3Bucket:      p.config.Resources.Managed.S3Bucket,
		URIScheme:     p.managedResourceURIScheme(),
		Authenticator: p.authenticator,
		AdminPersona:  p.config.Admin.Persona,
	}
	// Resolve all persona memberships from roles.
	if p.personaRegistry != nil {
		cfg.PersonasForRoles = personasForRolesFunc(p.personaRegistry)
	}
	p.mcpServer.AddReceivingMiddleware(middleware.MCPManagedResourceMiddleware(cfg))
}

// personasForRolesFunc returns a PersonasForRoles function that resolves
// all persona names a user belongs to from their roles.
func personasForRolesFunc(pr *persona.Registry) middleware.PersonasForRoles {
	return func(roles []string) []string {
		var names []string
		for _, per := range pr.All() {
			for _, r := range per.Roles {
				if slices.Contains(roles, r) {
					names = append(names, per.Name)
					break
				}
			}
		}
		return names
	}
}

// addProvenanceMiddleware registers provenance tracking middleware when portal is enabled.
func (p *Platform) addProvenanceMiddleware() {
	if p.provenanceTracker != nil {
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPProvenanceMiddleware(p.provenanceTracker, "save_artifact"),
		)
	}
}

// addMCPAppsMiddleware registers MCP Apps metadata middleware and UI resources.
func (p *Platform) addMCPAppsMiddleware() {
	if p.mcpAppsRegistry == nil || !p.mcpAppsRegistry.HasApps() {
		return
	}
	p.mcpServer.AddReceivingMiddleware(
		mcpapps.ToolMetadataMiddleware(p.mcpAppsRegistry),
	)
	p.mcpAppsRegistry.RegisterResources(p.mcpServer)
}

// addToolVisibilityMiddleware registers tool visibility filtering middleware.
// Applies both global allow/deny patterns and persona-based tool filtering
// so agents only see tools they're authorized to use.
func (p *Platform) addToolVisibilityMiddleware() {
	cfg := middleware.ToolVisibilityConfig{
		GlobalAllow:   p.config.Tools.Allow,
		GlobalDeny:    p.config.Tools.Deny,
		Authenticator: p.authenticator,
	}

	// Wire persona-based filtering via the authorizer.
	if p.authorizer != nil {
		cfg.IsToolAllowedForPersona = func(ctx context.Context, roles []string, toolName string) bool {
			allowed, _, _ := p.authorizer.IsAuthorized(ctx, "", roles, toolName, "")
			return allowed
		}
	}

	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPToolVisibilityMiddleware(cfg),
	)
}

// addDescriptionOverrideMiddleware registers description override middleware.
// Built-in overrides guide agents toward DataHub discovery; config overrides
// can customize or extend them.
func (p *Platform) addDescriptionOverrideMiddleware() {
	overrides := middleware.MergedDescriptionOverrides(p.config.Tools.DescriptionOverrides)
	if len(overrides) == 0 {
		return
	}
	p.mcpServer.AddReceivingMiddleware(middleware.MCPDescriptionOverrideMiddleware(overrides))
}

// addIconMiddleware registers icon injection middleware when icons are configured.
func (p *Platform) addIconMiddleware() {
	if !p.config.Icons.Enabled {
		return
	}
	cfg := middleware.IconsMiddlewareConfig{
		Tools:     convertIconDefs(p.config.Icons.Tools),
		Resources: convertIconDefs(p.config.Icons.Resources),
		Prompts:   convertIconDefs(p.config.Icons.Prompts),
	}
	p.mcpServer.AddReceivingMiddleware(middleware.MCPIconMiddleware(cfg))
}

// convertIconDefs converts platform IconDef map to middleware IconConfig map.
func convertIconDefs(defs map[string]IconDef) map[string]middleware.IconConfig {
	if len(defs) == 0 {
		return nil
	}
	out := make(map[string]middleware.IconConfig, len(defs))
	for k, v := range defs {
		out[k] = middleware.IconConfig{Source: v.Source, MIMEType: v.MIMEType}
	}
	return out
}

// buildServerCapabilities constructs explicit server capabilities from config.
// This replaces the SDK's auto-inference, making the server's contract visible.
func (p *Platform) buildServerCapabilities() *mcp.ServerCapabilities {
	caps := &mcp.ServerCapabilities{
		// Tools are always available — every platform deployment has at least platform_info.
		Tools: &mcp.ToolCapabilities{},
		// Logging is always available for client logging support.
		Logging: &mcp.LoggingCapabilities{},
	}

	// Resources are available when templates or managed resources are enabled.
	if p.config.Resources.Enabled || p.resourceStore != nil || len(p.config.Resources.Custom) > 0 {
		caps.Resources = &mcp.ResourceCapabilities{ListChanged: true}
	}

	// Prompts are available when configured.
	if len(p.config.Server.Prompts) > 0 || p.config.Tuning.PromptsDir != "" || !isExplicitlyDisabled(p.config.Knowledge.Enabled) {
		caps.Prompts = &mcp.PromptCapabilities{}
	}

	return caps
}

// addEnrichmentMiddleware adds the semantic enrichment middleware if any injection is configured.
func (p *Platform) addEnrichmentMiddleware() {
	needsEnrichment := p.config.Injection.TrinoSemanticEnrichment ||
		p.config.Injection.DataHubQueryEnrichment ||
		p.config.Injection.S3SemanticEnrichment ||
		p.config.Injection.DataHubStorageEnrichment

	if !needsEnrichment {
		return
	}

	enrichCfg := p.buildEnrichmentConfig()
	enrichCfg.WorkflowTracker = p.workflowTracker
	var mp middleware.MemoryProvider
	if p.memoryAdapter != nil {
		mp = p.memoryAdapter
	}
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPSemanticEnrichmentMiddleware(
			p.semanticProvider,
			p.queryProvider,
			p.storageProvider,
			enrichCfg,
			mp,
		),
	)
}

// buildEnrichmentConfig creates the enrichment middleware config, including
// optional session dedup cache setup.
func (p *Platform) buildEnrichmentConfig() middleware.EnrichmentConfig {
	cfg := middleware.EnrichmentConfig{
		EnrichTrinoResults:          p.config.Injection.TrinoSemanticEnrichment,
		EnrichDataHubResults:        p.config.Injection.DataHubQueryEnrichment,
		EnrichS3Results:             p.config.Injection.S3SemanticEnrichment,
		EnrichDataHubStorageResults: p.config.Injection.DataHubStorageEnrichment,
		ResourceLinksEnabled:        p.config.Resources.Enabled,
		ColumnContextFiltering:      p.config.Injection.IsColumnContextFilteringEnabled(),
		SearchSchemaPreview:         p.config.Injection.IsSearchSchemaPreviewEnabled(),
		SchemaPreviewMaxColumns:     p.config.Injection.EffectiveSchemaPreviewMaxColumns(),
	}

	// Wire connection source map lookups as closures to avoid import cycles.
	if p.connectionSources != nil {
		cfg.ForConnection = func(connectionName string) (string, map[string]string) {
			src := p.connectionSources.ForConnectionName(connectionName)
			if src == nil {
				return "", nil
			}
			return src.DataHubSourceName, src.CatalogMapping
		}
		cfg.ConnectionsForURN = func(urn string) []string {
			sources := p.connectionSources.ConnectionsForURN(urn)
			names := make([]string, len(sources))
			for i, s := range sources {
				names[i] = s.Name
			}
			return names
		}
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

		// Restore dedup state from session store (if available)
		p.loadPersistedEnrichmentState()
	}

	return cfg
}

// createSemanticProvider creates the semantic provider based on config.
func (p *Platform) createSemanticProvider() (semantic.Provider, error) {
	switch p.config.Semantic.Provider {
	case kindDataHub:
		// Get DataHub config from toolkits
		datahubCfg := p.getDataHubConfig(p.config.Semantic.Instance)
		if datahubCfg == nil {
			return nil, fmt.Errorf("datahub instance %q not found in toolkits config", p.config.Semantic.Instance)
		}

		// Determine platform for URN building
		platform := p.config.Semantic.URNMapping.Platform
		if platform == "" {
			platform = toolkitKindTrino // Default platform if not configured
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
	case toolkitKindTrino:
		// Get Trino config from toolkits
		trinoCfg := p.getTrinoConfig(p.config.Query.Instance)
		if trinoCfg == nil {
			return nil, fmt.Errorf("trino instance %q not found in toolkits config", p.config.Query.Instance)
		}

		adapter, err := trinoquery.New(trinoquery.Config{
			Host:              trinoCfg.Host,
			Port:              trinoCfg.Port,
			User:              trinoCfg.User,
			Password:          trinoCfg.Password,
			Catalog:           trinoCfg.Catalog,
			Schema:            trinoCfg.Schema,
			SSL:               trinoCfg.SSL,
			SSLVerify:         trinoCfg.SSLVerify,
			Timeout:           trinoCfg.Timeout,
			DefaultLimit:      trinoCfg.DefaultLimit,
			MaxLimit:          trinoCfg.MaxLimit,
			ReadOnly:          trinoCfg.ReadOnly,
			ConnectionName:    trinoCfg.ConnectionName,
			CatalogMapping:    p.config.Query.URNMapping.CatalogMapping,
			EstimateRowCounts: p.config.Injection.EstimateRowCounts,
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
	p.filePersonaNames = make(map[string]bool, len(p.config.Personas.Definitions))
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
			Connections: persona.ConnectionRules{
				Allow: def.Connections.Allow,
				Deny:  def.Connections.Deny,
			},
			Context: persona.ContextOverrides{
				DescriptionPrefix:         def.Context.DescriptionPrefix,
				DescriptionOverride:       def.Context.DescriptionOverride,
				AgentInstructionsSuffix:   def.Context.AgentInstructionsSuffix,
				AgentInstructionsOverride: def.Context.AgentInstructionsOverride,
			},
			Priority: def.Priority,
			Source:   SourceFile,
		}
		p.filePersonaNames[name] = true
		if err := p.personaRegistry.Register(personaDef); err != nil {
			return fmt.Errorf("registering persona %s: %w", name, err)
		}
	}

	if p.config.Personas.DefaultPersona != "" {
		p.personaRegistry.SetDefault(p.config.Personas.DefaultPersona)
	}

	return nil
}

// FilePersonaNames returns a copy of the persona names loaded from the config file.
func (p *Platform) FilePersonaNames() map[string]bool {
	if p.filePersonaNames == nil {
		return nil
	}
	cp := make(map[string]bool, len(p.filePersonaNames))
	maps.Copy(cp, p.filePersonaNames)
	return cp
}

// loadDBPersonas loads persona definitions from the database and registers
// them in the persona registry. DB personas override file-based ones with
// the same name because Register overwrites existing entries.
func (p *Platform) loadDBPersonas() {
	if p.personaStore == nil {
		return
	}
	defs, err := p.personaStore.List(context.Background())
	if err != nil {
		slog.Warn("failed to load DB personas", logKeyError, err)
		return
	}
	for _, def := range defs {
		per := def.ToPersona()
		if p.filePersonaNames[def.Name] {
			per.Source = SourceBoth
		} else {
			per.Source = SourceDatabase
		}
		if err := p.personaRegistry.Register(per); err != nil {
			slog.Warn("failed to load DB persona", "name", def.Name, logKeyError, err)
		}
	}
	if len(defs) > 0 {
		slog.Info("loaded DB persona overrides", logKeyCount, len(defs))
	}
}

// loadDBAPIKeys loads API key definitions from the database and registers
// them in the API key authenticator using bcrypt hashes.
func (p *Platform) loadDBAPIKeys() {
	if p.apiKeyStore == nil || p.apiKeyAuth == nil {
		return
	}
	defs, err := p.apiKeyStore.List(context.Background())
	if err != nil {
		slog.Warn("failed to load DB api keys", logKeyError, err)
		return
	}
	for _, def := range defs {
		p.apiKeyAuth.AddHashedKey(auth.APIKey{
			KeyHash:     def.KeyHash,
			Name:        def.Name,
			Email:       def.Email,
			Description: def.Description,
			Roles:       def.Roles,
			ExpiresAt:   def.ExpiresAt,
		})
	}
	if len(defs) > 0 {
		slog.Info("loaded DB api keys", logKeyCount, len(defs))
	}
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
				Key:         k.Key,
				Name:        k.Name,
				Email:       k.Email,
				Description: k.Description,
				Roles:       k.Roles,
			})
		}
		apiKeyAuth := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{Keys: keys})
		p.apiKeyAuth = apiKeyAuth
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

	// Register platform-level tools
	p.registerInfoTool()
	p.registerConnectionsTool()
	p.registerPromptTool()

	// Register platform-level prompts from config
	p.registerPlatformPrompts()

	// Register user-defined custom resources from config
	p.registerCustomResources()

	// Register resource templates (schema, glossary, availability)
	p.registerResourceTemplates()

	// Validate agent_instructions references against registered tools
	p.validateAgentInstructions()

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

// SessionStore returns the session store.
func (p *Platform) SessionStore() session.Store {
	return p.sessionStore
}

// OAuthServer returns the OAuth server, or nil if not enabled.
func (p *Platform) OAuthServer() *oauth.Server {
	return p.oauthServer
}

// AuditStore returns the PostgreSQL audit store, or nil if audit is disabled.
func (p *Platform) AuditStore() *auditpostgres.Store {
	return p.auditStore
}

// Authenticator returns the platform authenticator.
func (p *Platform) Authenticator() middleware.Authenticator {
	return p.authenticator
}

// APIKeyAuthenticator returns the API key authenticator, or nil if API keys are disabled.
func (p *Platform) APIKeyAuthenticator() *auth.APIKeyAuthenticator {
	return p.apiKeyAuth
}

// ConfigStore returns the config store.
func (p *Platform) ConfigStore() configstore.Store {
	return p.configStore
}

// PersonaStore returns the persona definition store, or nil if not initialized.
func (p *Platform) PersonaStore() PersonaStore {
	return p.personaStore
}

// APIKeyStore returns the API key definition store, or nil if not initialized.
func (p *Platform) APIKeyStore() APIKeyStore {
	return p.apiKeyStore
}

// PromptStore returns the prompt definition store, or nil if not initialized.
func (p *Platform) PromptStore() prompt.Store {
	return p.promptStore
}

// ConnectionStore returns the connection instance store, or nil if not initialized.
func (p *Platform) ConnectionStore() ConnectionStore {
	return p.connectionStore
}

// ConnectionSources returns the connection→DataHub source mapping.
func (p *Platform) ConnectionSources() *ConnectionSourceMap {
	return p.connectionSources
}

// mergeDBConnectionsIntoConfig loads DB connection instances and merges them
// into p.config.Toolkits so the toolkit loader creates clients for them.
func (p *Platform) mergeDBConnectionsIntoConfig() {
	if p.connectionStore == nil {
		return
	}

	instances, err := p.connectionStore.List(context.Background())
	if err != nil {
		slog.Warn("failed to load DB connections for toolkit merge", logKeyError, err)
		return
	}
	if len(instances) == 0 {
		return
	}

	if p.config.Toolkits == nil {
		p.config.Toolkits = make(map[string]any)
	}

	// Only merge connections for kinds that support DB management (trino, s3).
	// Datahub is single-instance and managed via YAML only.
	manageableKinds := map[string]bool{kindTrino: true, kindS3: true}

	for _, inst := range instances {
		if manageableKinds[inst.Kind] {
			mergeConnectionInstance(p.config.Toolkits, inst)
		}
	}
}

// mergeConnectionInstance merges a single DB connection instance into the
// toolkit config map. File config takes precedence over DB connections.
func mergeConnectionInstance(toolkits map[string]any, inst ConnectionInstance) {
	kindMap, ok := toolkits[inst.Kind].(map[string]any)
	if !ok || !isToolkitEnabled(kindMap) {
		return
	}

	kindInstances, ok := kindMap[cfgKeyInstances].(map[string]any)
	if !ok {
		kindInstances = make(map[string]any)
		kindMap[cfgKeyInstances] = kindInstances
	}

	// Only add if not already present (file config takes precedence)
	if _, exists := kindInstances[inst.Name]; !exists {
		kindInstances[inst.Name] = inst.Config
		slog.Info("merged DB connection into toolkit config", "kind", inst.Kind, "name", inst.Name)
	}
}

// isToolkitEnabled checks if a toolkit kind map has enabled=true.
// Handles both bool and string values (env var expansion produces strings).
func isToolkitEnabled(kindMap map[string]any) bool {
	v, ok := kindMap[cfgKeyEnabled]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true"
	default:
		return false
	}
}

// FileDefaults returns the original file-based config values for whitelisted keys.
// Used to revert to file defaults when a DB override is deleted.
func (p *Platform) FileDefaults() map[string]string {
	return p.fileDefaults
}

// MemoryStore returns the memory store, or nil if memory is disabled.
func (p *Platform) MemoryStore() memory.Store {
	return p.memoryStore
}

// KnowledgeInsightStore returns the insight store, or nil if knowledge is disabled.
func (p *Platform) KnowledgeInsightStore() knowledgekit.InsightStore {
	return p.knowledgeInsightStore
}

// KnowledgeChangesetStore returns the changeset store, or nil if knowledge apply is disabled.
func (p *Platform) KnowledgeChangesetStore() knowledgekit.ChangesetStore {
	return p.knowledgeChangesetStore
}

// KnowledgeDataHubWriter returns the DataHub writer, or nil if knowledge apply is disabled.
func (p *Platform) KnowledgeDataHubWriter() knowledgekit.DataHubWriter {
	return p.knowledgeDataHubWriter
}

// PortalAssetStore returns the portal asset store, or nil if portal is disabled.
func (p *Platform) PortalAssetStore() portal.AssetStore {
	return p.portalAssetStore
}

// PortalShareStore returns the portal share store, or nil if portal is disabled.
func (p *Platform) PortalShareStore() portal.ShareStore {
	return p.portalShareStore
}

// PortalVersionStore returns the portal version store, or nil if portal is disabled.
func (p *Platform) PortalVersionStore() portal.VersionStore {
	return p.portalVersionStore
}

// PortalCollectionStore returns the portal collection store, or nil if portal is disabled.
func (p *Platform) PortalCollectionStore() portal.CollectionStore {
	return p.portalCollectionStore
}

// PortalS3Client returns the portal S3 client, or nil if portal is disabled.
func (p *Platform) PortalS3Client() portal.S3Client {
	return p.portalS3Client
}

// BrandLogoSVG returns the resolved brand logo SVG content (from portal.logo
// or mcpapps platform-info config), or empty string if none is configured.
func (p *Platform) BrandLogoSVG() string {
	return p.resolvedBrandLogoSVG
}

// BrandURL returns the resolved brand URL from the mcpapps platform-info
// config (brand_url), or empty string if not configured.
func (p *Platform) BrandURL() string {
	return p.resolvedBrandURL
}

// ResolveImplementorLogo fetches the implementor logo SVG from the URL
// configured in portal.implementor.logo. The result is cached so subsequent
// calls return the same value without another HTTP request. Returns empty
// string if no logo URL is configured or the fetch fails.
func (p *Platform) ResolveImplementorLogo() string {
	logoURL := p.config.Portal.Implementor.Logo
	if logoURL == "" {
		return ""
	}
	if p.resolvedImplementorLogo != "" {
		return p.resolvedImplementorLogo
	}
	svg, err := fetchLogoSVG(logoURL)
	if err != nil {
		slog.Debug("implementor logo fetch failed", "url", logoURL, "err", err)
		return ""
	}
	p.resolvedImplementorLogo = svg
	return svg
}

// BrowserSessionFlow returns the OIDC login flow, or nil if browser sessions are disabled.
func (p *Platform) BrowserSessionFlow() *browsersession.Flow {
	return p.browserSessionFlow
}

// BrowserSessionAuth returns the cookie-based authenticator, or nil if browser sessions are disabled.
func (p *Platform) BrowserSessionAuth() *browsersession.Authenticator {
	return p.browserSessionAuth
}

// ToolInfo describes a tool registered directly on the platform (not via a toolkit).
type ToolInfo struct {
	Name string
	Kind string
}

// PlatformTools returns tools registered directly on the platform outside of any toolkit.
func (p *Platform) PlatformTools() []ToolInfo {
	tools := []ToolInfo{
		{Name: "platform_info", Kind: "platform"},
		{Name: "list_connections", Kind: "platform"},
	}
	if p.promptStore != nil {
		tools = append(tools, ToolInfo{Name: "manage_prompt", Kind: "platform"})
	}
	return tools
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
	Password       string // #nosec G117 -- Trino connection credential from admin config
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
	UsePathStyle   bool
}

// getDataHubConfig extracts DataHub configuration from toolkits config.
func (p *Platform) getDataHubConfig(instanceName string) *datahubConfig {
	instanceCfg := p.getInstanceConfig(kindDataHub, instanceName)
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
	instanceCfg := p.getInstanceConfig(toolkitKindTrino, instanceName)
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
		UsePathStyle:   cfgBool(instanceCfg, "use_path_style"),
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

	instances, ok := kindCfg[cfgKeyInstances].(map[string]any)
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

// injectToolkitPlatformConfig injects platform-level configuration into
// toolkit instance config maps before the registry loader processes them.
// This allows platform-wide settings (e.g., progress.enabled, elicitation)
// to reach toolkit factories via the normal config parsing path.
func (p *Platform) injectToolkitPlatformConfig() {
	instances := p.trinoInstanceConfigs()
	if instances == nil {
		return
	}

	needsProgress := p.config.Progress.Enabled
	needsElicitation := p.config.Elicitation.Enabled

	if !needsProgress && !needsElicitation {
		return
	}

	for name, v := range instances {
		instanceCfg, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if needsProgress {
			instanceCfg["progress_enabled"] = true
		}
		if needsElicitation {
			instanceCfg["elicitation"] = map[string]any{
				cfgKeyEnabled: true,
				"cost_estimation": map[string]any{
					cfgKeyEnabled:   p.config.Elicitation.CostEstimation.Enabled,
					"row_threshold": p.config.Elicitation.CostEstimation.RowThreshold,
				},
				"pii_consent": map[string]any{
					cfgKeyEnabled: p.config.Elicitation.PIIConsent.Enabled,
				},
			}
		}
		instances[name] = instanceCfg
	}
}

// trinoInstanceConfigs returns the Trino toolkit instances map, or nil if not found.
func (p *Platform) trinoInstanceConfigs() map[string]any {
	if p.config.Toolkits == nil {
		return nil
	}
	trinoCfg, ok := p.config.Toolkits[toolkitKindTrino]
	if !ok {
		return nil
	}
	kindCfg, ok := trinoCfg.(map[string]any)
	if !ok {
		return nil
	}
	instances, ok := kindCfg[cfgKeyInstances].(map[string]any)
	if !ok {
		return nil
	}
	return instances
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

// Close closes all platform resources in the correct order:
//  1. Flush enrichment state, stop session cache, close session store
//  2. Close audit logger + audit store (goroutine stops, can still use DB)
//  3. Close providers and toolkit registry (trino, datahub, s3)
//  4. Close database connection (last — nothing else needs it)
func (p *Platform) Close() error {
	var errs []error

	// Phase 0: stop background goroutines
	p.stopBackgroundTrackers()

	// Phase 1a: flush enrichment dedup state to session store
	p.flushEnrichmentState()

	// Phase 1b: stop session cache goroutine
	if p.sessionCache != nil {
		slog.Debug("shutdown: stopping session cache")
		p.sessionCache.Stop()
	}

	// Phase 1c: close session store (stop cleanup goroutine)
	if p.sessionStore != nil {
		slog.Debug("shutdown: closing session store")
		closeResource(&errs, p.sessionStore)
	}

	// Phase 1d: close OAuth store (stop cleanup goroutine)
	if p.oauthStoreCloser != nil {
		slog.Debug("shutdown: closing OAuth store")
		closeResource(&errs, p.oauthStoreCloser)
	}

	// Phase 2: close audit (cancel cleanup goroutine, wait for exit)
	if closer, ok := p.auditLogger.(Closer); ok {
		slog.Debug("shutdown: closing audit logger")
		closeResource(&errs, closer)
	}
	if p.auditStore != nil {
		slog.Debug("shutdown: closing audit store")
		closeResource(&errs, p.auditStore)
	}

	// Phase 3: close providers and toolkit registry
	slog.Debug("shutdown: closing providers")
	closeResource(&errs, p.semanticProvider)
	closeResource(&errs, p.queryProvider)
	closeResource(&errs, p.storageProvider)
	if p.portalS3Client != nil {
		slog.Debug("shutdown: closing portal S3 client")
		closeResource(&errs, p.portalS3Client)
	}
	closeResource(&errs, p.toolkitRegistry)

	// Phase 4: close database connection (last)
	if p.db != nil {
		slog.Debug("shutdown: closing database")
		if err := p.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing database: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing platform: %v", errs)
	}
	slog.Debug("shutdown: platform closed")
	return nil
}

// stopBackgroundTrackers stops background goroutines for workflow tracking and
// session gating. Called at the beginning of Close() to halt periodic cleanups.
func (p *Platform) stopBackgroundTrackers() {
	if p.workflowTracker != nil {
		slog.Debug("shutdown: stopping workflow tracker")
		p.workflowTracker.Stop()
	}
	if p.sessionGate != nil {
		slog.Debug("shutdown: stopping session gate")
		p.sessionGate.Stop()
	}
	if p.stalenessWatcher != nil {
		slog.Debug("shutdown: stopping staleness watcher")
		p.stalenessWatcher.Stop()
	}
}

// flushEnrichmentState persists enrichment dedup state from the session cache
// to the session store for continuity across restarts.
func (p *Platform) flushEnrichmentState() {
	if p.sessionCache == nil || p.sessionStore == nil {
		return
	}

	exported := p.sessionCache.ExportSessions()
	if len(exported) == 0 {
		return
	}

	ctx := context.Background()
	flushed := 0
	for sessionID, tables := range exported {
		state := map[string]any{"enrichment_dedup": tables}
		if err := p.sessionStore.UpdateState(ctx, sessionID, state); err != nil {
			slog.Debug("shutdown: failed to flush enrichment state",
				"session_id", sessionID, logKeyError, err)
			continue
		}
		flushed++
	}
	slog.Debug("shutdown: flushed enrichment state", "sessions", flushed)
}

// loadPersistedEnrichmentState restores enrichment dedup state from the
// session store into the session cache on startup.
func (p *Platform) loadPersistedEnrichmentState() {
	if p.sessionCache == nil || p.sessionStore == nil {
		return
	}

	ctx := context.Background()
	sessions, err := p.sessionStore.List(ctx)
	if err != nil {
		slog.Warn("failed to load persisted enrichment state", logKeyError, err)
		return
	}

	loaded := 0
	for _, sess := range sessions {
		dedupRaw, ok := sess.State["enrichment_dedup"]
		if !ok {
			continue
		}
		tables := parseDedupState(dedupRaw)
		if len(tables) > 0 {
			p.sessionCache.LoadSession(sess.ID, tables)
			loaded++
		}
	}
	if loaded > 0 {
		slog.Info("loaded persisted enrichment state", "sessions", loaded)
	}
}

// parseDedupState converts the enrichment_dedup state value into the typed
// map the session cache expects. Handles three storage formats:
//   - map[string]middleware.SentTableEntry (memory store preserves Go types directly)
//   - map[string]any with object values (new JSON format: {"sent_at": ..., "token_count": ...})
//   - map[string]any with string/time values (old JSON format: table → timestamp)
func parseDedupState(raw any) map[string]middleware.SentTableEntry {
	// Memory store: value is already the correct type.
	if typed, ok := raw.(map[string]middleware.SentTableEntry); ok {
		return typed
	}

	// Database store: JSON deserialized as map[string]any.
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]middleware.SentTableEntry, len(m))
	for table, v := range m {
		entry, entryOK := parseDedupEntry(v)
		if entryOK {
			result[table] = entry
		}
	}
	return result
}

// parseDedupEntry parses a single dedup entry from either new or old format.
func parseDedupEntry(v any) (middleware.SentTableEntry, bool) {
	switch t := v.(type) {
	case middleware.SentTableEntry:
		return t, true
	case map[string]any:
		return parseEntryFromMap(t)
	case time.Time:
		return middleware.SentTableEntry{SentAt: t}, true
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return middleware.SentTableEntry{SentAt: parsed}, true
		}
	}
	return middleware.SentTableEntry{}, false
}

// parseEntryFromMap parses a SentTableEntry from a JSON-deserialized map.
func parseEntryFromMap(m map[string]any) (middleware.SentTableEntry, bool) {
	entry := middleware.SentTableEntry{}
	sentAt, ok := m["sent_at"]
	if !ok {
		return entry, false
	}
	switch t := sentAt.(type) {
	case time.Time:
		entry.SentAt = t
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, t)
		if err != nil {
			return entry, false
		}
		entry.SentAt = parsed
	default:
		return entry, false
	}
	if tc, ok := m["token_count"]; ok {
		switch n := tc.(type) {
		case float64:
			entry.TokenCount = int(n)
		case int:
			entry.TokenCount = n
		}
	}
	return entry, true
}
