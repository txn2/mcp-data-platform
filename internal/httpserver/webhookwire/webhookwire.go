// Package webhookwire assembles inbound webhooks for the HTTP composition root
// (#1870): the source store, the receiver mounted at /hooks/, the compactor,
// and the source service the admin API calls.
//
// Everything a source writes lands in the managed-resources bucket, through a
// client on the managed-resources S3 connection, because the scratch catalog
// that reads a source's compacted windows -- which are managed resources -- has
// to read its raw segments as well. A deployment with no database, no managed
// resources store or no Trino toolkit has no webhooks.
package webhookwire

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/admin/webhookapi"
	"github.com/txn2/mcp-data-platform/internal/advisorylock"
	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/httpobs"
	"github.com/txn2/mcp-data-platform/internal/httpserver/catalogwire"
	"github.com/txn2/mcp-data-platform/internal/httpserver/instanceheader"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcelayer"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister/regstore"
	"github.com/txn2/mcp-data-platform/internal/procload"
	"github.com/txn2/mcp-data-platform/internal/webhook/compactor"
	"github.com/txn2/mcp-data-platform/internal/webhook/receiver"
	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whalert"
	"github.com/txn2/mcp-data-platform/internal/webhook/whimap"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whspool"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// readHeaderTimeout bounds how long the receiver's own listener waits for a
// request's headers.
const readHeaderTimeout = 10 * time.Second

// shutdownGrace bounds how long the receiver's own listener waits for
// requests in flight when the platform stops.
const shutdownGrace = 30 * time.Second

// Webhooks is the assembled feature. A nil *Webhooks is a deployment without
// webhooks, and every method on it does nothing.
type Webhooks struct {
	// Service manages sources; the admin API mounts it.
	Service *whadmin.Service

	receiver  *receiver.Receiver
	compactor *compactor.Worker
	// imap polls the sources whose input is a mailbox (#2247); nil where the
	// compactor does not run.
	imap *whimap.Poller
	// spool is the receiver's local spool, closed after the receiver stops;
	// nil when the receiver does not spool.
	spool    *whspool.Spool
	address  string
	listener *http.Server
	// observe is the request observer's config for the receiver's own
	// listener, which serves one handler and no mux (#1889).
	observe httpobs.Config
	// alertDeps is what the alert evaluation reads, kept until
	// EnableAlerts names where it posts; alerts is the evaluation (#2149).
	alertDeps whalert.Deps
	alerts    *whalert.Service
	// files saves a form's files as managed resources (#2333).
	files *formFiles
}

// EnableAlerts builds the webhook alert evaluation (#2149), posting through
// sender and linking to the portal under base. Called before Start; a nil
// Webhooks does nothing.
func (w *Webhooks) EnableAlerts(sender levelalert.Sender, base string) {
	if w == nil {
		return
	}
	d := w.alertDeps
	d.Engine = levelalert.New(levelalert.NewPostgresStore(d.DB), sender)
	d.BaseURL = strings.TrimRight(base, "/")
	d.Interval = alertInterval()
	w.alerts = whalert.New(d)
}

// EnvAlertInterval overrides how often the webhook alerts are evaluated
// (default a minute), as a Go duration.
const EnvAlertInterval = "MCP_PLATFORM_WEBHOOK_ALERT_INTERVAL"

// alertInterval reads EnvAlertInterval, or zero (the default) when it is
// unset or not a positive duration.
func alertInterval() time.Duration {
	raw := os.Getenv(EnvAlertInterval)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("ignoring "+EnvAlertInterval+"; it must be a positive duration such as 30s", "value", logsan.SanitizeForLog(raw))
		return 0
	}
	return d
}

// Build assembles webhooks from the platform. address is the main listener's,
// which names this replica. It returns nil when the deployment cannot hold a
// webhook source, and an error when the receiver's spool is configured and
// cannot be opened: an operator who asked for acknowledged events to be kept
// on disk is told at start, not answered without it.
func Build(p *platform.Platform, address string, exec whtable.Executor) (*Webhooks, error) {
	if p == nil {
		return nil, nil //nolint:nilnil // a nil *Webhooks is a deployment without webhooks, and every method on it does nothing
	}
	return buildFrom(p, address, exec)
}

// platformSource is the part of the platform webhooks are assembled from.
type platformSource interface {
	DB() *sql.DB
	Config() *platform.Config
	ResourceStore() resource.Store
	ResourceS3Client() resource.S3Client
	RegisterManagedResource(res *resource.Resource)
	UnregisterManagedResource(uri string)
	RestEncryptor() *fieldcrypt.RestFieldEncryptor
	Metrics() *observability.Metrics
	PersonaRegistry() *persona.Registry
}

// buildFrom is Build over the part of the platform it reads.
func buildFrom(p platformSource, address string, exec whtable.Executor) (*Webhooks, error) {
	if p.DB() == nil || p.ResourceStore() == nil || p.ResourceS3Client() == nil || exec == nil {
		return nil, nil //nolint:nilnil // as in Build: no webhooks, and no error
	}
	cfg := p.Config()
	client, err := resourcelayer.OpenS3(resourcelayer.Config{
		S3Connection: cfg.Resources.Managed.S3Connection, Toolkits: cfg.Toolkits,
		MaxObjectBytes: cfg.Resources.Managed.MaxUploadBytes,
	})
	if err != nil || client == nil {
		slog.Warn("Webhooks disabled: no client on the managed-resources connection", "error", err)
		return nil, nil //nolint:nilnil // as in Build: no webhooks, and no error
	}
	uriScheme := cfg.Resources.Managed.URIScheme
	if uriScheme == "" {
		uriScheme = resource.DefaultURIScheme
	}
	writer := resourcewrite.New(resourcewrite.Deps{
		Store: p.ResourceStore(), Blobs: p.ResourceS3Client(), Bucket: cfg.Resources.Managed.S3Bucket,
		URIScheme: uriScheme, MaxVersions: cfg.Resources.Managed.MaxVersions,
		Registered: p.RegisterManagedResource, Unregistered: p.UnregisterManagedResource,
		IndexPolicy: cfg.Resources.Index.Resolver(),
	})
	return assemble(parts{
		db: p, objects: objects{c: client}, bucket: cfg.Resources.Managed.S3Bucket,
		tables: whtable.New(exec, cfg.Resources.Managed.S3Bucket),
		resources: windowResources{
			w: writer, byURI: p.ResourceStore(), uriScheme: uriScheme, adminPersona: cfg.Admin.Persona,
			external: externalPersona(p.PersonaRegistry()),
		},
		encryptor: p.RestEncryptor(), metrics: p.Metrics(), observe: p.Metrics(), cfg: cfg,
		replica: instanceheader.HostName(address), address: address,
		personaExists: personaLookup(p.PersonaRegistry()),
		registrations: sourceRegistrations(p),
	})
}

// sourceRegistrations is the registration store a source's table is recorded
// in: one that also catalogs the table (#1918) when the platform can build the
// catalog, the plain store otherwise.
func sourceRegistrations(p platformSource) tableregister.Store {
	inner := regstore.NewPostgresStore(p.DB())
	if cp, ok := p.(catalogwire.Platform); ok {
		return catalogwire.RegistrationStore(cp, inner)
	}
	return inner
}

// dbSource is the part of the platform the stores are built on.
type dbSource interface {
	DB() *sql.DB
}

// parts are what assemble builds from, separated from the platform so the
// assembly can be exercised on its own.
type parts struct {
	db        dbSource
	objects   objects
	bucket    string
	tables    *whtable.Tables
	resources windowResources
	encryptor whsource.Encryptor
	metrics   metricsSink
	// observe is the same metrics as the request observer's typed handle;
	// nil when metrics are off.
	observe *observability.Metrics
	cfg     *platform.Config
	replica string
	address string
	// personaExists reports whether a persona is defined; nil accepts any.
	personaExists func(string) bool
	// registrations records a source's table; nil records it in the plain
	// store.
	registrations tableregister.Store
}

// metricsSink is the platform's metrics, which report for both the receiver
// and the compactor.
type metricsSink interface {
	receiver.Metrics
	compactor.Metrics
}

// assemble builds the stores, the receiver, the compactor and the service.
func assemble(pt parts) (*Webhooks, error) {
	db := pt.db.DB()
	sources := whsource.NewStore(db, pt.encryptor)
	windows := whstore.New(db)
	registerCompactionGauge(bgloop.Metrics(), windows)
	registerStorageGauge(bgloop.Metrics(), windows)
	wcfg := pt.cfg.Webhooks
	w := &Webhooks{address: wcfg.Receiver.Address, observe: httpobs.Config{Route: receiver.PathPrefix, Metrics: pt.observe}, files: &formFiles{r: pt.resources}}
	if wcfg.ReceiverEnabled() {
		rcfg := receiver.Config{
			Sources: sources, Objects: pt.objects, Bucket: pt.bucket, Recorder: windows,
			RawWindows: rawWindows{tables: pt.tables}, Metrics: pt.metrics, Replica: pt.replica,
			WriteTimeout: wcfg.WriteTimeout(),
		}
		if wcfg.SpoolEnabled() {
			sp := wcfg.Receiver.Spool
			spool, err := whspool.Open(whspool.Options{Dir: sp.Dir, MaxBytes: sp.MaxBytes, MaxAge: sp.MaxAge, Replica: pt.replica})
			if err != nil {
				return nil, fmt.Errorf("webhooks: opening the receiver's spool: %w", err)
			}
			w.spool = spool
			rcfg.Spool = spool
			rcfg.SpoolLimits = receiver.SpoolLimits{MaxBytes: orDefault(sp.MaxBytes, whspool.DefaultMaxBytes), MaxAge: orDefault(sp.MaxAge, whspool.DefaultMaxAge)}
			st := spool.Stats()
			slog.Info("Webhook receiver spools to local disk", "dir", sp.Dir, "waiting_events", st.Events, "waiting_files", st.Files)
		}
		w.receiver = receiver.New(rcfg)
	}
	if wcfg.CompactorEnabled() {
		w.compactor = compactor.New(wcfg.Tuning(), compactor.Deps{
			Windows: windows, Sources: sources, Objects: pt.objects, Bucket: pt.bucket,
			Tables: pt.tables, Resources: pt.resources, Metrics: pt.metrics,
		})
		w.imap = whimap.New(whimap.Config{
			Sources: sources, Objects: pt.objects, Bucket: pt.bucket, Recorder: windows,
			RawWindows: rawWindows{tables: pt.tables}, Marks: whimap.NewMarks(db), Files: pt.resources,
			Lock: imapLock(db), Replica: pt.replica, Logger: slog.Default(),
		})
	}
	registrations := pt.registrations
	if registrations == nil {
		registrations = regstore.NewPostgresStore(db)
	}
	grace := wcfg.Tuning().Grace
	if grace <= 0 {
		grace = compactor.DefaultGrace
	}
	w.alertDeps = whalert.Deps{
		DB: db, Settings: whalert.NewSettingsStore(db), Reader: windows, Sources: sources, Grace: grace,
	}
	w.Service = whadmin.New(whadmin.Deps{
		Sources: sources, Tables: pt.tables, Registrations: registrations,
		Windows: windows, Objects: pt.objects, Resources: pt.resources, Bucket: pt.bucket,
		Changed: w.receiver.Reload, NewID: newRegistrationID, Logger: slog.Default(),
		PersonaExists: pt.personaExists, MemoryLimit: procload.MemoryLimit,
		Mailbox: whimap.NewMailbox(whimap.NewMarks(db)),
	})
	return w, nil
}

// orDefault is v, or def when v is not positive.
func orDefault[T int64 | time.Duration](v, def T) T {
	if v > 0 {
		return v
	}
	return def
}

// personaLookup reports whether the platform defines a persona.
func personaLookup(reg *persona.Registry) func(string) bool {
	if reg == nil {
		return nil
	}
	return func(name string) bool {
		_, ok := reg.Get(name)
		return ok
	}
}

// externalPersona reports whether a registered persona is external; nil
// without a registry.
func externalPersona(reg *persona.Registry) func(string) bool {
	if reg == nil {
		return nil
	}
	return func(name string) bool {
		p, ok := reg.Get(name)
		return ok && p.External
	}
}

// rawWindows registers a new window's raw partition on the source's
// connection.
type rawWindows struct {
	tables *whtable.Tables
}

// EnsureRawWindow registers one new window's raw partition on the source's
// connection.
func (r rawWindows) EnsureRawWindow(ctx context.Context, src whsource.Source, start time.Time) error {
	tg, err := r.tables.TargetFor(src.Connection)
	if err != nil {
		return err //nolint:wrapcheck // the connection refusal says what is wrong
	}
	return r.tables.RegisterRawWindow(ctx, tg, src, start) //nolint:wrapcheck // names the window and source
}

// newRegistrationID mints the id of a source's table registration.
func newRegistrationID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating a registration id: %w", err)
	}
	return "reg_" + hex.EncodeToString(b), nil
}

// Mount serves /hooks/ on mux when this replica receives.
func (w *Webhooks) Mount(mux *http.ServeMux) {
	if w == nil || w.receiver == nil {
		return
	}
	mux.Handle(receiver.PathPrefix, w.receiver)
	slog.Info("Webhook receiver enabled", "path", receiver.PathPrefix)
}

// Start begins receiving, compacting, and serving the receiver's own
// listener when one is configured.
func (w *Webhooks) Start(ctx context.Context) {
	if w == nil {
		return
	}
	w.receiver.Start(ctx)
	w.compactor.Start(ctx)
	w.imap.Start(ctx)
	w.alerts.Start(ctx)
	if w.receiver == nil || w.address == "" {
		return
	}
	w.listener = &http.Server{
		Addr: w.address,
		Handler: instanceheader.Middleware(instanceheader.HostName(w.address),
			httpobs.Middleware(w.observe)(w.receiver)),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		if err := w.listener.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(ctx, "Webhook receiver listener stopped", "addr", w.address, "error", err)
		}
	}()
	slog.InfoContext(ctx, "Webhook receiver also listening", "addr", w.address)
}

// Stop ends the listener, answers every request waiting on a segment, and
// stops the compactor.
func (w *Webhooks) Stop() {
	if w == nil {
		return
	}
	if w.listener != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		_ = w.listener.Shutdown(ctx)
		cancel()
	}
	w.receiver.Stop()
	if w.spool != nil {
		if err := w.spool.Close(); err != nil {
			slog.Warn("webhooks: closing the receiver's spool", "error", err)
		}
	}
	w.compactor.Stop()
	w.imap.Stop()
	w.alerts.Stop()
}

// imapLock holds an imap source's poll to one replica at a time.
func imapLock(db *sql.DB) whimap.Lock {
	return func(ctx context.Context, source string) (func(), bool, error) {
		//nolint:wrapcheck // the lock package names itself
		return advisorylock.Try(ctx, db, advisorylock.Pair(whimap.LockKeyspace, whimap.LockKey(source)))
	}
}

// MountAdmin registers the webhook source routes behind the admin API's
// authentication, when there are webhooks and the admin API is enabled.
func (w *Webhooks) MountAdmin(
	mux *http.ServeMux, p *platform.Platform,
	auth func(*platform.Platform) func(http.Handler) http.Handler, author func(*http.Request) string,
) {
	if w == nil || p == nil || !p.Config().Admin.IsEnabled() {
		return
	}
	webhookapi.Register(mux, auth(p), webhookapi.Config{Service: w.Service, Author: author})
	slog.Info("Webhook source admin API enabled on /api/v1/admin/webhooks/sources and /api/v1/admin/webhooks/status")
}
