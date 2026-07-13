package platform

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"slices"
	"time"

	// PostgreSQL driver for database/sql.
	_ "github.com/lib/pq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/apps"
	"github.com/txn2/mcp-data-platform/internal/platform/branding"
	"github.com/txn2/mcp-data-platform/internal/platform/browserauth"
	"github.com/txn2/mcp-data-platform/internal/platform/completionlayer"
	"github.com/txn2/mcp-data-platform/internal/platform/connauth"
	"github.com/txn2/mcp-data-platform/internal/platform/dedup"
	"github.com/txn2/mcp-data-platform/internal/platform/exportadapters"
	"github.com/txn2/mcp-data-platform/internal/platform/iam"
	"github.com/txn2/mcp-data-platform/internal/platform/indexqueue"
	"github.com/txn2/mcp-data-platform/internal/platform/knowledgelayer"
	"github.com/txn2/mcp-data-platform/internal/platform/listchanged"
	"github.com/txn2/mcp-data-platform/internal/platform/memorylayer"
	"github.com/txn2/mcp-data-platform/internal/platform/mwchain"
	"github.com/txn2/mcp-data-platform/internal/platform/oauthserver"
	"github.com/txn2/mcp-data-platform/internal/platform/obs"
	"github.com/txn2/mcp-data-platform/internal/platform/portalstore"
	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer"
	"github.com/txn2/mcp-data-platform/internal/platform/reflexivecapture"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcelayer"
	"github.com/txn2/mcp-data-platform/internal/platform/routepolicy"
	"github.com/txn2/mcp-data-platform/internal/platform/searchfed"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionsync"
	"github.com/txn2/mcp-data-platform/internal/platform/toolkitcfg"
	"github.com/txn2/mcp-data-platform/internal/platform/userdir"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	configpostgres "github.com/txn2/mcp-data-platform/pkg/configstore/postgres"
	"github.com/txn2/mcp-data-platform/pkg/connbackfill"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/mcpapps"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/oauth"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/query"
	trinoquery "github.com/txn2/mcp-data-platform/pkg/query/trino"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	searchgatepostgres "github.com/txn2/mcp-data-platform/pkg/searchgate/postgres"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	datahubsemantic "github.com/txn2/mcp-data-platform/pkg/semantic/datahub"
	"github.com/txn2/mcp-data-platform/pkg/session"
	"github.com/txn2/mcp-data-platform/pkg/storage"
	s3storage "github.com/txn2/mcp-data-platform/pkg/storage/s3"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
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
	enrichmentStore   enrichment.Store
	// connAuth owns the connection-OAuth token lifecycle (the unified
	// connection_oauth_tokens store, the durable connection_auth_events store +
	// nil-safe writer with its daily 90-day prune routine, and the background
	// token-refresh loop) behind one handle. nil when no database is configured;
	// the read accessors and shutdown helpers are all nil-safe.
	connAuth      *connauth.Handle
	restEncryptor *fieldcrypt.RestFieldEncryptor
	personaStore  personastore.Store
	apiKeyStore   APIKeyStore

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
	oauthHandle *oauthserver.Handle
	// oauthKeys holds the active signing key plus verify-only previous keys.
	// Grouped into one field so rotation support does not widen the Platform
	// struct past its frozen field ceiling.
	oauthKeys oauthSigningKeys

	// Browser session (OIDC login flow + cookie-based auth for the web UI)
	browserSession *browserauth.Session

	// Audit
	auditLogger middleware.AuditLogger

	// Session management: the externalized session store, the
	// enrichment-dedup cache, the client-facing MCP notification
	// broadcaster, and the dedicated cross-replica reload bus all live
	// behind one owner handle (issue #843). Built from p.db + resolved
	// config in initSessions; the reload re-materialization handlers
	// (reload*Local) stay on Platform and are injected into the owner.
	sessions *sessionsync.Handle

	// Tuning
	ruleEngine *tuning.RuleEngine

	// Prompt layer. The owner (pkg/platform/promptlayer) holds the prompt store,
	// the file-based tuning prompt manager, and the name-keyed prompt-metadata
	// list behind one Handle, along with the static/workflow/database registration
	// path, the per-viewer dynamic-serving callbacks, and the manage_prompt tool.
	// The store is read through Store() and surfaced by PromptStore(); the
	// registration/serving entry points take the *mcp.Server per call. Never nil
	// (prompts register and serve without a database); the store is nil on a
	// no-DB deployment.
	prompts *promptlayer.Handle

	// Knowledge-capture layer. The owner (pkg/platform/knowledgelayer) holds the
	// insight store (the memory-backed adapter over memory_records, else the
	// legacy Postgres store), the changeset store + DataHub writer for
	// apply_knowledge, and the capture_insight / apply_knowledge toolkit behind
	// one Handle; the stores / writer / toolkit are read through its accessors and
	// surfaced by the three admin accessors below. nil when knowledge is disabled
	// or no database is configured.
	knowledge *knowledgelayer.Handle
	// Search-federation layer. The owner (pkg/platform/searchfed) holds the
	// unified search router and the search toolkit behind one Handle; the router
	// is read through its accessor and backs both the MCP search tool and the
	// portal's GET /search REST endpoint. nil on a store-less deployment with no
	// searchable source, so no search tool is registered.
	searchFed *searchfed.Handle

	// Memory layer. The owner (pkg/platform/memorylayer) holds the memory store,
	// memory toolkit (with its recall-first checker), enrichment adapter, and
	// staleness watcher behind one Handle; the store / toolkit / adapter are read
	// through its accessors. embeddingProv stays a Platform field (built by the
	// memory owner, then handed back) because it backs many other subsystems
	// (portalstore, indexqueue, api-gateway, search/knowledge); it is nil when
	// memory is disabled or no database is configured.
	memory        *memorylayer.Handle
	embeddingProv embedding.Provider

	// shared index-jobs embedding queue. The owner (pkg/platform/indexqueue)
	// holds the store, registry, worker, reaper, reconciler, retention sweep,
	// LISTEN adapter, and every consumer behind one Handle; the admin view,
	// cross-kind reporter, and tools vector store are read through its
	// accessors. nil until WireAPIGatewayEmbedJobsFromDB runs, which requires
	// both a database connection and a configured embedding provider.
	indexQueue *indexqueue.Handle

	// portalStore owns the asset-portal store layer: the five Postgres stores
	// (asset, share, version, collection, thread), the knowledge-page store, the
	// S3 blob backend, and the save/manage-artifact toolkit. nil until initPortal
	// runs, which requires the portal enabled and a database connection. Read
	// through its accessors by the Portal* accessors (admin/portal REST wiring),
	// the trino/api export wiring, and the search/enrichment provider assembly.
	portalStore       *portalstore.Handle
	provenanceTracker *middleware.ProvenanceTracker
	// Brand assets (logo SVG, brand URL, implementor logo). The owner
	// (pkg/platform/branding) resolves each once from config and caches it behind
	// one Handle; the caller injects the portal logo into the platform-info app
	// config and reads the cached values through BrandLogoSVG() / BrandURL() /
	// ResolveImplementorLogo(). Built in initMCPApps.
	branding *branding.Handle

	// Workflow gating
	workflowTracker *middleware.SessionWorkflowTracker

	// Reflexive knowledge capture (#635): per-session query-failure state used to
	// pair a query error with its later in-session fix into an auto-minted
	// correction memory.
	reflexiveErrors *middleware.SessionErrorTracker

	// Session gate
	sessionGate *middleware.SessionGate

	// Managed-resources layer. The owner (pkg/platform/resourcelayer) holds the
	// Postgres resource store, the S3 blob client, and the MCP-server
	// registration behind one Handle; the store / client are read through its
	// accessors and surfaced by ResourceStore() / ResourceS3Client(). nil when
	// managed resources are disabled or no database is configured.
	resources *resourcelayer.Handle

	// Known-users directory (#614). The owner (pkg/platform/userdir) holds the
	// user store and the throttled-async directory behind one Handle; the store
	// is read through UserStore(), and the owner's Observe methods are wired as
	// the authenticator's UserObserver and the browser-session login callback.
	// nil without a database, in which case every consumer degrades cleanly to
	// free-typed email sharing.
	users *userdir.Handle

	// MCP Apps
	mcpAppsRegistry *mcpapps.Registry

	// obs owns the observability layer: the metrics recorder, its /metrics
	// listener, and the OTel tracer. Every handle is nil-safe, so consumers
	// record and trace unconditionally; the layer reflects the (default-off)
	// metrics/tracing config. NewTracer installs the global TracerProvider
	// when tracing is enabled so toolkit adapters emit nested spans without
	// an injected reference.
	obs *obs.Layer

	// apiMemBudget is the process-wide in-flight memory budget shared by
	// every api gateway toolkit's buffered tools (issue #535). Created
	// once by WireAPIGatewayMemBudget and injected into each toolkit so
	// accounting is truly process-wide, not per-instance. nil-safe:
	// unset means unlimited.
	apiMemBudget *apigatewaykit.MemBudget
}

// New creates a new platform instance.
func New(opts ...Option) (*Platform, error) {
	options := &Options{}
	for _, opt := range opts {
		opt(options)
	}

	if options.Config == nil {
		return nil, errors.New("config is required")
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
	// Observability has no deps; build it first so any later init
	// step can record startup metrics or store a recorder reference.
	if err := p.initObservability(); err != nil {
		return err
	}
	// Initialize data infrastructure first (database + config store)
	if err := p.initDataInfra(opts); err != nil {
		return err
	}
	if err := p.initProviders(opts); err != nil {
		return err
	}
	if err := p.initRegistries(opts); err != nil {
		return err
	}
	// Prompt layer: assembled after the toolkit registry exists (it reads the
	// registry for capability bullets and workflow gating) and before
	// initExtensions, whose knowledge/search wiring reads the prompt store.
	p.initPromptStore()
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
func (p *Platform) initDataInfra(opts *Options) error {
	if err := p.initDatabase(); err != nil {
		return err
	}
	if err := p.initConnectionStore(opts); err != nil {
		return err
	}
	p.initPersonaStore()
	p.initAPIKeyStore()
	p.initUserStore()
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
	// Bridge feedback threads into memory_capture (#602/#633). The linker is the
	// portal toolkit, not the raw thread store, so thread linking is gated by the
	// same owns-or-edit access check as resolve_thread (the toolkit authorizes
	// each thread before linking). Portal creates the toolkit, so this is wired
	// after both init.
	if p.memory.Toolkit() != nil && p.portalStore.Toolkit() != nil {
		p.memory.Toolkit().SetThreadLinker(p.portalStore.Toolkit())
	}
	// Unified knowledge read path (#632). Federates the stores initialized
	// above (memory, insights, assets), so it must run after them.
	if err := p.initSearch(); err != nil {
		return err
	}
	if err := p.initManagedResources(); err != nil {
		return err
	}
	return p.initMCPApps()
}

// initMemory assembles the memory layer via the memorylayer owner: the store,
// embedder, toolkit (with its recall-first checker), enrichment adapter, and
// staleness watcher behind one Handle. It translates platform config into the
// owner's Config and delegates assembly; toolkit registration stays here (a
// registry concern), and embeddingProv is lifted back onto Platform because it
// backs many other subsystems. No-op (nil handle, nil embedder) when memory is
// explicitly disabled or no database is configured.
func (p *Platform) initMemory() error {
	if isExplicitlyDisabled(p.config.Memory.Enabled) || p.db == nil {
		return nil
	}

	handle, err := memorylayer.New(p.db, p.semanticProvider, memorylayer.Config{
		ToolkitName:       instanceDefault,
		EmbeddingProvider: p.config.Memory.Embedding.Provider,
		Ollama: embedding.OllamaConfig{
			URL:           p.config.Memory.Embedding.Ollama.URL,
			Model:         p.config.Memory.Embedding.Ollama.Model,
			Timeout:       p.config.Memory.Embedding.Ollama.Timeout,
			MaxInputBytes: p.config.Memory.Embedding.Ollama.MaxInputBytes,
		},
		StalenessEnabled: p.config.Memory.Staleness.Enabled,
		Staleness: memory.StalenessConfig{
			Interval:  p.config.Memory.Staleness.Interval,
			BatchSize: p.config.Memory.Staleness.BatchSize,
		},
	})
	if err != nil {
		return fmt.Errorf("creating memory layer: %w", err)
	}
	p.memory = handle
	// embeddingProv stays a Platform field: it backs portalstore, indexqueue,
	// the api-gateway, and the search/knowledge assembly (constraint 1).
	p.embeddingProv = handle.EmbeddingProvider()

	// Toolkit registration stays a Platform/registry concern. Register before
	// starting the staleness watcher so a registration failure never leaves a
	// detached watcher goroutine running (matching the original order).
	if err := p.toolkitRegistry.Register(handle.Toolkit()); err != nil {
		return fmt.Errorf("registering memory toolkit: %w", err)
	}
	handle.Start()

	slog.Info("memory layer enabled",
		"embedding_provider", p.config.Memory.Embedding.Provider,
		"staleness_enabled", p.config.Memory.Staleness.Enabled)
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
	// Report this pool's saturation stats under the "platform" label. All
	// stores (connections, audit, OAuth, sessions) share this single handle.
	p.obs.Metrics().RegisterDBPool(p.db, "platform")
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
//
// An injected store (opts.ConnectionStore) wins over the default
// resolution — used by tests to inject a controlled mock without
// standing up a real database, and by embedders that supply their
// own backend.
func (p *Platform) initConnectionStore(opts *Options) error {
	if opts != nil && opts.ConnectionStore != nil {
		p.connectionStore = opts.ConnectionStore
		slog.Info("connection store: injected", "persistent", opts.ConnectionStore.Persistent())
		return nil
	}
	if p.db == nil {
		p.connectionStore = &NoopConnectionStore{}
		slog.Info("connection store: noop (no database)")
		return nil
	}

	encryptor, err := buildFieldEncryptor()
	if err != nil {
		return err
	}
	p.restEncryptor = fieldcrypt.NewRestFieldEncryptor(encryptor)
	p.connectionStore = NewPostgresConnectionStore(p.db, encryptor)
	p.enrichmentStore = enrichment.NewPostgresStore(p.db)
	// The connection-OAuth token lifecycle (unified token store, durable
	// auth-event store + writer, and its 90-day prune routine) is owned by
	// connauth.Handle, assembled from the same *sql.DB and at-rest encryptor.
	// The background refresher is started post-init via StartConnOAuthRefresher.
	p.connAuth = connauth.New(p.db, p.restEncryptor)
	return nil
}

// ConnOAuthStore returns the unified OAuth-token store backing the
// connection_oauth_tokens table. Used by the admin layer's unified
// OAuth handler and (via toolkit OAuthKindHandlers) by per-kind
// Authenticators. Nil when no database is configured — the platform
// falls back to the legacy per-kind in-memory stores in that case.
func (p *Platform) ConnOAuthStore() connoauth.Store {
	return p.connAuth.Store()
}

// AuthEventStore returns the read-side handle for connection
// lifecycle events. Used by the admin layer to render the OAuth
// History panel. Nil when no database is configured.
func (p *Platform) AuthEventStore() authevents.Store {
	return p.connAuth.AuthEventStore()
}

// AuthEventWriter returns the writer wrapping AuthEventStore. nil-safe
// — every call site can pass this directly to a component without
// nil-checks; the Writer methods short-circuit when the receiver is
// nil. Wired into the admin handler and the toolkit auth paths.
func (p *Platform) AuthEventWriter() *authevents.Writer {
	return p.connAuth.AuthEventWriter()
}

// StartConnOAuthRefresher launches the background refresh loop with
// the supplied ConfigResolver. The platform exposes the start as a
// post-init step so the resolver — which depends on the per-kind
// OAuthKindHandlers wired by main.go — can be passed in at the
// correct point in the startup sequence (after ConnectionStore +
// OAuthKindHandlers exist, before the HTTP server starts taking
// traffic). Idempotent across replicas: the locker (NoopLocker for
// single-replica, PostgresLocker for multi-replica) keeps races out
// of the IdP side.
func (p *Platform) StartConnOAuthRefresher(resolver connoauth.ConfigResolver, multiReplica bool) {
	if p.connAuth == nil || resolver == nil {
		return
	}
	// The locker selection stays here because it depends on p.db and the
	// replica mode; connauth takes the chosen locker so it stays free of that
	// decision. NoopLocker for single-replica, PostgresLocker for multi-replica.
	var locker connoauth.AdvisoryLocker = connoauth.NoopLocker{}
	if multiReplica && p.db != nil {
		locker = connoauth.NewPostgresLocker(p.db)
	}
	p.connAuth.StartRefresher(resolver, locker)
}

// StopConnOAuthRefresher cancels the refresher loop. Called by the
// lifecycle shutdown path so an in-flight refresh has up to ctx's
// deadline to settle before the process exits.
func (p *Platform) StopConnOAuthRefresher(ctx context.Context) error {
	if err := p.connAuth.Stop(ctx); err != nil {
		return fmt.Errorf("stop connoauth refresher: %w", err)
	}
	return nil
}

// RestEncryptor returns the platform's at-rest field encryption
// adapter, or nil when no database is configured. Sub-package stores
// can pass this to their constructors so secrets they persist use the
// same key and format as connection credentials.
func (p *Platform) RestEncryptor() *fieldcrypt.RestFieldEncryptor {
	return p.restEncryptor
}

// EnrichmentStore returns the gateway enrichment rule store. Nil when no
// database is configured (the gateway toolkit still works without
// enrichment rules — they're optional).
func (p *Platform) EnrichmentStore() enrichment.Store {
	return p.enrichmentStore
}

// WireGatewayIntegrations runs the post-construction wiring the gateway and
// api-gateway toolkits need before the HTTP root handler is built, in
// dependency order. Every step is idempotent and no-ops when no gateway /
// api-gateway toolkit is loaded, so an entry point calls it unconditionally.
//
// The order is load-bearing: the token store, broadcaster, route policy,
// api-gateway token store, embedding provider, and catalog store are wired
// first; the embed-job queue is wired LAST because it depends on the catalog
// store and embedding provider already being in place. Encapsulating the
// sequence here (rather than as a bare call list in the composition root) makes
// the ordering contract a single testable unit and keeps cmd/main.go thin.
func (p *Platform) WireGatewayIntegrations() {
	p.WireGatewayTokenStore()
	p.WireGatewayBroadcaster()
	p.WireAPIGatewayRoutePolicy()
	p.WireAPIGatewayTokenStore()
	p.WireAPIGatewayEmbeddingProvider()
	p.WireAPIGatewayCatalogStoreFromDB()
	p.WireAPIGatewayEmbedJobsFromDB()
}

// WireGatewayTokenStore attaches the unified connoauth.Store to every
// live gateway toolkit in the registry so authorization_code grants
// survive process restarts. No-op when no database is configured.
//
// Lives on Platform (not in cmd/main.go) so the post-construction
// wiring step is part of the platform contract — testable without
// spinning up a full main, and discoverable for any future entry-point
// that builds a Platform.
//
// Calling SetConnOAuthStore on a gateway toolkit that already has
// placeholder authorization_code connections will retry their dial
// path so persisted tokens come live without requiring an additional
// admin action. This is what makes auto-enabled gateway toolkits work
// on a fresh boot.
func (p *Platform) WireGatewayTokenStore() {
	store := p.connAuth.Store()
	if store == nil {
		return
	}
	for _, tk := range p.toolkitRegistry.All() {
		if gw, ok := tk.(*gatewaykit.Toolkit); ok {
			// Wire the audit-event writer FIRST so the SetConnOAuthStore
			// retry path's discoverFor builds Sources with the events
			// writer already in place. The writer is nil-safe; events
			// short-circuit when the writer is nil (dev with no DB).
			gw.SetAuthEvents(p.connAuth.AuthEventWriter())
			gw.SetConnOAuthStore(store)
		}
	}
}

// Broadcaster returns the platform's session broadcaster. Always
// non-nil after New — initSessions runs during platform construction
// and wires either a postgres LISTEN/NOTIFY broadcaster or an
// in-memory one.
func (p *Platform) Broadcaster() session.Broadcaster {
	return p.sessions.Broadcaster()
}

// gatewayListChangedNotifier adapts session.Broadcaster onto
// gatewaykit.ToolListChangedNotifier so the gateway package doesn't
// have to import pkg/session directly. Holds a reference to the
// shared broadcaster; Publish errors are swallowed (logged) since
// tools/list_changed is best-effort.
type gatewayListChangedNotifier struct {
	b session.Broadcaster
}

// NotifyToolsListChanged publishes a notifications/tools/list_changed
// event to every connected SSE long-poll subscriber.
func (n gatewayListChangedNotifier) NotifyToolsListChanged(ctx context.Context) {
	if n.b == nil {
		return
	}
	if err := n.b.Publish(ctx, session.Event{Method: "notifications/tools/list_changed"}); err != nil {
		// source=gateway lets operators correlate this warning back to
		// the gateway publish path vs. other broadcaster publishers
		// (today there are none, but the broadcaster is shared and a
		// future publisher would otherwise produce identically-shaped
		// noise in dashboards).
		slog.Warn("broadcaster: publish tools/list_changed failed",
			"source", "gateway",
			"method", "notifications/tools/list_changed",
			"error", err)
	}
}

// WireAPIGatewayTokenStore attaches the unified connoauth.Store to
// every live api gateway toolkit. Mirrors WireGatewayTokenStore in
// placement and lifecycle. Safe to call multiple times — the
// toolkit's SetConnOAuthStore re-threads any already-materialized
// authorization_code Authenticators.
func (p *Platform) WireAPIGatewayTokenStore() {
	store := p.connAuth.Store()
	if store == nil {
		return
	}
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			// Audit writer FIRST so any subsequent refresh through the
			// Authenticator emits lifecycle events.
			api.SetAuthEvents(p.connAuth.AuthEventWriter())
			api.SetConnOAuthStore(store)
		}
	}
}

// WireAPIGatewayEmbeddingProvider attaches the platform's embedding
// provider to every live api gateway toolkit. Enables the
// "semantic" and "hybrid" ranking modes of api_list_endpoints; when
// the platform was built without an embedding provider (memory
// disabled or explicitly noop) the call is a no-op and the toolkit
// silently falls back to lexical for any non-lexical request.
func (p *Platform) WireAPIGatewayEmbeddingProvider() {
	if p.embeddingProv == nil {
		return
	}
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.SetEmbeddingProvider(p.embeddingProv)
		}
	}
}

// EmbeddingProvider returns the platform's embedding provider, or
// nil when no provider was configured. Exposed so the admin handler
// can compute and persist per-operation vectors at spec-write time
// (the path that replaces the in-process embedding warmer).
func (p *Platform) EmbeddingProvider() embedding.Provider {
	return p.embeddingProv
}

// WireAPIGatewayCatalogStoreFromDB builds a Postgres-backed catalog
// store from the platform's *sql.DB and wires it into every api
// gateway toolkit. No-op when the platform was built without a
// database (file-only deployments).
func (p *Platform) WireAPIGatewayCatalogStoreFromDB() {
	if p.db == nil {
		return
	}
	p.WireAPIGatewayCatalogStore(apigatewaycatalog.NewPostgresStore(p.db))
}

// APIGatewayCatalogStore returns the catalog store currently wired
// into the first api gateway toolkit, or nil when no toolkit has
// one wired. Used by the admin layer to share the same store for
// both reads (toolkit) and writes (admin CRUD).
func (p *Platform) APIGatewayCatalogStore() apigatewaycatalog.Store {
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			return api.CatalogStore()
		}
	}
	return nil
}

// WireAPIGatewayCatalogStore attaches the catalog.Store the toolkit
// uses to load OpenAPI specs referenced by connection.catalog_id.
// Mirrors WireAPIGatewayTokenStore in placement and lifecycle: safe
// to call multiple times, no-op when the catalog store is unwired
// (file-only deployments without a database).
//
// After wiring the store, every already-registered connection is
// reloaded so connections that registered before the store became
// available pick up their catalog content immediately. Without this
// reload, the initial NewMulti call (which runs before
// platform-level wiring) would leave connections in the "catalog_id
// set but zero ops" state until the next admin save.
func (p *Platform) WireAPIGatewayCatalogStore(store apigatewaycatalog.Store) {
	if store == nil {
		return
	}
	for _, tk := range p.toolkitRegistry.All() {
		api, ok := tk.(*apigatewaykit.Toolkit)
		if !ok {
			continue
		}
		api.SetCatalogStore(store)
		for _, detail := range api.ListConnections() {
			if err := api.ReloadConnection(detail.Name); err != nil {
				slog.Warn("apigateway: catalog wire reload failed",
					"connection", detail.Name, "error", err)
			}
		}
	}
}

// WireAPIGatewayRoutePolicy installs a per-(connection, method, path)
// authorization gate on every live api gateway toolkit. No-op when
// the platform's authorizer is not the persona-based implementation
// (custom authorizers may opt in later via their own wiring).
//
// Mirrors WireGatewayTokenStore / WireGatewayBroadcaster in placement
// and lifecycle. Safe to call before or after RegisterTools.
func (p *Platform) WireAPIGatewayRoutePolicy() {
	if p.authorizer == nil {
		return
	}
	pa, ok := p.authorizer.(*persona.Authorizer)
	if !ok {
		return
	}
	policy := routepolicy.New(routepolicy.Deps{Authenticator: p.authenticator, Authorizer: pa})
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.SetRoutePolicy(policy)
		}
	}
}

// WireGatewayBroadcaster attaches the platform's session broadcaster
// to every live gateway toolkit so SSE long-poll subscribers receive
// tools/list_changed events whenever a gateway connection is added,
// removed, or comes up after re-auth.
//
// Mirrors WireGatewayTokenStore in placement and lifecycle. Safe to
// call before or after RegisterTools — gateway toolkits read the
// notifier atomically per call, so wiring order does not matter.
func (p *Platform) WireGatewayBroadcaster() {
	b := p.sessions.Broadcaster()
	if b == nil {
		return
	}
	notifier := gatewayListChangedNotifier{b: b}
	for _, tk := range p.toolkitRegistry.All() {
		if gw, ok := tk.(*gatewaykit.Toolkit); ok {
			gw.SetToolListChangedNotifier(notifier)
		}
	}
}

// DB returns the platform's database handle, or nil when running
// without a database. Exposed so consumers can build their own
// DB-backed stores (e.g., pkcestore.PostgresStore for multi-replica
// OAuth) without needing platform-side wiring per store.
func (p *Platform) DB() *sql.DB {
	return p.db
}

// initPersonaStore initializes the persona definition store.
func (p *Platform) initPersonaStore() {
	if p.db != nil {
		p.personaStore = personastore.NewPostgresStore(p.db)
		slog.Info("persona store: postgres")
	} else {
		p.personaStore = &personastore.NoopStore{}
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

// initPromptStore assembles the prompt layer via the promptlayer owner: the
// prompt store, the file-based tuning prompt manager, and the name-keyed
// prompt-metadata list behind one Handle. It translates platform config into the
// owner's Config and delegates assembly. The owner is never nil (prompts
// register and serve without a database); the store is nil when no database is
// configured. The embedding provider and portal share lister are bound later
// (finalizeSetup), once those subsystems exist.
func (p *Platform) initPromptStore() {
	p.prompts = promptlayer.New(promptlayer.Config{
		DB:                p.db,
		PromptsDir:        p.config.Tuning.PromptsDir,
		ServerName:        p.config.Server.Name,
		ServerDescription: p.config.Server.Description,
		AdminPersona:      p.config.Admin.Persona,
		OperatorPrompts:   promptSpecsFromConfig(p.config.Server.Prompts),
		BuiltinPrompts:    p.config.Server.BuiltinPrompts,
		Registry:          p.toolkitRegistry,
	})
}

// promptSpecsFromConfig translates operator-configured prompts into the owner's
// caller-neutral PromptSpec shape, keeping the platform config types out of the
// promptlayer package.
func promptSpecsFromConfig(cfgs []PromptConfig) []promptlayer.PromptSpec {
	specs := make([]promptlayer.PromptSpec, 0, len(cfgs))
	for _, c := range cfgs {
		spec := promptlayer.PromptSpec{
			Name:        c.Name,
			Description: c.Description,
			Content:     c.Content,
		}
		for _, a := range c.Arguments {
			spec.Arguments = append(spec.Arguments, promptlayer.PromptArgSpec{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
			})
		}
		specs = append(specs, spec)
	}
	return specs
}

// buildFieldEncryptor creates a FieldEncryptor from the ENCRYPTION_KEY env var.
// The key can be provided as hex (64 hex chars), base64 (44 chars), or raw bytes (32 bytes).
// Returns nil encryptor (encryption disabled) if the env var is not set.
func buildFieldEncryptor() (*fieldcrypt.FieldEncryptor, error) {
	keyStr := os.Getenv("ENCRYPTION_KEY")
	if keyStr == "" {
		slog.Warn("connection store: ENCRYPTION_KEY not set — sensitive fields stored in plain text")
		return nil, nil //nolint:nilnil // nil encryptor = encryption disabled
	}

	key := decodeEncryptionKey(keyStr)

	encryptor, err := fieldcrypt.NewFieldEncryptor(key)
	if err != nil {
		return nil, fmt.Errorf("initializing field encryptor: %w", err)
	}
	slog.Info("connection store: encryption enabled")
	return encryptor, nil
}

// decodeEncryptionKey tries hex, then base64, then raw bytes to decode the key.
func decodeEncryptionKey(keyStr string) []byte {
	// Try hex first (64 hex chars = 32 bytes).
	if key, err := hex.DecodeString(keyStr); err == nil && len(key) == fieldcrypt.KeyLength {
		return key
	}

	// Try base64 (44 chars = 32 bytes).
	if key, err := base64.StdEncoding.DecodeString(keyStr); err == nil && len(key) == fieldcrypt.KeyLength {
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
// Encodes tools.deny as a JSON array so it round-trips through the
// string-valued config_entries store.
func (p *Platform) buildConfigEntryMap() map[string]string {
	m := map[string]string{
		ConfigKeyServerDescription:       p.config.Server.Description,
		ConfigKeyServerAgentInstructions: p.config.Server.AgentInstructions,
	}
	if len(p.config.Tools.Deny) > 0 {
		if buf, err := json.Marshal(p.config.Tools.Deny); err == nil {
			m[ConfigKeyToolsDeny] = string(buf)
		}
	}
	return m
}

// applyConfigEntry updates a live config field for a whitelisted key.
func (p *Platform) applyConfigEntry(key, value string) {
	p.config.ApplyConfigEntry(key, value)
}

// oauthSigningKeys bundles the active OAuth JWT signing key with the verify-only
// previous keys retained across a rotation, so the Platform carries both in a
// single field.
type oauthSigningKeys struct {
	// current signs new tokens and verifies tokens bearing its kid.
	current []byte
	// previous verify tokens minted with a prior key during a rotation window.
	previous [][]byte
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
	p.oauthKeys.current = signingKey

	previous, err := parsePreviousSigningKeys(p.config.OAuth.PreviousSigningKeys)
	if err != nil {
		return fmt.Errorf("configuring OAuth previous signing keys: %w", err)
	}
	p.oauthKeys.previous = previous
	return nil
}

// parsePreviousSigningKeys decodes the verify-only previous signing keys used to
// keep live sessions valid across a rotation. Each is decoded and length-checked
// by the same decodeSigningKey helper as the active key, so both key classes
// validate identically. A free function (not a *Platform method) so it does not
// widen the Platform method surface.
func parsePreviousSigningKeys(raw []string) ([][]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	keys := make([][]byte, 0, len(raw))
	for i, encoded := range raw {
		key, err := decodeSigningKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("previous signing key %d: %w", i, err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// decodeSigningKey decodes a base64-encoded HMAC signing key and enforces the
// minimum length. Shared by the active key and the verify-only previous keys so
// the two never drift apart in how they are decoded or validated.
func decodeSigningKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decoding signing key: %w", err)
	}
	if len(key) < minSigningKeyLength {
		return nil, fmt.Errorf("signing key must be at least %d bytes", minSigningKeyLength)
	}
	return key, nil
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

	// Inject providers for cross-enrichment
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
	connbackfill.Run(context.Background(), p.db, p.toolkitRegistry.All())

	return nil
}

// initAuth initializes authentication and authorization components. The
// authenticator and authorizer are built by pkg/platform/iam from an
// explicit input; each is skipped when the caller injects its own (test wiring),
// so an override never triggers construction of the other (#756, #828).
func (p *Platform) initAuth(opts *Options) error {
	in := p.authInput()

	if opts.Authenticator != nil {
		p.authenticator = opts.Authenticator
	} else {
		identity, err := iam.NewIdentity(in)
		if err != nil {
			return fmt.Errorf("creating authenticator: %w", err)
		}
		p.authenticator = identity.Authenticator
		p.apiKeyAuth = identity.APIKeyAuth
	}

	// Record every authenticated person in the known-users directory (#614).
	// Wrapping the authenticator catches all auth paths (MCP, portal, admin)
	// at their single Authenticate() chokepoint. The observer is best-effort
	// and asynchronous, so it never blocks or fails authentication.
	if p.users.Directory() != nil {
		p.authenticator = auth.NewObservingAuthenticator(p.authenticator, p.users.ObserveAuthenticated)
	}

	if opts.Authorizer != nil {
		p.authorizer = opts.Authorizer
	} else {
		p.authorizer = iam.NewAuthorizer(in)
	}

	// Initialize browser session (OIDC login + cookie auth) when enabled.
	if err := p.initBrowserSession(); err != nil {
		return fmt.Errorf("initializing browser session: %w", err)
	}

	return nil
}

// authInput translates the platform config, OAuth signing key and persona
// registry into the explicit input the iam package consumes, keeping the
// platform-local config types on this side of the package boundary.
func (p *Platform) authInput() iam.Input {
	oidc := p.config.Auth.OIDC

	keys := make([]auth.APIKey, 0, len(p.config.Auth.APIKeys.Keys))
	for _, k := range p.config.Auth.APIKeys.Keys {
		keys = append(keys, auth.APIKey{
			Key:         k.Key,
			Name:        k.Name,
			Email:       k.Email,
			Description: k.Description,
			Roles:       k.Roles,
		})
	}

	return iam.Input{
		OAuthEnabled: p.config.OAuth.Enabled,
		OAuthJWT: auth.OAuthJWTConfig{
			Issuer:              p.config.OAuth.Issuer,
			SigningKey:          p.oauthKeys.current,
			PreviousSigningKeys: p.oauthKeys.previous,
			RoleClaimPath:       oidc.RoleClaimPath,
			RolePrefix:          oidc.RolePrefix,
		},
		OIDCEnabled: oidc.Enabled,
		OIDC: auth.OIDCConfig{
			Issuer:        oidc.Issuer,
			ClientID:      oidc.ClientID,
			Audience:      oidc.Audience,
			RoleClaimPath: oidc.RoleClaimPath,
			RolePrefix:    oidc.RolePrefix,
		},
		APIKeysEnabled:  p.config.Auth.APIKeys.Enabled,
		APIKeys:         keys,
		AllowAnonymous:  p.config.Auth.AllowAnonymous,
		PersonaRegistry: p.personaRegistry,
		RoleClaimPath:   oidc.RoleClaimPath,
		RolePrefix:      oidc.RolePrefix,
		OIDCToPersona:   p.config.Personas.RoleMapping.OIDCToPersona,
	}
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

	oidcCfg := p.config.Auth.OIDC
	redirectURI := p.config.Portal.PublicBaseURL + "/portal/auth/callback"

	bs, err := browserauth.New(context.Background(), browserauth.Config{
		CookieName:         bsCfg.CookieName,
		Domain:             bsCfg.Domain,
		SameSite:           bsCfg.SameSite,
		Secure:             bsCfg.IsSecure(),
		TTL:                bsCfg.TTL,
		SigningKey:         keyBytes,
		Issuer:             oidcCfg.Issuer,
		ClientID:           oidcCfg.ClientID,
		ClientSecret:       oidcCfg.ClientSecret,
		Scopes:             oidcCfg.Scopes,
		RoleClaim:          oidcCfg.RoleClaimPath,
		RolePrefix:         oidcCfg.RolePrefix,
		RedirectURI:        redirectURI,
		PostLogoutRedirect: p.config.Portal.PublicBaseURL + browsersession.DefaultPortalPath,
		// Portal/admin SPA users log in via the session cookie, so the token
		// authenticator's ObservingAuthenticator never sees them; record them
		// here at login instead (#614).
		OnLogin: p.users.ObserveBrowserLogin,
	})
	if err != nil {
		return fmt.Errorf("initializing browser session: %w", err)
	}

	p.browserSession = bs

	slog.Info("browser session enabled",
		"issuer", oidcCfg.Issuer,
		"redirect_uri", redirectURI,
	)

	return nil
}

// initAudit initializes audit logging.
func (p *Platform) initAudit(opts *Options) error {
	// Use provided audit logger if available. An injected logger is used
	// verbatim (no async-writer wrapping): the audit middleware calls Log
	// synchronously, so the caller is responsible for making it non-blocking
	// — see WithAuditLogger (#884).
	if opts.AuditLogger != nil {
		p.auditLogger = opts.AuditLogger
		return nil
	}

	// Audit is enabled by default when a database is available.
	if isExplicitlyDisabled(p.config.Audit.Enabled) || p.db == nil {
		p.auditLogger = &middleware.NoopAuditLogger{}
		return nil
	}

	// Reject a typo'd delivery mode at boot rather than silently resolving it
	// to async and dropping the durability a compliance deployment asked for.
	if err := p.config.Audit.ValidateDelivery(); err != nil {
		return err
	}

	// Create PostgreSQL audit store
	store := auditpostgres.New(p.db, auditpostgres.Config{
		RetentionDays: p.config.Audit.RetentionDays,
	})

	// Start background cleanup routine
	store.StartCleanupRoutine(24 * time.Hour)

	delivery := p.config.Audit.DeliveryMode()
	p.auditStore = store
	p.auditLogger = newAuditLogger(store, delivery, p.obs.Metrics())

	slog.Info("audit logging enabled",
		"retention_days", p.config.Audit.RetentionDays,
		"log_tool_calls", p.config.Audit.IsToolCallLoggingEnabled(),
		"log_parameters", p.config.Audit.IsParameterLoggingEnabled(),
		"redact_keys", len(p.config.Audit.RedactKeys),
		"delivery", delivery,
	)
	return nil
}

// newAuditLogger wraps the audit store in the writer selected by the configured
// delivery mode (#898) and adapts it to the middleware.AuditLogger interface.
//
// Async (default): a bounded writer with a single drain goroutine, a per-write
// timeout, and drain-on-shutdown replaces the middleware's old per-call detached
// goroutine, which grew without bound under a stalled store (#884); a sustained
// outage sheds events. Sync: write on the request goroutine with a per-write
// timeout, trading tool-call latency for backpressure and zero queue-overflow
// drops. Either way the adapter owns the writer; for async it drains the writer
// on Close via the platform's existing audit-logger Closer path, so no extra
// Platform field is held.
func newAuditLogger(store audit.Logger, delivery string, m *observability.Metrics) middleware.AuditLogger {
	if delivery == AuditDeliverySync {
		return middleware.NewAuditStoreAdapter(audit.NewSyncWriter(store, audit.WithSyncMetrics(m)))
	}
	return middleware.NewAuditStoreAdapter(audit.NewAsyncWriter(store, audit.WithMetrics(m)))
}

// initSessions assembles the session / cross-replica-sync layer (session
// store, enrichment-dedup cache, client broadcaster, reload bus) behind the
// sessionsync owner handle. It resolves the platform's config defaults and
// injects the reload re-materialization handlers (which stay on Platform), then
// applies the owner's stateless-forcing signal to the SDK's streamable config.
// The cache is built later in buildEnrichmentConfig via the handle's StartCache.
func (p *Platform) initSessions(opts *Options) error {
	ttl := p.config.Sessions.TTL
	if ttl == 0 {
		ttl = defaultSessionTimeout
	}
	cleanupInterval := p.config.Sessions.CleanupInterval
	if cleanupInterval == 0 {
		cleanupInterval = defaultCleanupInterval
	}

	handle, err := sessionsync.New(p.db, sessionsync.Config{
		Store:            p.config.Sessions.Store,
		TTL:              ttl,
		CleanupInterval:  cleanupInterval,
		DSN:              p.config.Database.DSN,
		BroadcastChannel: p.config.Sessions.BroadcastChannel,
	}, opts.SessionStore, sessionsync.ReloadHandlers{
		Connection: p.reloadConnectionLocal,
		Catalog:    p.reloadCatalogLocal,
		Persona:    p.reloadPersonaLocal,
		APIKey:     p.reloadAPIKeyLocal,
	})
	if err != nil {
		return fmt.Errorf("init sessions: %w", err)
	}
	p.sessions = handle

	// The database store bypasses the SDK's built-in session map; the owner
	// reports this so Platform (which owns the config) applies it.
	if handle.StatelessForced() {
		p.config.Server.Streamable.Stateless = true
	}
	return nil
}

// initOAuth initializes the OAuth server if enabled. It translates platform
// config into oauthserver.Config and delegates assembly to the owner package;
// the returned Handle owns the server plus its store-closer/cleanup, closed at
// platform shutdown.
func (p *Platform) initOAuth() error {
	if !p.config.OAuth.Enabled {
		return nil
	}

	cfg := oauthserver.Config{
		Issuer:         p.config.OAuth.Issuer,
		AccessTokenTTL: 1 * time.Hour,
		SigningKey:     p.oauthKeys.current,
		Clients:        make([]oauthserver.Client, 0, len(p.config.OAuth.Clients)),
		DCR: oauthserver.DCR{
			Enabled:                 p.config.OAuth.DCR.Enabled,
			AllowedRedirectPatterns: p.config.OAuth.DCR.AllowedRedirectPatterns,
			AllowAllRedirectURIs:    p.config.OAuth.DCR.AllowAllRedirectURIs,
		},
		RateLimit: oauthserver.RateLimit{
			Enabled:        p.config.OAuth.RateLimit.Enabled,
			TrustedProxies: p.config.OAuth.RateLimit.TrustedProxies,
			TokenRPM:       p.config.OAuth.RateLimit.Token.RequestsPerMinute,
			TokenBurst:     p.config.OAuth.RateLimit.Token.Burst,
			RegisterRPM:    p.config.OAuth.RateLimit.Register.RequestsPerMinute,
			RegisterBurst:  p.config.OAuth.RateLimit.Register.Burst,
		},
		DB:      p.db,
		Metrics: p.obs.Metrics(),
	}
	for _, clientCfg := range p.config.OAuth.Clients {
		cfg.Clients = append(cfg.Clients, oauthserver.Client{
			ID:           clientCfg.ID,
			Secret:       clientCfg.Secret,
			RedirectURIs: clientCfg.RedirectURIs,
		})
	}
	if p.config.OAuth.Upstream != nil {
		cfg.Upstream = &oauthserver.Upstream{
			Issuer:                p.config.OAuth.Upstream.Issuer,
			ClientID:              p.config.OAuth.Upstream.ClientID,
			ClientSecret:          p.config.OAuth.Upstream.ClientSecret,
			RedirectURI:           p.config.OAuth.Upstream.RedirectURI,
			AuthorizationEndpoint: p.config.OAuth.Upstream.AuthorizationEndpoint,
			TokenEndpoint:         p.config.OAuth.Upstream.TokenEndpoint,
		}
	}

	handle, err := oauthserver.New(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("initializing OAuth server: %w", err)
	}
	p.oauthHandle = handle

	// The in-memory (single-replica) state-store path runs a cleanup ticker that
	// must be canceled when the lifecycle stops so repeated start/stop cycles do
	// not leak the goroutine. The database path returns a nil cancel (its store
	// sweeps itself and is closed at platform Close).
	if cancel := handle.StateStoreCleanup(); cancel != nil {
		p.lifecycle.OnStop(func(context.Context) error {
			cancel()
			return nil
		})
	}
	return nil
}

// parseOrGenerateSigningKey parses the configured signing key or generates a random one.
func (p *Platform) parseOrGenerateSigningKey() ([]byte, error) {
	if p.config.OAuth.SigningKey != "" {
		return decodeSigningKey(p.config.OAuth.SigningKey)
	}

	// No key configured. On an HTTP deployment, refuse to auto-generate: a
	// per-process key makes each replica reject tokens minted by its peers.
	// Config.Validate reports the same condition, but Validate is not on every
	// startup path, so this is the genuine boot-time gate. stdio and the
	// explicit ephemeral escape hatch fall through to generation.
	if p.config.requiresConfiguredSigningKey() {
		return nil, errors.New(errOAuthSigningKeyRequiredMsg)
	}

	// Generate random key if not configured (not recommended for production)
	key := make([]byte, minSigningKeyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating random key: %w", err)
	}
	if isHTTPTransport(p.config.Server.Transport) {
		slog.Warn("OAuth signing key not configured; generated an ephemeral per-process key. " +
			"UNSAFE for multi-replica deployments: replicas reject each other's tokens. " +
			"Configure oauth.signing_key for production.")
	} else {
		slog.Warn("OAuth signing key not configured, generated random key (tokens won't survive restart)")
	}
	return key, nil
}

// initTuning initializes tuning components. The file-based prompt manager is
// owned by the prompt layer (assembled in initPromptStore), so only the rule
// engine is built here.
func (p *Platform) initTuning(opts *Options) {
	if opts.RuleEngine != nil {
		p.ruleEngine = opts.RuleEngine
	} else {
		rules := &tuning.Rules{
			QualityThreshold: p.config.Tuning.Rules.QualityThreshold,
		}
		p.ruleEngine = tuning.NewRuleEngine(rules)
	}
}

// initWorkflow initializes the session workflow tracker for the search-first
// gate. Enabled by default; only require_search: false skips it.
func (p *Platform) initWorkflow() {
	if !p.config.Workflow.IsRequireSearchEnabled() {
		return
	}

	sessionTimeout := p.config.Server.Streamable.SessionTimeout
	if sessionTimeout == 0 {
		sessionTimeout = defaultSessionTimeout
	}

	// The discovery signal must be shared across replicas: with only an
	// in-memory store, a query load-balanced to a replica that did not handle
	// the search is wrongly refused (#789). Use the Postgres store whenever a
	// database is available (the same condition under which sessions
	// externalize); fall back to in-memory for single-replica / no-DB runs.
	var store searchgate.Store
	if p.db != nil {
		store = searchgatepostgres.New(p.db, sessionTimeout)
	} else {
		store = searchgate.NewMemoryStore(sessionTimeout)
	}

	p.workflowTracker = middleware.NewSessionWorkflowTracker(
		p.config.Workflow.DiscoveryTools,
		p.config.Workflow.QueryTools,
		store,
		sessionTimeout,
	)
	p.workflowTracker.StartCleanup(1 * time.Minute)

	slog.Info("search-first gate enabled",
		"discovery_tools", len(p.workflowTracker.DiscoveryToolNames()),
		"query_tools", len(p.workflowTracker.QueryToolNames()),
		"store", searchgateStoreKind(p.db),
	)
}

// searchgateStoreKind names the discovery store backing for logging.
func searchgateStoreKind(db *sql.DB) string {
	if db != nil {
		return "postgres"
	}
	return "memory"
}

// initSessionGate initializes the session initialization gate if configured.
func (p *Platform) initSessionGate() {
	if !p.config.SessionGate.Enabled {
		return
	}

	// Explicit session handles (#792) supersede the legacy transport-keyed gate:
	// running both double-gates (platform_info records init under the transport
	// session while later calls carry the handle), so skip it when handles are on.
	// The gate's exempt_tools are carried into the handle resolver instead.
	if p.config.Sessions.Handles.IsEnabled() {
		slog.Info("session gate: superseded by explicit session handles (sessions.handles)",
			"exempt_tools", p.config.SessionGate.ExemptTools)
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

// initKnowledge assembles the knowledge-capture layer via the knowledgelayer
// owner: the insight store (the memory-backed adapter over memory_records when
// the memory store is present, else the legacy Postgres store), the changeset
// store + DataHub writer for apply_knowledge, and the capture_insight /
// apply_knowledge toolkit behind one Handle. It translates platform config into
// the owner's Config (resolving the DataHub connection through toolkitcfg so the
// owner stays free of that coupling), delegates assembly, then keeps the
// prompt-creator wiring and toolkit registration here (both reach back into
// Platform / the shared registry). Knowledge tools require database persistence —
// without a database the toolkit is not registered and its tools won't appear in
// tools/list. Must run after initMemory so the memory store is available for the
// insight adapter.
func (p *Platform) initKnowledge() error {
	if isExplicitlyDisabled(p.config.Knowledge.Enabled) || p.db == nil {
		return nil
	}

	apply := p.config.Knowledge.Apply
	applyEnabled := apply.IsEnabled()
	handle, err := knowledgelayer.New(p.db, p.memory.MemoryStore(), p.embeddingProv, knowledgelayer.Config{
		ToolkitName:              instanceDefault,
		ApplyEnabled:             applyEnabled,
		ApplyDataHubConnection:   apply.DataHubConnection,
		ApplyRequireConfirmation: apply.RequireConfirmation,
		PageGuards:               p.config.Knowledge.Pages.Resolve(),
		DataHub:                  p.resolveKnowledgeDataHubConfig(apply.DataHubConnection, applyEnabled),
	})
	if err != nil {
		return fmt.Errorf("creating knowledge layer: %w", err)
	}
	p.knowledge = handle

	// Wire prompt creator for add_prompt change type through the prompt layer's
	// adapter. Toolkit registration stays a Platform/registry concern, so this is
	// applied here through the handle's toolkit. The adapter is nil on a no-DB
	// deployment (no store to create into).
	if pc := p.prompts.PromptCreator(); pc != nil {
		handle.Toolkit().SetPromptCreator(pc)
	}

	// Toolkit registration stays a Platform/registry concern.
	if err := p.toolkitRegistry.Register(handle.Toolkit()); err != nil {
		return fmt.Errorf("registering knowledge toolkit: %w", err)
	}
	return nil
}

// resolveKnowledgeDataHubConfig resolves the apply_knowledge DataHub connection
// into the owner's config shape, keeping Platform's toolkitcfg coupling out of
// the knowledgelayer package. It returns nil when apply is disabled or the
// connection is not configured (the owner then selects the noop writer with a
// WARN).
func (p *Platform) resolveKnowledgeDataHubConfig(connName string, applyEnabled bool) *knowledgelayer.DataHubConfig {
	if !applyEnabled {
		return nil
	}
	dhCfg := toolkitcfg.DataHubConfig(p.config.Toolkits, connName)
	if dhCfg == nil {
		return nil
	}
	return &knowledgelayer.DataHubConfig{
		URL:     dhCfg.URL,
		Token:   dhCfg.Token,
		Timeout: dhCfg.Timeout,
		Debug:   dhCfg.Debug,
	}
}

// initSearch wires the universal, topology-free discovery entry point (#645):
// one search tool over a router that federates every searchable source the
// caller can access — the per-user stores initialized in initExtensions plus
// the technical catalog, prompts, API endpoints, and connections. Each provider
// registers only when its backing source exists, so the tool appears whenever
// at least one source is available and is skipped entirely on a store-less
// (no-database) deployment with no catalog, endpoints, or connections.
func (p *Platform) initSearch() error {
	// Assemble the search federation behind one handle from the searchable source
	// handles/stores + the shared semantic provider / registry / embedding
	// provider (all stay owned by Platform). New returns nil when no source is
	// searchable, so a store-less deployment with no catalog, endpoints, or
	// connections registers no search tool.
	p.searchFed = searchfed.New(searchfed.Config{
		ToolkitName:        instanceDefault,
		ProviderTimeout:    p.config.Knowledge.SearchProviderTimeout, // 0 keeps the default
		EmbedTimeout:       p.config.Knowledge.SearchEmbedTimeout,    // 0 keeps the default
		CatalogEnabled:     p.config.Semantic.Provider == kindDataHub,
		SemanticProvider:   p.semanticProvider,
		MemoryStore:        p.memory.MemoryStore(),
		InsightStore:       p.knowledge.InsightStore(),
		KnowledgePageStore: p.portalStore.KnowledgePageStore(),
		AssetStore:         p.portalStore.AssetStore(),
		ThreadStore:        p.portalStore.ThreadStore(),
		PromptStore:        p.prompts.Store(),
		Registry:           p.toolkitRegistry,
		Embedding:          p.embeddingProv,
	})

	if p.searchFed == nil {
		return nil
	}

	// Toolkit registration stays a Platform/registry concern.
	if err := p.toolkitRegistry.Register(p.searchFed.Toolkit()); err != nil {
		return fmt.Errorf("registering search toolkit: %w", err)
	}
	return nil
}

// initPortal initializes the asset portal toolkit if enabled.
// The portal requires a database for metadata and an S3 connection for content storage.
func (p *Platform) initPortal() error {
	if isExplicitlyDisabled(p.config.Portal.Enabled) || p.db == nil {
		return nil
	}

	// Create S3 client from referenced S3 connection. Built here (Platform owns
	// the toolkit config lookup) and passed into the owner; nil in database-only
	// mode when no s3_connection is configured.
	var s3Client portal.S3Client
	if p.config.Portal.S3Connection != "" {
		var clientErr error
		s3Client, clientErr = p.createPortalS3Client()
		if clientErr != nil {
			return fmt.Errorf("creating portal S3 client: %w", clientErr)
		}
	} else {
		slog.Warn("portal: no s3_connection configured; artifacts will be saved to database only")
	}

	// Assemble the portal store layer (six stores + S3 client + artifact
	// toolkit) behind one handle from p.db + the resolved S3 client +
	// embeddingProv (both stay owned by Platform).
	p.portalStore = portalstore.New(p.db, s3Client, p.embeddingProv, portalstore.Config{
		Name:           instanceDefault,
		S3Bucket:       p.config.Portal.S3Bucket,
		S3Prefix:       p.config.Portal.S3Prefix,
		BaseURL:        p.config.Portal.PublicBaseURL,
		MaxContentSize: p.config.Portal.MaxContentSize,
	})

	// provenanceTracker is a middleware primitive wired into the middleware
	// chain, not a portal store; it stays on Platform.
	p.provenanceTracker = middleware.NewProvenanceTracker()

	// Registration stays a Platform/registry concern.
	if err := p.toolkitRegistry.Register(p.portalStore.Toolkit()); err != nil {
		return fmt.Errorf("registering portal toolkit: %w", err)
	}

	slog.Info("portal enabled",
		"s3_connection", p.config.Portal.S3Connection,
		"s3_bucket", p.config.Portal.S3Bucket,
	)

	// Wire trino_export if portal + trino are both configured
	p.wireTrinoExport()
	// Same wiring for api_export — uses the same portal asset
	// store + S3 client so the model gets a single "exports"
	// surface in the portal regardless of source toolkit.
	p.wireAPIGatewayExport()

	return nil
}

// createPortalS3Client creates an S3Client from the referenced S3 connection config.
func (p *Platform) createPortalS3Client() (portal.S3Client, error) {
	connName := p.config.Portal.S3Connection
	s3Cfg := toolkitcfg.S3Config(p.config.Toolkits, connName)
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
	return s3adapter.New(c), nil
}

// wireTrinoExport injects portal dependencies into Trino toolkits for trino_export.
func (p *Platform) wireTrinoExport() {
	if isExplicitlyDisabled(p.config.Portal.Export.Enabled) {
		slog.Debug("trino_export: disabled by config")
		return
	}
	if p.portalStore.S3Client() == nil || p.portalStore.AssetStore() == nil {
		slog.Debug("trino_export: portal S3 or asset store not configured, skipping")
		return
	}

	trinoToolkits := p.toolkitRegistry.GetByKind("trino")
	if len(trinoToolkits) == 0 {
		slog.Debug("trino_export: no trino toolkits registered, skipping")
		return
	}

	exportCfg := p.parseExportConfig()

	trinoExporter := exportadapters.NewTrinoExporter(
		p.portalStore.AssetStore(), p.portalStore.VersionStore(), p.portalStore.ShareStore(), p.config.Portal.PublicBaseURL,
	)

	for _, tk := range trinoToolkits {
		trinoTk, ok := tk.(*trinokit.Toolkit)
		if !ok {
			continue
		}
		trinoTk.SetExportDeps(trinokit.ExportDeps{
			AssetStore:   trinoExporter,
			VersionStore: trinoExporter,
			S3Client:     p.portalStore.S3Client(),
			ShareCreator: trinoExporter,
			S3Bucket:     p.config.Portal.S3Bucket,
			S3Prefix:     p.config.Portal.S3Prefix,
			BaseURL:      p.config.Portal.PublicBaseURL,
			Config:       exportCfg,
			GetUserContext: func(ctx context.Context) *trinokit.ExportUserContext {
				pc := middleware.GetPlatformContext(ctx)
				if pc == nil {
					return nil
				}
				return &trinokit.ExportUserContext{
					UserID:    pc.UserID,
					UserEmail: pc.UserEmail,
					SessionID: pc.SessionID,
				}
			},
			GetProvenanceCalls: func(ctx context.Context) []trinokit.ExportProvenanceCall {
				calls := middleware.GetProvenanceToolCalls(ctx)
				result := make([]trinokit.ExportProvenanceCall, len(calls))
				for i, c := range calls {
					result[i] = trinokit.ExportProvenanceCall{
						ToolName:   c.ToolName,
						Timestamp:  c.Timestamp,
						Parameters: c.Parameters,
					}
				}
				return result
			},
		})
	}

	slog.Info("trino_export wired",
		"max_rows", exportCfg.MaxRows,
		"max_bytes", exportCfg.MaxBytes,
	)
}

// parseExportConfig converts the portal export config to the trino toolkit's ExportConfig.
func (p *Platform) parseExportConfig() trinokit.ExportConfig {
	cfg := trinokit.ExportConfig{
		MaxRows:  p.config.Portal.Export.MaxRows,
		MaxBytes: p.config.Portal.Export.MaxBytes,
	}
	if p.config.Portal.Export.DefaultTimeout != "" {
		if d, err := time.ParseDuration(p.config.Portal.Export.DefaultTimeout); err == nil {
			cfg.DefaultTimeout = d
		}
	}
	if p.config.Portal.Export.MaxTimeout != "" {
		if d, err := time.ParseDuration(p.config.Portal.Export.MaxTimeout); err == nil {
			cfg.MaxTimeout = d
		}
	}
	return cfg
}

// initManagedResources assembles the managed-resources layer via the
// resourcelayer owner: the Postgres resource store, the S3 blob client, and the
// MCP-server registration behind one Handle. It translates platform config into
// the owner's Config and delegates assembly. No-op (nil handle) when managed
// resources are explicitly disabled or no database is configured. The
// resources/read middleware wiring stays on Platform (addManagedResourceMiddleware).
func (p *Platform) initManagedResources() error {
	if isExplicitlyDisabled(p.config.Resources.Managed.Enabled) || p.db == nil {
		return nil
	}

	handle, err := resourcelayer.New(p.db, resourcelayer.Config{
		S3Connection: p.config.Resources.Managed.S3Connection,
		S3Bucket:     p.config.Resources.Managed.S3Bucket,
		URIScheme:    p.config.Resources.Managed.URIScheme,
		Toolkits:     p.config.Toolkits,
	})
	if err != nil {
		return fmt.Errorf("creating managed-resources layer: %w", err)
	}
	p.resources = handle
	return nil
}

// ResourceStore returns the managed resource store (nil if not enabled).
func (p *Platform) ResourceStore() resource.Store {
	return p.resources.Store()
}

// ResourceS3Client returns the S3 client for managed resources (nil if not configured).
func (p *Platform) ResourceS3Client() resource.S3Client {
	return p.resources.S3Client()
}

// RegisterManagedResource registers a managed resource with the MCP server so it
// appears in the SDK's native resource list. Delegates to the resourcelayer
// owner, passing the live p.mcpServer (created after the resource layer is
// built); wired as the create callback of the REST resources API.
func (p *Platform) RegisterManagedResource(res *resource.Resource) {
	p.resources.Register(p.mcpServer, res)
	// Emit resources/list_changed so connected clients re-list without
	// reconnecting (#927). Wired as the REST create callback; the startup
	// LoadAll path calls resources.Register directly and does not notify.
	p.resources.NotifyListChanged()
}

// UnregisterManagedResource removes a managed resource from the MCP server's
// resource list. Delegates to the resourcelayer owner; wired as the delete
// callback of the REST resources API.
func (p *Platform) UnregisterManagedResource(uri string) {
	p.resources.Unregister(p.mcpServer, uri)
	// Emit resources/list_changed so connected clients re-list without
	// reconnecting (#927). Wired as the REST delete callback.
	p.resources.NotifyListChanged()
}

// LoadManagedResources registers all existing managed resources with the MCP
// server so they're visible on the first resources/list call. Delegates to the
// resourcelayer owner, passing the live p.mcpServer; called during platform
// initialization after finalizeSetup has created the server.
func (p *Platform) LoadManagedResources() {
	p.resources.LoadAll(p.mcpServer)
}

// initMCPApps initializes MCP Apps support. The branding owner is built first
// (regardless of whether MCP Apps are enabled) so BrandLogoSVG() / BrandURL() /
// ResolveImplementorLogo() resolve from config for every consumer.
func (p *Platform) initMCPApps() error {
	p.branding = branding.New(branding.Config{
		PortalLogo:      p.config.Portal.Logo,
		ImplementorLogo: p.config.Portal.Implementor.Logo,
	})

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
		ToolNames:   []string{defaultInitTool},
		Content:     subFS,
		EntryPoint:  entryPointHTML,
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
	app.Config = p.branding.InjectPortalLogo(app.Config)

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
		app.EntryPoint = entryPointHTML
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

// initializeInstructions is the static bootstrap pointer advertised in
// InitializeResult.instructions (the MCP protocol's designated "how to use this
// server" field). It is deliberately a short routing hint, not the full agent
// guidance: the initialize field is set once at server construction and cannot
// vary per persona (initialization precedes authentication in most flows), so
// the persona-aware, DB-editable guidance stays tool-delivered via platform_info
// (see info_tool.go and the instructions seam). Do not inline the full
// agent_instructions content here; a generic bootstrap sentence is the correct
// scope. The one thing a spec-faithful client needs up front is the call order,
// because every other tool refuses with SESSION_REQUIRED until platform_info runs.
const initializeInstructions = "Call the platform_info tool first. It returns " +
	"platform documentation, workflow requirements, and a session_id that every " +
	"other tool requires. Then call search before any query tool."

// finalizeSetup completes platform initialization.
func (p *Platform) finalizeSetup() {
	// Bind the prompt layer's late collaborators before the middleware chain and
	// tool are registered below, now that the subsystems that build them have
	// initialized.
	p.bindPromptCollaborators()

	// Wire debounced list_changed notifiers for prompts and managed resources
	// through the session broadcaster (cross-replica via LISTEN/NOTIFY). Owned
	// by inline closures + lifecycle OnStop so the platform facade gains no
	// field or method for them — mirrors the #929 rate-limiter wiring. The
	// broadcaster is always non-nil after New (initSessions wires an in-memory
	// or postgres broadcaster during construction); the guard keeps this robust
	// against a future construction path that leaves it nil (#927).
	if b := p.sessions.Broadcaster(); b != nil {
		promptNotifier := listchanged.New(b, "notifications/prompts/list_changed")
		p.prompts.SetListChangedNotifier(promptNotifier)
		p.lifecycle.OnStop(func(context.Context) error { promptNotifier.Stop(); return nil })

		resourceNotifier := listchanged.New(b, "notifications/resources/list_changed")
		p.resources.SetListChangedNotifier(resourceNotifier)
		p.lifecycle.OnStop(func(context.Context) error { resourceNotifier.Stop(); return nil })
	}

	serverCaps := p.buildServerCapabilities()

	// Wire the completion/complete handler on the same condition the capability
	// is advertised, so the declared capability and the implementation stay in
	// lockstep. The completion logic lives in the completionlayer seam so the
	// facade gains no field or method for it (#928, #854).
	var completionHandler func(context.Context, *mcp.CompleteRequest) (*mcp.CompleteResult, error)
	if serverCaps.Completions != nil {
		completionDeps := completionlayer.Deps{
			Authenticator:   p.authenticator,
			Authorizer:      p.authorizer,
			AdminPersona:    p.config.Admin.Persona,
			Semantic:        p.semanticProvider,
			Query:           p.queryProvider,
			Registry:        p.toolkitRegistry,
			PersonaRegistry: p.personaRegistry,
		}
		if p.personaRegistry != nil {
			completionDeps.PersonasForRoles = personasForRolesFunc(p.personaRegistry)
		}
		completionHandler = completionlayer.New(completionDeps).Handler()
	}

	p.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    p.config.Server.Name,
		Version: p.config.Server.Version,
	}, &mcp.ServerOptions{
		SchemaCache:       mcp.NewSchemaCache(),
		Capabilities:      serverCaps,
		Instructions:      initializeInstructions,
		CompletionHandler: completionHandler,
	})

	// Add MCP protocol-level receiving middleware.
	//
	// The chain order is a checked invariant, not a comment (issue #758).
	// receivingMiddlewareChain() declares the canonical execution order
	// (outermost first) and each middleware's ordering dependencies — the
	// canonical case being that every PlatformContext reader (audit, metrics,
	// tracing, reflexive capture, the gates, enrichment) must be inner to the
	// auth/authz middleware that writes PlatformContext via context.WithValue,
	// or the value is invisible downstream. We validate the declared order
	// before touching the server so any accidental reorder fails fast at
	// startup with a named error instead of silently mis-wiring the chain.
	specs := p.receivingMiddlewareChain()
	if err := mwchain.Validate(specs); err != nil {
		panic(fmt.Sprintf("mcp-data-platform: invalid receiving-middleware chain order: %v", err))
	}

	// AddReceivingMiddleware wraps the current handler, so each call makes its
	// middleware the new OUTERMOST layer: the last middleware added runs first.
	// The chain is declared outermost-first, so we register it in reverse
	// (innermost first) to realize that execution order.
	for i := len(specs) - 1; i >= 0; i-- {
		specs[i].Register()
	}
}

// bindPromptCollaborators injects the prompt layer's two late collaborators, now
// that the subsystems that build them have initialized: the embedding provider
// (manage_prompt semantic ranking; nil falls back to lexical) and the portal
// share lister (shared-prompt serving; nil when portal is disabled). Both feed
// the prompts/list visibility middleware and manage_prompt tool wired later in
// finalizeSetup, so this must run before that wiring.
func (p *Platform) bindPromptCollaborators() {
	p.prompts.SetEmbedder(p.embeddingProv)
	if p.portalStore != nil {
		p.prompts.SetShareStore(p.portalStore.ShareStore())
	}
}

// addReflexiveCaptureMiddleware wires reflexive query-error capture (#635) via
// the reflexivecapture package, default-on when the memory subsystem is
// available. The tracker is retained so shutdown can Stop its cleanup loop.
func (p *Platform) addReflexiveCaptureMiddleware() {
	p.reflexiveErrors = reflexivecapture.Wire(reflexivecapture.Deps{
		Enabled:           p.config.Knowledge.ReflexiveCapture.IsEnabled() && p.memory.Toolkit() != nil,
		Server:            p.mcpServer,
		Toolkit:           p.memory.Toolkit(),
		ResolveURNMapping: p.reflexiveURNMapping,
		PersonaAllowsTool: p.reflexivePersonaAllowsTool(),
	})
}

// reflexiveURNMapping resolves the DataHub platform and catalog mapping for a
// connection, falling back to the query-provider mapping when it is unknown.
func (p *Platform) reflexiveURNMapping(connection string) (platform string, catalogMapping map[string]string) {
	if p.connectionSources != nil && connection != "" {
		if src := p.connectionSources.ForConnectionName(connection); src != nil {
			return src.DataHubSourceName, src.CatalogMapping
		}
	}
	m := p.config.Query.URNMapping
	return m.Platform, m.CatalogMapping
}

// reflexivePersonaAllowsTool returns the persona tool-access predicate, or nil
// when no authorizer is configured (no persona gating, allow all).
func (p *Platform) reflexivePersonaAllowsTool() reflexivecapture.PersonaToolCheck {
	if p.authorizer == nil {
		return nil
	}
	return p.personaAllowsTool
}

// addManagedResourceMiddleware registers managed resources middleware when enabled.
func (p *Platform) addManagedResourceMiddleware() {
	if p.resources.Store() == nil {
		return
	}
	cfg := middleware.ManagedResourceConfig{
		Store:         p.resources.Store(),
		S3Client:      p.resources.S3Client(),
		S3Bucket:      p.config.Resources.Managed.S3Bucket,
		URIScheme:     p.resources.URIScheme(),
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
		harvestTools := []string{"save_artifact"}
		if p.hasTrinoExport() {
			harvestTools = append(harvestTools, "trino_export")
		}
		p.mcpServer.AddReceivingMiddleware(
			middleware.MCPProvenanceMiddleware(p.provenanceTracker, harvestTools...),
		)
	}
}

// hasTrinoExport returns true if trino_export is configured.
func (p *Platform) hasTrinoExport() bool {
	if isExplicitlyDisabled(p.config.Portal.Export.Enabled) {
		return false
	}
	if p.portalStore.S3Client() == nil || p.portalStore.AssetStore() == nil {
		return false
	}
	trinoToolkits := p.toolkitRegistry.GetByKind("trino")
	return len(trinoToolkits) > 0
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

// buildSessionResolver constructs the explicit session-handle resolver (#792),
// or nil when handles are disabled (a valid no-op in MCPToolCallMiddleware).
func (p *Platform) buildSessionResolver() *middleware.SessionResolver {
	store := p.sessions.SessionStore()
	if !p.config.Sessions.Handles.IsEnabled() || store == nil {
		return nil
	}
	metrics := p.obs.Metrics()
	// Carry the superseded legacy gate's exempt_tools forward (not silently dropped).
	var exempt []string
	if p.config.SessionGate.Enabled {
		exempt = p.config.SessionGate.ExemptTools
	}
	return middleware.NewSessionResolver(store, middleware.SessionResolverConfig{
		Enabled:     true,
		Require:     p.config.Sessions.Handles.IsRequired(),
		TTL:         p.config.Sessions.Handles.HandleTTL(),
		InitTool:    defaultInitTool,
		ExemptTools: exempt,
		Metric: func(ctx context.Context, source string) {
			metrics.RecordSessionResolution(ctx, source)
		},
	})
}

// addSessionHandleSchemaMiddleware advertises the session_id argument on every
// tool's input schema (except platform_info) when explicit handles are enabled.
func (p *Platform) addSessionHandleSchemaMiddleware() {
	// Same gate as buildSessionResolver: never advertise a session_id the platform
	// neither issues nor validates.
	if !p.config.Sessions.Handles.IsEnabled() || p.sessions.SessionStore() == nil {
		return
	}
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPSessionHandleSchemaMiddleware(defaultInitTool),
	)
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

	// Wire persona-based filtering via the authorizer. The same
	// predicate backs platform_find_tools' read-time filter so the two
	// cannot diverge (see personaAllowsTool).
	if p.authorizer != nil {
		cfg.IsToolAllowedForPersona = p.personaAllowsTool
	}

	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPToolVisibilityMiddleware(cfg),
	)
}

// addPromptVisibilityMiddleware registers prompts/list visibility filtering so
// a caller only sees prompts they are entitled to (global + built-in to all,
// persona prompts to persona members, personal prompts to their owner). Without
// it the shared MCP server returns every database prompt — including other
// users' personal prompts — to every client.
func (p *Platform) addPromptVisibilityMiddleware() {
	cfg := middleware.PromptVisibilityConfig{
		Authenticator: p.authenticator,
		ListVisible:   p.prompts.ListVisible,
		GetByName:     p.prompts.GetByName,
	}
	if p.personaRegistry != nil {
		cfg.PersonasForRoles = personasForRolesFunc(p.personaRegistry)
	}
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPPromptVisibilityMiddleware(cfg),
	)
}

// addDescriptionOverrideMiddleware registers description override middleware.
// Built-in overrides guide agents toward DataHub discovery; config overrides
// (loaded from the file or the database-backed config_entries store) can
// customize or extend them.
//
// The dynamic variant re-resolves the override map on every tools/list call
// so admin-API edits to tool.<name>.description take effect immediately,
// without a platform restart. The cost is a tiny map allocation per
// tools/list — negligible compared to the surrounding RPC.
func (p *Platform) addDescriptionOverrideMiddleware() {
	// Snapshot through the runtime lock on every tools/list call so admin
	// API edits to tool.<name>.description take effect immediately AND
	// concurrent writes don't race the iteration in MergedDescriptionOverrides.
	getOverrides := func() map[string]string {
		return middleware.MergedDescriptionOverrides(p.config.ToolDescriptionOverridesSnapshot())
	}
	if len(getOverrides()) == 0 {
		// No defaults and no overrides configured at startup. The map can
		// later be populated via admin API, but skipping the middleware
		// here matches the prior behavior for the empty-config case.
		// Re-evaluate on the next platform restart.
		return
	}
	p.mcpServer.AddReceivingMiddleware(middleware.MCPDescriptionOverrideMiddlewareDynamic(getOverrides))
}

// addIconMiddleware registers icon injection middleware when icons are configured.
func (p *Platform) addIconMiddleware() {
	if !p.config.Icons.IsEnabled() {
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
		Tools: &mcp.ToolCapabilities{ListChanged: true},
		// Logging is always available for client logging support.
		Logging: &mcp.LoggingCapabilities{},
	}

	// Resources are available when templates or managed resources are enabled.
	resourcesOn := p.config.Resources.IsEnabled() || p.resources.Store() != nil || len(p.config.Resources.Custom) > 0
	if resourcesOn {
		caps.Resources = &mcp.ResourceCapabilities{ListChanged: true}
	}

	// Prompts are available when configured. ListChanged is always true when
	// prompts are advertised: prompts are DB-backed and runtime-mutable
	// (create/update/delete/approve/promote via manage_prompt and the admin
	// API), and the platform emits notifications/prompts/list_changed on every
	// such write (see the prompt layer's notifying store), so the capability is
	// honest in both directions (#927).
	promptsOn := len(p.config.Server.Prompts) > 0 || p.config.Tuning.PromptsDir != "" || !isExplicitlyDisabled(p.config.Knowledge.Enabled)
	if promptsOn {
		caps.Prompts = &mcp.PromptCapabilities{ListChanged: true}
	}

	// Completions serve argument autocompletion for prompts and resource
	// templates, so the capability is advertised exactly when there is something
	// to complete. New wires the CompletionHandler on the same condition (a
	// non-nil caps.Completions), so the declared capability and the
	// implementation stay in lockstep (#928).
	if promptsOn || resourcesOn {
		caps.Completions = &mcp.CompletionCapabilities{}
	}

	return caps
}

// addEnrichmentMiddleware adds the semantic enrichment middleware if any injection is configured.
func (p *Platform) addEnrichmentMiddleware() {
	needsEnrichment := p.config.Enrichment.IsTrinoSemanticEnrichmentEnabled() ||
		p.config.Enrichment.IsDataHubQueryEnrichmentEnabled() ||
		p.config.Enrichment.IsS3SemanticEnrichmentEnabled() ||
		p.config.Enrichment.IsDataHubStorageEnrichmentEnabled()

	if !needsEnrichment {
		return
	}

	enrichCfg := p.buildEnrichmentConfig()
	enrichCfg.WorkflowTracker = p.workflowTracker
	// Memory↔enrichment bridge, or a nil provider when memory is disabled.
	mp := p.memory.MemoryProvider()
	p.mcpServer.AddReceivingMiddleware(
		middleware.MCPSemanticEnrichmentMiddleware(
			p.semanticProvider,
			p.queryProvider,
			p.storageProvider,
			enrichCfg,
			mp,
			knowledgePageProviders(p.portalStore.KnowledgePageStore())...,
		),
	)
}

// knowledgePageProviders returns the page-enrichment provider for the middleware, or
// nothing when no knowledge-page store is configured (so the variadic stays empty).
func knowledgePageProviders(store knowledgepage.ReverseLookup) []middleware.KnowledgePageProvider {
	if store == nil {
		return nil
	}
	return []middleware.KnowledgePageProvider{&knowledgePageEnrichmentBridge{store: store}}
}

// knowledgePageEnrichmentBridge adapts the knowledge-page reverse lookup to
// middleware.KnowledgePageProvider for entity tool-response cross-enrichment (#634).
type knowledgePageEnrichmentBridge struct {
	store knowledgepage.ReverseLookup
}

// PagesForEntities returns the bounded set of pages referencing the given entity URNs.
func (b *knowledgePageEnrichmentBridge) PagesForEntities(ctx context.Context, urns []string, limit int) ([]middleware.KnowledgePageSnippet, error) {
	pages, err := knowledgepage.PagesForURNs(ctx, b.store, urns, limit)
	if err != nil {
		return nil, fmt.Errorf("knowledge-page enrichment lookup: %w", err)
	}
	out := make([]middleware.KnowledgePageSnippet, 0, len(pages))
	for _, pg := range pages {
		out = append(out, middleware.KnowledgePageSnippet{ID: pg.ID, Slug: pg.Slug, Title: pg.Title})
	}
	return out, nil
}

// buildEnrichmentConfig creates the enrichment middleware config, including
// optional session dedup cache setup.
func (p *Platform) buildEnrichmentConfig() middleware.EnrichmentConfig {
	cfg := middleware.EnrichmentConfig{
		EnrichTrinoResults:          p.config.Enrichment.IsTrinoSemanticEnrichmentEnabled(),
		EnrichDataHubResults:        p.config.Enrichment.IsDataHubQueryEnrichmentEnabled(),
		EnrichS3Results:             p.config.Enrichment.IsS3SemanticEnrichmentEnabled(),
		EnrichDataHubStorageResults: p.config.Enrichment.IsDataHubStorageEnrichmentEnabled(),
		ResourceLinksEnabled:        p.config.Resources.IsEnabled(),
		ColumnContextFiltering:      p.config.Enrichment.IsColumnContextFilteringEnabled(),
		SearchSchemaPreview:         p.config.Enrichment.IsSearchSchemaPreviewEnabled(),
		SchemaPreviewMaxColumns:     p.config.Enrichment.EffectiveSchemaPreviewMaxColumns(),
		SemanticFallbackEnabled:     p.config.Enrichment.IsSemanticFallbackEnabled(),
		SemanticFallbackTopK:        p.config.Enrichment.EffectiveSemanticFallbackTopK(),
		MemoryLimit:                 p.config.Enrichment.EffectiveMemoryLimit(),
		MemoryContextBudgetBytes:    p.config.Enrichment.EffectiveMemoryContextBudgetBytes(),
		MemorySummaryBytes:          p.config.Enrichment.EffectiveMemorySummaryBytes(),
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

	if p.config.Enrichment.SessionDedup.IsEnabled() {
		cfg.SessionCache = p.sessions.StartCache(
			p.config.Enrichment.SessionDedup.EntryTTL,
			p.config.Enrichment.SessionDedup.SessionTimeout,
		)
		cfg.DedupMode = middleware.DedupMode(p.config.Enrichment.SessionDedup.EffectiveMode())

		slog.Info("session metadata dedup enabled",
			"mode", p.config.Enrichment.SessionDedup.EffectiveMode(),
			"entry_ttl", p.config.Enrichment.SessionDedup.EntryTTL,
			"session_timeout", p.config.Enrichment.SessionDedup.SessionTimeout,
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
		datahubCfg := toolkitcfg.DataHubConfig(p.config.Toolkits, p.config.Semantic.Instance)
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

		// Instrument before the cache wrap so DataHub request metrics and
		// spans are recorded on the underlying client, not skipped by
		// cache hits. Installed only when metrics or tracing is on.
		if p.obs.Enabled() {
			adapter.SetMetrics(p.obs.Metrics())
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
		trinoCfg := toolkitcfg.TrinoConfig(p.config.Toolkits, p.config.Query.Instance)
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
			EstimateRowCounts: p.config.Enrichment.EstimateRowCounts,
		})
		if err != nil {
			return nil, fmt.Errorf("creating trino query provider: %w", err)
		}
		if p.obs.Enabled() {
			adapter.SetMetrics(p.obs.Metrics())
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
		s3Cfg := toolkitcfg.S3Config(p.config.Toolkits, p.config.Storage.Instance)
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

// Start starts the platform.
func (p *Platform) Start(ctx context.Context) error {
	// Load prompts from prompts_dir
	if err := p.prompts.LoadPrompts(); err != nil {
		return fmt.Errorf("loading prompts: %w", err)
	}

	// Wire toolkit-level metrics BEFORE registering tools: the S3 toolkit
	// installs its mcp-s3 metrics middleware in SetMetrics, which must be in
	// place when RegisterAllTools registers the handlers.
	p.WireToolkitMetrics()

	// Register tools from all toolkits
	p.toolkitRegistry.RegisterAllTools(p.mcpServer)

	// Register platform-level tools
	p.registerInfoTool()
	p.registerConnectionsTool()
	p.registerFindToolsTool()
	p.prompts.RegisterTool(p.mcpServer)

	// Register platform-level prompts from config
	p.prompts.RegisterPlatformPrompts(p.mcpServer)

	// Register user-defined custom resources from config
	p.registerCustomResources()

	// Register resource templates (schema, glossary, availability)
	p.registerResourceTemplates()

	// Validate agent_instructions references against registered tools
	p.validateAgentInstructions()

	// One-time knowledge-page reference backfill (#664 Phase 5), guarded by a
	// sentinel and run in the background so it never delays startup.
	if p.db != nil && p.knowledge.Toolkit() != nil {
		go p.knowledge.Toolkit().RunGuardedBackfill(ctx, p.db, knowledgepage.NewPostgresStore(p.db))
	}

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
	return p.sessions.SessionStore()
}

// OAuthServer returns the OAuth server, or nil if not enabled.
func (p *Platform) OAuthServer() *oauth.Server {
	return p.oauthHandle.Server()
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
func (p *Platform) PersonaStore() personastore.Store {
	return p.personaStore
}

// APIKeyStore returns the API key definition store, or nil if not initialized.
func (p *Platform) APIKeyStore() APIKeyStore {
	return p.apiKeyStore
}

// PromptStore returns the prompt definition store, or nil if not initialized.
// Delegates to the prompt layer owner.
func (p *Platform) PromptStore() prompt.Store {
	return p.prompts.Store()
}

// AllPromptInfos returns all prompt metadata (platform + toolkit). Delegates to
// the prompt layer owner; read by the admin and portal prompt REST handlers.
func (p *Platform) AllPromptInfos() []registry.PromptInfo {
	return p.prompts.AllPromptInfos()
}

// RegisterRuntimePrompt records a prompt's metadata at runtime after a
// create/update. Delegates to the prompt layer owner; called by the admin and
// portal prompt REST handlers.
func (p *Platform) RegisterRuntimePrompt(pr *prompt.Prompt) {
	p.prompts.RegisterRuntimePrompt(pr)
}

// UnregisterRuntimePrompt drops a database prompt's tracked metadata. Delegates
// to the prompt layer owner; called by the admin and portal prompt REST handlers.
func (p *Platform) UnregisterRuntimePrompt(name string) {
	p.prompts.UnregisterRuntimePrompt(name)
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
//
// Implements the "features-on-by-default-when-requirements-are-met" rule:
//
//  1. The mcp gateway toolkit auto-enables whenever a connection store is
//     available — gateway connections are added dynamically through the
//     admin UI, there's no YAML 'instances' block to gate on, and forcing
//     operators to copy `toolkits.mcp.enabled: true` boilerplate before
//     they can save their first connection makes the admin UI silently
//     inert. The "Add Connection" form already exposes mcp as an option;
//     saving must produce a working connection without further config.
//
//  2. Trino and S3 toolkit kinds auto-enable when an operator saves their
//     first DB instance via the admin UI. Pre-fix, the saved row was
//     orphaned: the kind block didn't exist, isToolkitEnabled returned
//     false, mergeConnectionInstance was a no-op, and the toolkit loader
//     never instantiated anything.
//
// Operator-set values are never overridden — if the YAML explicitly sets
// `toolkits.mcp.enabled: false`, that wins.
func (p *Platform) mergeDBConnectionsIntoConfig() {
	if p.connectionStore == nil {
		return
	}
	// Skip the auto-enable path when the store has no durable backing
	// (stateless mode). Without persistence, gateway connections
	// wouldn't survive a restart anyway, and the admin UI's "Add
	// Connection" form would silently discard saves. In that mode the
	// operator must opt in via YAML, the same way they would for
	// trino / s3 / datahub.
	//
	// Uses the interface contract Persistent() rather than a type
	// assertion against *NoopConnectionStore, so future transient or
	// test-only implementations don't accidentally toggle auto-enable
	// behavior on or off.
	if !p.connectionStore.Persistent() {
		return
	}

	if p.config.Toolkits == nil {
		p.config.Toolkits = make(map[string]any)
	}

	// (1) The gateway toolkits need no instance config to be useful —
	// auto-enable so the admin UI's "Add Connection" path produces a
	// live toolkit on the next request. Both the MCP gateway (#338)
	// and the HTTP API gateway (#364) follow the same convention:
	// connections are added dynamically through the admin UI rather
	// than via YAML 'instances' blocks, so the kind has to be
	// pre-enabled for saves to land in a live toolkit.
	p.autoEnableToolkitKind(kindMCP)
	p.autoEnableToolkitKind(kindAPI)

	instances, err := p.connectionStore.List(context.Background())
	if err != nil {
		slog.Warn("failed to load DB connections for toolkit merge", logKeyError, err)
		return
	}
	if len(instances) == 0 {
		return
	}

	// Only merge connections for kinds that support DB management.
	// Datahub is single-instance and managed via YAML only.
	manageableKinds := map[string]bool{
		kindTrino: true,
		kindS3:    true,
		kindMCP:   true,
		kindAPI:   true,
	}

	for _, inst := range instances {
		if manageableKinds[inst.Kind] {
			// (2) Auto-enable the kind so the merge actually has effect.
			p.autoEnableToolkitKind(inst.Kind)
			mergeConnectionInstance(p.config.Toolkits, inst)
		}
	}
}

// autoEnableToolkitKind ensures p.config.Toolkits[kind] exists with
// enabled=true so the toolkit loader will instantiate it. Idempotent and
// non-overriding: if the operator has already declared the kind block
// (enabled OR disabled), their explicit choice is respected.
//
// Logs at Debug, not Info: this is the platform's documented default
// behavior, not an exceptional condition that requires operator
// attention. Operators who want to silence the path entirely can set
// the kind explicitly in YAML (with either enabled state).
func (p *Platform) autoEnableToolkitKind(kind string) {
	if _, exists := p.config.Toolkits[kind]; exists {
		return
	}
	p.config.Toolkits[kind] = map[string]any{cfgKeyEnabled: true}
	slog.Debug("auto-enabled toolkit kind (requirements met, no explicit YAML)",
		"kind", kind)
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
	return p.memory.MemoryStore()
}

// KnowledgeInsightStore returns the insight store, or nil if knowledge is disabled.
func (p *Platform) KnowledgeInsightStore() knowledgekit.InsightStore {
	return p.knowledge.InsightStore()
}

// KnowledgeChangesetStore returns the changeset store, or nil if knowledge apply is disabled.
func (p *Platform) KnowledgeChangesetStore() knowledgekit.ChangesetStore {
	return p.knowledge.ChangesetStore()
}

// KnowledgeDataHubWriter returns the DataHub writer, or nil if knowledge apply is disabled.
func (p *Platform) KnowledgeDataHubWriter() knowledgekit.DataHubWriter {
	return p.knowledge.DataHubWriter()
}

// PortalAssetStore returns the portal asset store, or nil if portal is disabled.
func (p *Platform) PortalAssetStore() portal.AssetStore {
	return p.portalStore.AssetStore()
}

// PortalShareStore returns the portal share store, or nil if portal is disabled.
func (p *Platform) PortalShareStore() portal.ShareStore {
	return p.portalStore.ShareStore()
}

// PortalVersionStore returns the portal version store, or nil if portal is disabled.
func (p *Platform) PortalVersionStore() portal.VersionStore {
	return p.portalStore.VersionStore()
}

// PortalCollectionStore returns the portal collection store, or nil if portal is disabled.
func (p *Platform) PortalCollectionStore() portal.CollectionStore {
	return p.portalStore.CollectionStore()
}

// PortalThreadStore returns the portal feedback thread store, or nil if portal is disabled.
func (p *Platform) PortalThreadStore() portal.ThreadStore {
	return p.portalStore.ThreadStore()
}

// PortalKnowledgePageStore returns the canonical knowledge-page store, or nil
// when the portal is disabled.
func (p *Platform) PortalKnowledgePageStore() knowledgepage.Store {
	return p.portalStore.KnowledgePageStore()
}

// PortalS3Client returns the portal S3 client, or nil if portal is disabled.
func (p *Platform) PortalS3Client() portal.S3Client {
	return p.portalStore.S3Client()
}

// KnowledgeRouter returns the unified search federation, or nil when no
// searchable source is configured. The portal's GET /api/v1/portal/search REST
// endpoint wraps it so the browser surfaces the same grouped, scope-enforced
// results as the MCP search tool.
func (p *Platform) KnowledgeRouter() *knowledge.Router {
	return p.searchFed.Router()
}

// BrandLogoSVG returns the resolved brand logo SVG content (from portal.logo
// or mcpapps platform-info config), or empty string if none is configured.
// Delegates to the branding owner.
func (p *Platform) BrandLogoSVG() string {
	return p.branding.BrandLogoSVG()
}

// BrandURL returns the resolved brand URL from the mcpapps platform-info
// config (brand_url), or empty string if not configured. Delegates to the
// branding owner.
func (p *Platform) BrandURL() string {
	return p.branding.BrandURL()
}

// ResolveImplementorLogo fetches (once, then caches) the implementor logo SVG
// from portal.implementor.logo, or empty string if no logo URL is configured or
// the fetch fails. Delegates to the branding owner.
func (p *Platform) ResolveImplementorLogo() string {
	return p.branding.ResolveImplementorLogo()
}

// BrowserSessionFlow returns the OIDC login flow, or nil if browser sessions are disabled.
func (p *Platform) BrowserSessionFlow() *browsersession.Flow {
	return p.browserSession.Flow()
}

// BrowserSessionAuth returns the cookie-based authenticator, or nil if browser sessions are disabled.
func (p *Platform) BrowserSessionAuth() *browsersession.Authenticator {
	return p.browserSession.Authenticator()
}

// ToolInfo describes a tool registered directly on the platform (not via a toolkit).
type ToolInfo struct {
	Name string
	Kind string
}

// PlatformTools returns tools registered directly on the platform outside of any toolkit.
func (p *Platform) PlatformTools() []ToolInfo {
	tools := []ToolInfo{
		{Name: defaultInitTool, Kind: kindPlatform},
		{Name: toolListConns, Kind: kindPlatform},
	}
	if p.prompts.Store() != nil {
		tools = append(tools, ToolInfo{Name: "manage_prompt", Kind: kindPlatform})
	}
	return tools
}

// injectToolkitPlatformConfig injects platform-level configuration into
// toolkit instance config maps before the registry loader processes them.
// This allows platform-wide settings (e.g., progress.enabled, elicitation)
// to reach toolkit factories via the normal config parsing path.
//
// Both progress.enabled and elicitation.enabled default to true, so this
// runs on nearly every config. It must not clobber an instance that already
// sets progress_enabled or elicitation explicitly under
// toolkits.trino.instances.<name> — those per-instance overrides win.
func (p *Platform) injectToolkitPlatformConfig() {
	instances := p.trinoInstanceConfigs()
	if instances == nil {
		return
	}

	needsProgress := p.config.Progress.IsEnabled()
	needsElicitation := p.config.Elicitation.IsEnabled()

	if !needsProgress && !needsElicitation {
		return
	}

	for name, v := range instances {
		instanceCfg, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if needsProgress {
			if _, exists := instanceCfg["progress_enabled"]; !exists {
				instanceCfg["progress_enabled"] = true
			}
		}
		if needsElicitation {
			if _, exists := instanceCfg["elicitation"]; !exists {
				instanceCfg["elicitation"] = map[string]any{
					cfgKeyEnabled: true,
					"cost_estimation": map[string]any{
						cfgKeyEnabled:   p.config.Elicitation.CostEstimation.IsEnabled(),
						"row_threshold": p.config.Elicitation.CostEstimation.RowThreshold,
					},
					"pii_consent": map[string]any{
						cfgKeyEnabled: p.config.Elicitation.PIIConsent.IsEnabled(),
					},
				}
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
	p.stopBackgroundTrackers()
	p.stopConnOAuthRefresherDuringShutdown(&errs)
	p.flushEnrichmentState()
	p.closeSessionLayer(&errs)
	p.closeAuthEventStore(&errs)
	p.closeAuditLayer(&errs)
	p.closeProvidersAndRegistry(&errs)
	p.closeMetricsLayer(&errs)
	p.closeDatabase(&errs)
	if len(errs) > 0 {
		return fmt.Errorf("errors closing platform: %v", errs)
	}
	slog.Debug("shutdown: platform closed")
	return nil
}

// closeMetricsLayer stops the /metrics listener and flushes the OTel
// MeterProvider and (when enabled) the TracerProvider so buffered spans
// are exported before exit. The listener stop is bounded by a short
// timeout so a stuck scraper cannot delay platform shutdown; the
// provider flushes are best-effort. All calls are nil-safe.
func (p *Platform) closeMetricsLayer(errs *[]error) {
	if p.obs == nil {
		return
	}
	slog.Debug("shutdown: stopping metrics layer")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.ShutdownMetricsListener(ctx); err != nil {
		*errs = append(*errs, err)
	}
	if err := p.obs.Tracer().Shutdown(ctx); err != nil {
		*errs = append(*errs, err)
	}
}

// stopConnOAuthRefresherDuringShutdown waits up to 10s for any
// in-flight refresh to settle before exiting. The Stop call is a
// no-op when the refresher was never started. Named distinctly from
// the exported StopConnOAuthRefresher so revive's confusing-naming
// rule doesn't flag the differ-only-by-casing pair.
func (p *Platform) stopConnOAuthRefresherDuringShutdown(errs *[]error) {
	if p.connAuth == nil {
		return
	}
	// connauth.Stop is a no-op (and logs nothing) when no refresher was ever
	// started, so this bounded stop stays silent unless there is a live loop to
	// wind down.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.connAuth.Stop(ctx); err != nil {
		*errs = append(*errs, err)
	}
}

// closeAuthEventStore stops the connection-auth prune goroutine via the
// connauth handle's Close. Safe to call when no handle was wired (e.g.,
// dev with no DB).
func (p *Platform) closeAuthEventStore(errs *[]error) {
	if p.connAuth == nil {
		return
	}
	slog.Debug("shutdown: closing auth event store")
	closeResource(errs, p.connAuth)
}

// closeSessionLayer tears down the session / cross-replica-sync layer and the
// OAuth store in that order. The sessionsync handle closes the session cache,
// session store, client broadcaster, and reload bus internally in the current
// order; the broadcasters must shut down before the database in Phase 4 because
// the postgres broadcasters hold their own dedicated LISTEN connections.
func (p *Platform) closeSessionLayer(errs *[]error) {
	closeResource(errs, p.sessions)
	if p.oauthHandle != nil {
		slog.Debug("shutdown: closing OAuth server")
		closeResource(errs, p.oauthHandle)
	}
}

// closeAuditLayer closes the audit logger, then the underlying audit store.
// Order matters: the logger is the async-writer-backed adapter, whose Close
// drains the queue through the store before the store (and later the database)
// is closed, so events queued at shutdown are persisted. Draining is bounded by
// the adapter's deadline; events still queued when it expires are abandoned
// (canceled, not written into the closing store) and counted in
// audit_events_dropped_total — audit delivery is best-effort (#884).
func (p *Platform) closeAuditLayer(errs *[]error) {
	if closer, ok := p.auditLogger.(Closer); ok {
		slog.Debug("shutdown: closing audit logger")
		closeResource(errs, closer)
	}
	if p.auditStore != nil {
		slog.Debug("shutdown: closing audit store")
		closeResource(errs, p.auditStore)
	}
}

// closeProvidersAndRegistry releases provider clients and the
// toolkit registry. None of these should hold the database handle —
// the database closes last in closeDatabase.
func (p *Platform) closeProvidersAndRegistry(errs *[]error) {
	slog.Debug("shutdown: closing providers")
	closeResource(errs, p.semanticProvider)
	closeResource(errs, p.queryProvider)
	closeResource(errs, p.storageProvider)
	closeResource(errs, p.portalStore)
	closeResource(errs, p.toolkitRegistry)
}

// closeDatabase closes the database connection last so every other
// component has had a chance to release its handles.
func (p *Platform) closeDatabase(errs *[]error) {
	if p.db == nil {
		return
	}
	slog.Debug("shutdown: closing database")
	if err := p.db.Close(); err != nil {
		*errs = append(*errs, fmt.Errorf("closing database: %w", err))
	}
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
	if p.reflexiveErrors != nil {
		slog.Debug("shutdown: stopping reflexive error tracker")
		p.reflexiveErrors.Stop()
	}
	// Stops the memory layer's staleness watcher (no-op when disabled). Runs
	// before the DB closes, matching the prior teardown order.
	p.memory.Stop()
}

// flushEnrichmentState persists enrichment dedup state from the session cache
// to the session store for continuity across restarts.
func (p *Platform) flushEnrichmentState() {
	cache := p.sessions.SessionCache()
	store := p.sessions.SessionStore()
	if cache == nil || store == nil {
		return
	}

	exported := cache.ExportSessions()
	if len(exported) == 0 {
		return
	}

	ctx := context.Background()
	flushed := 0
	for sessionID, tables := range exported {
		state := map[string]any{"enrichment_dedup": tables}
		if err := store.UpdateState(ctx, sessionID, state); err != nil {
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
	cache := p.sessions.SessionCache()
	store := p.sessions.SessionStore()
	if cache == nil || store == nil {
		return
	}

	ctx := context.Background()
	sessions, err := store.List(ctx)
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
		tables := dedup.ParseState(dedupRaw)
		if len(tables) > 0 {
			cache.LoadSession(sess.ID, tables)
			loaded++
		}
	}
	if loaded > 0 {
		slog.Info("loaded persisted enrichment state", "sessions", loaded)
	}
}

// WireAPIGatewayMemBudget creates the process-wide in-flight memory
// budget (issue #535) and injects the same handle into every registered
// api gateway toolkit so buffered reads across all connections and both
// api_invoke_endpoint and api_export are accounted against one ceiling.
// Idempotent: the budget is created once and re-injected on subsequent
// calls (so toolkits registered later still pick it up). A zero/unset
// max yields a disabled (unlimited) budget that is still safe to call.
func (p *Platform) WireAPIGatewayMemBudget() {
	if p.apiMemBudget == nil {
		p.apiMemBudget = apigatewaykit.NewMemBudget(p.config.APIGateway.Memory.MaxInFlightBytes)
	}
	for _, tk := range p.toolkitRegistry.GetByKind(apigatewaykit.Kind) {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.SetMemBudget(p.apiMemBudget)
		}
	}
	if p.apiMemBudget.Enabled() {
		slog.Info("api gateway in-flight memory budget enabled",
			"max_in_flight_bytes", p.apiMemBudget.Max())
	}
}

// APIGatewayRawMaxBytes returns the configured all-or-nothing size cap
// for the raw passthrough REST route, for the REST shim to enforce as a
// 413. 0 = no cap (the streamed path is memory-bounded regardless).
func (p *Platform) APIGatewayRawMaxBytes() int64 {
	return p.config.APIGateway.Memory.RawMaxBytes
}

// wireAPIGatewayExport injects portal dependencies into api gateway
// toolkits for api_export. Mirrors wireTrinoExport: same portal
// asset store, same S3 client, same share creator. The two
// toolkits each carry their own ExportDeps types (no shared
// interface yet) so a thin set of adapters bridges them to the
// shared portal stores.
func (p *Platform) wireAPIGatewayExport() {
	if isExplicitlyDisabled(p.config.Portal.Export.Enabled) {
		slog.Debug("api_export: disabled by portal.export.enabled")
		return
	}
	if p.portalStore.S3Client() == nil || p.portalStore.AssetStore() == nil {
		slog.Debug("api_export: portal S3 or asset store not configured, skipping")
		return
	}

	apiToolkits := p.toolkitRegistry.GetByKind(apigatewaykit.Kind)
	if len(apiToolkits) == 0 {
		slog.Debug("api_export: no api gateway toolkits registered, skipping")
		return
	}

	// Reuse the same ExportConfig knobs as trino_export — operators
	// configure the cap once and both _export tools honor it.
	tcfg := p.parseExportConfig()
	exportCfg := apigatewaykit.ExportConfig{
		MaxBytes:       tcfg.MaxBytes,
		DefaultTimeout: tcfg.DefaultTimeout,
		MaxTimeout:     tcfg.MaxTimeout,
	}

	apiExporter := exportadapters.NewAPIExporter(
		p.portalStore.AssetStore(), p.portalStore.VersionStore(), p.portalStore.ShareStore(), p.config.Portal.PublicBaseURL,
	)

	for _, tk := range apiToolkits {
		apiTk, ok := tk.(*apigatewaykit.Toolkit)
		if !ok {
			continue
		}
		apiTk.SetExportDeps(apigatewaykit.ExportDeps{
			AssetStore:   apiExporter,
			VersionStore: apiExporter,
			S3Client:     p.portalStore.S3Client(),
			ShareCreator: apiExporter,
			S3Bucket:     p.config.Portal.S3Bucket,
			S3Prefix:     p.config.Portal.S3Prefix,
			BaseURL:      p.config.Portal.PublicBaseURL,
			Config:       exportCfg,
			GetUserContext: func(ctx context.Context) *apigatewaykit.ExportUserContext {
				pc := middleware.GetPlatformContext(ctx)
				if pc == nil {
					return nil
				}
				return &apigatewaykit.ExportUserContext{
					UserID:    pc.UserID,
					UserEmail: pc.UserEmail,
					SessionID: pc.SessionID,
				}
			},
		})
	}

	slog.Info("api_export wired",
		"max_bytes", exportCfg.MaxBytes,
	)
}
