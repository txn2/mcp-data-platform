package platformstate

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/platform/capacity"
	"github.com/txn2/mcp-data-platform/internal/platform/thumbworker"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/oidcdiscovery"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// providerNoop is the name the noop semantic and query providers report: a
// deployment with no semantic or query block has no such dependency.
const providerNoop = "noop"

// Auth is which authentication methods the deployment configures.
type Auth struct {
	OIDC, APIKeys, OAuth, Browser bool
	// Issuer is the OIDC issuer, probed for dependency_up{dependency="idp"}.
	Issuer string
}

// Sources is what the platform hands the seam.
type Sources struct {
	DB       *sql.DB
	Semantic semantic.Provider
	Query    query.Provider
	// Objects is the portal's blob store and Bucket its bucket; Objects is nil
	// in database-only mode.
	Objects    any
	Bucket     string
	Auth       Auth
	Thumbnails thumbworker.Config
	Toolkits   *registry.Registry
	Personas   *persona.Registry
	Tracer     *observability.Tracer
	// Interval is server.state_probe_interval; zero is DefaultInterval.
	Interval time.Duration
	// Capacity is where the rest of the platform's objects live, for the
	// capacity gauges (#1899); Objects and Bucket above are the portal's.
	Capacity CapacitySources
}

// CapacitySources is what the capacity service needs beyond the portal's
// client and bucket.
type CapacitySources struct {
	PortalPrefix string
	// Resources is the managed-resources blob client and ResourceBucket its
	// bucket; Resources is nil without one.
	Resources      any
	ResourceBucket string
	// Toolkits is the toolkits configuration, and PortalConnection and
	// ResourceConnection the S3 connections the portal and managed resources
	// name (empty: the default), read for the endpoint each bucket is asked
	// which object store it is on.
	Toolkits           map[string]any
	PortalConnection   string
	ResourceConnection string
}

// Wire records mcp_platform_config_info and starts the sampler over src's
// dependencies. Nil-safe on m.
func Wire(m *observability.Metrics, src Sources) *Sampler {
	m.RecordConfigInfo(context.Background(), observability.ConfigInfo{
		ToolkitKinds: toolkitKinds(src.Toolkits),
		AuthMethods:  src.Auth.methods(),
		Tracing:      src.Tracer.Enabled(),
		SamplerRatio: src.Tracer.SamplerRatio(),
	})
	s := Start(m, Deps{
		DB: src.DB, Dependencies: dependencies(src), Toolkits: src.Toolkits, Personas: src.Personas, Interval: src.Interval,
	})
	s.capacity = startCapacity(m, src)
	return s
}

// startCapacity starts the capacity service over the platform's buckets. A
// malformed capacity variable is logged and its default used, keeping every
// variable that parsed: a typo in a telemetry knob never stops the platform.
func startCapacity(m *observability.Metrics, src Sources) *capacity.Service {
	cfg, err := capacity.ConfigFromEnv()
	if err != nil {
		slog.Warn("capacity: configuration value ignored", "error", logsan.SanitizeForLog(err.Error()))
	}
	return capacity.Start(m, capacitySources(src), cfg)
}

// capacitySources is the layout of the platform's buckets and the client that
// lists each: the portal's, and the managed resources' when they are on a
// bucket of their own or the same one.
func capacitySources(src Sources) capacity.Sources {
	layout := capacity.Layout{PortalPrefix: src.Capacity.PortalPrefix}
	walkers := map[string]capacity.Walker{}
	if w, ok := src.Objects.(capacity.Walker); ok && src.Bucket != "" {
		layout.PortalBucket, walkers[src.Bucket] = src.Bucket, w
	}
	if w, ok := src.Capacity.Resources.(capacity.Walker); ok && src.Capacity.ResourceBucket != "" {
		layout.ResourceBucket, walkers[src.Capacity.ResourceBucket] = src.Capacity.ResourceBucket, w
	}
	endpoints := map[string]string{}
	if layout.PortalBucket != "" {
		endpoints[layout.PortalBucket], _ = capacity.Endpoint(src.Capacity.Toolkits, src.Capacity.PortalConnection)
	}
	if layout.ResourceBucket != "" {
		endpoints[layout.ResourceBucket], _ = capacity.Endpoint(src.Capacity.Toolkits, src.Capacity.ResourceConnection)
	}
	return capacity.Sources{DB: src.DB, Layout: layout, Walkers: walkers, Endpoints: endpoints}
}

// methods is the configured methods as auth_attempts_total names them.
func (a Auth) methods() []string {
	var out []string
	for name, on := range map[string]bool{
		opsobs.AuthMethodOIDC: a.OIDC, opsobs.AuthMethodAPIKey: a.APIKeys,
		opsobs.AuthMethodOAuth: a.OAuth, opsobs.AuthMethodBrowser: a.Browser && a.OIDC,
	} {
		if on {
			out = append(out, name)
		}
	}
	return out
}

// toolkitKinds is the kinds the registry runs.
func toolkitKinds(reg *registry.Registry) []string {
	if reg == nil {
		return nil
	}
	var out []string
	for _, tk := range reg.All() {
		out = append(out, tk.Kind())
	}
	return out
}

// dependencies is the probe of every dependency src configures; the database
// is added by the sampler.
func dependencies(src Sources) map[string]Pinger {
	out := map[string]Pinger{}
	if p := providerPinger(src.Semantic); p != nil {
		out[opsobs.DependencySemantic] = p
	}
	if p := providerPinger(src.Query); p != nil {
		out[opsobs.DependencyQuery] = p
	}
	if lister, ok := src.Objects.(objectLister); ok && src.Bucket != "" {
		out[opsobs.DependencyObjectStorage] = objectProbe{objects: lister, bucket: src.Bucket}
	}
	if src.Auth.OIDC && src.Auth.Issuer != "" {
		out[opsobs.DependencyIDP] = idpProbe{issuer: src.Auth.Issuer, client: outbound.NewClient(outbound.Options{
			Kind: outbound.KindOIDC, CheckRedirect: outbound.FollowRedirects,
		})}
	}
	if src.Thumbnails.IsEnabled() {
		out[opsobs.DependencyRenderer] = headless.New(src.Thumbnails.EffectiveRendererURL(), nil)
	}
	return out
}

// named is a provider's name; a noop provider is no dependency.
type named interface{ Name() string }

// providerPinger finds the Pinger under a provider, through the wrappers that
// expose Unwrap (the semantic cache), or nil for a noop or an unpingable one.
func providerPinger(p any) Pinger {
	for depth := 0; p != nil && depth < maxUnwrap; depth++ {
		if n, ok := p.(named); ok && n.Name() == providerNoop {
			return nil
		}
		if pinger, ok := p.(Pinger); ok {
			return pinger
		}
		switch u := p.(type) {
		case interface{ Unwrap() semantic.Provider }:
			p = u.Unwrap()
		case interface{ Unwrap() query.Provider }:
			p = u.Unwrap()
		default:
			return nil
		}
	}
	return nil
}

// maxUnwrap bounds the wrapper walk.
const maxUnwrap = 4

// objectLister is the portal blob store's listing, which every S3 backend
// answers cheaply.
type objectLister interface {
	ListDirectory(ctx context.Context, bucket, prefix string) ([]s3adapter.ObjectEntry, bool, error)
}

// objectProbe lists an empty prefix of the bucket: an answer, even an empty
// one, is a reachable store that accepts the platform's credential.
type objectProbe struct {
	objects objectLister
	bucket  string
}

// probePrefix is a prefix no object is written under.
const probePrefix = "_mcp_platform_probe/"

// Ping lists the probe prefix.
func (o objectProbe) Ping(ctx context.Context) error {
	_, _, err := o.objects.ListDirectory(ctx, o.bucket, probePrefix)
	return err //nolint:wrapcheck // the probe reports up or down; the error is never shown
}

// idpProbe fetches the issuer's discovery document.
type idpProbe struct {
	issuer string
	client *http.Client
}

// Ping fetches the discovery document and checks it names signing keys.
func (i idpProbe) Ping(ctx context.Context) error {
	doc, err := oidcdiscovery.Fetch(ctx, i.client, i.issuer)
	if err != nil {
		return err //nolint:wrapcheck // the probe reports up or down; the error is never shown
	}
	if doc.JWKSURI == "" {
		return errNoJWKS
	}
	return nil
}

// errNoJWKS is a discovery document that names no signing keys.
var errNoJWKS = errors.New("discovery document names no jwks_uri")
