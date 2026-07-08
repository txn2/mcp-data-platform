// Package oauthserver assembles the OAuth 2.1 authorization server and its
// storage behind one Handle: storage selection (Postgres when a database is
// present, else in-memory), bcrypt-hashed pre-registration of configured
// clients, the server itself, its authorization-state cleanup, and metrics
// wiring.
//
// New takes an explicit Config so the server can be built and tested on its
// own; the package imports pkg/oauth (+ pkg/oauth/postgres) and
// pkg/observability, never pkg/platform. Callers translate their own config
// into Config at the boundary.
package oauthserver

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/pkg/oauth"
	oauthpostgres "github.com/txn2/mcp-data-platform/pkg/oauth/postgres"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Client is a pre-registered OAuth client. Its Secret is the plaintext secret;
// New bcrypt-hashes it before persisting.
type Client struct {
	ID           string
	Secret       string
	RedirectURIs []string
}

// DCR carries the Dynamic Client Registration policy.
type DCR struct {
	Enabled                 bool
	AllowedRedirectPatterns []string
	AllowAllRedirectURIs    bool
}

// Upstream identifies the upstream identity provider used for the
// authorization-code flow, when one is configured.
type Upstream struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// Config carries the values New needs to assemble the OAuth server. Callers
// build it from their own config so this package stays free of platform config
// types.
type Config struct {
	Issuer         string
	AccessTokenTTL time.Duration
	SigningKey     []byte
	Clients        []Client
	DCR            DCR
	Upstream       *Upstream

	// DB selects Postgres-backed storage when non-nil; otherwise in-memory
	// storage is used. Ignored when Storage is set. Only the DB path yields a
	// shared (multi-replica) authorization-state store; see New.
	DB *sql.DB
	// Storage overrides storage selection with a pre-chosen backend. When set,
	// DB is ignored and no store-closer is owned by the Handle. An injected
	// Storage backs client/code/token persistence only: the server still uses
	// the in-memory authorization-state store (multi-replica state sharing
	// requires DB). Primarily a test seam.
	Storage oauth.Storage

	// Metrics records token issuance/refresh outcomes; may be nil.
	Metrics *observability.Metrics
}

// Handle owns the assembled OAuth server together with the resources that must
// be torn down at shutdown: the Postgres store's closer (database path) and the
// in-memory state-store cleanup routine's cancellation (single-replica path).
//
// The two torn-down resources belong to different shutdown phases, mirroring the
// original platform wiring: the store-closer is released by Close (the resource
// teardown phase), while the in-memory cleanup cancel is returned by
// StateStoreCleanup for the caller to wire into its lifecycle stop phase.
type Handle struct {
	server       *oauth.Server
	storeCloser  interface{ Close() error }
	stateCleanup context.CancelFunc
}

// Server returns the assembled OAuth server, or nil when the Handle is nil
// (OAuth disabled).
func (h *Handle) Server() *oauth.Server {
	if h == nil {
		return nil
	}
	return h.server
}

// StateStoreCleanup returns the cancel for the in-memory authorization-state
// cleanup routine (single-replica path), or nil on the database path or a nil
// Handle. The caller wires it into its lifecycle stop so the ticker goroutine is
// canceled when the platform stops, without this package importing the caller's
// lifecycle.
func (h *Handle) StateStoreCleanup() context.CancelFunc {
	if h == nil {
		return nil
	}
	return h.stateCleanup
}

// Close closes the Postgres store (database path); it is nil-safe and a no-op on
// the in-memory path, whose cleanup routine is canceled via StateStoreCleanup.
func (h *Handle) Close() error {
	if h == nil || h.storeCloser == nil {
		return nil
	}
	if err := h.storeCloser.Close(); err != nil {
		return fmt.Errorf("closing oauth store: %w", err)
	}
	return nil
}

// resolveStorage picks the storage backend. It returns the storage plus the
// typed Postgres store when the database path is chosen (nil otherwise), so the
// caller can wire it as the shared state store and close it at shutdown.
func (c Config) resolveStorage() (oauth.Storage, *oauthpostgres.Store) {
	if c.Storage != nil {
		return c.Storage, nil
	}
	if c.DB != nil {
		pg := oauthpostgres.New(c.DB)
		return pg, pg
	}
	return oauth.NewMemoryStorage(), nil
}

// New assembles the OAuth server from cfg: it selects storage, pre-registers
// configured clients with bcrypt-hashed secrets, builds the server, wires the
// authorization-state store (shared Postgres store, or an in-memory cleanup
// routine whose cancel is returned by StateStoreCleanup), and attaches metrics.
//
// The Postgres store's cleanup ticker starts before the failure-prone assembly
// steps, so the returned Handle owns its closer immediately: on any error after
// that point, New closes the Handle before returning so the store and its
// goroutine never outlive a failed construction.
func New(ctx context.Context, cfg Config) (*Handle, error) {
	storage, pgStore := cfg.resolveStorage()

	h := &Handle{}
	if pgStore != nil {
		pgStore.StartCleanupRoutine(time.Minute)
		h.storeCloser = pgStore
		slog.Info("OAuth storage: database")
	} else {
		slog.Info("OAuth storage: memory")
	}

	// If assembly fails after a cleanup ticker has started, tear the Handle back
	// down so no goroutine outlives the failed construction. The Handle is never
	// returned on error, so the caller cannot wire StateStoreCleanup itself;
	// cancel both cleanups here regardless of which path started one.
	ok := false
	defer func() {
		if !ok {
			if h.stateCleanup != nil {
				h.stateCleanup()
			}
			_ = h.Close()
		}
	}()

	if err := preRegisterClients(ctx, storage, cfg.Clients); err != nil {
		return nil, err
	}

	serverConfig := oauth.ServerConfig{
		Issuer:         cfg.Issuer,
		AccessTokenTTL: cfg.AccessTokenTTL,
		SigningKey:     cfg.SigningKey,
		DCR: oauth.DCRConfig{
			Enabled:                 cfg.DCR.Enabled,
			AllowedRedirectPatterns: cfg.DCR.AllowedRedirectPatterns,
			AllowAllRedirectURIs:    cfg.DCR.AllowAllRedirectURIs,
			RequirePKCE:             true,
		},
	}
	if cfg.Upstream != nil {
		serverConfig.Upstream = &oauth.UpstreamConfig{
			Issuer:       cfg.Upstream.Issuer,
			ClientID:     cfg.Upstream.ClientID,
			ClientSecret: cfg.Upstream.ClientSecret,
			RedirectURI:  cfg.Upstream.RedirectURI,
		}
	}

	server, err := oauth.NewServer(serverConfig, storage)
	if err != nil {
		return nil, fmt.Errorf("creating OAuth server: %w", err)
	}
	h.server = server

	warnOnDefaultDenyDCR(cfg.DCR)

	// In multi-replica deployments the upstream IdP callback can land on a
	// different replica than the one that started the flow, so in-flight
	// authorization state must live in the shared database when one is
	// configured. The Postgres store's cleanup routine sweeps it; the memory
	// path sweeps via the server's own cleanup routine, whose cancel the caller
	// wires into its lifecycle stop so repeated start/stop cycles do not leak
	// ticker goroutines.
	if pgStore != nil {
		server.SetStateStore(pgStore)
		slog.Info("OAuth state store: database")
	} else {
		// cancelCleanup escapes via h.stateCleanup: the caller invokes it at
		// lifecycle stop (StateStoreCleanup), and the error defer invokes it on a
		// failed construction, so it is never dropped.
		cleanupCtx, cancelCleanup := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel is retained on the Handle and invoked by the caller / error defer, not leaked.
		server.StartCleanupRoutine(cleanupCtx, time.Minute)
		h.stateCleanup = cancelCleanup
		slog.Info("OAuth state store: memory (single-replica)")
	}

	server.SetMetrics(cfg.Metrics)
	ok = true
	return h, nil
}

// preRegisterClients bcrypt-hashes each configured client's secret and persists
// it. The registration endpoint stores hashed secrets, so pre-registered
// clients must match that shape.
func preRegisterClients(ctx context.Context, storage oauth.Storage, clients []Client) error {
	for _, clientCfg := range clients {
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

		if err := storage.CreateClient(ctx, client); err != nil {
			return fmt.Errorf("creating client %s: %w", clientCfg.ID, err)
		}
	}
	return nil
}

// warnOnDefaultDenyDCR surfaces the DCR default-deny at boot: a deployment that
// enabled DCR without patterns previously accepted any redirect URI and now
// denies all registrations. The request-time error repeats the guidance, but
// the operator should learn about it from the startup log, not from the first
// failing client.
func warnOnDefaultDenyDCR(dcr DCR) {
	if dcr.Enabled && len(dcr.AllowedRedirectPatterns) == 0 && !dcr.AllowAllRedirectURIs {
		slog.Warn("OAuth DCR is enabled without allowed_redirect_patterns: all client registrations will be denied. " +
			"Set oauth.dcr.allowed_redirect_patterns, or oauth.dcr.allow_all_redirect_uris: true to explicitly accept any redirect URI.")
	}
}
