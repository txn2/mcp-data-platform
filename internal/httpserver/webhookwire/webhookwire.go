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
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpserver/instanceheader"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcelayer"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister/regstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/compactor"
	"github.com/txn2/mcp-data-platform/internal/webhook/receiver"
	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
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
	address   string
	listener  *http.Server
}

// Build assembles webhooks from the platform. address is the main listener's,
// which names this replica. It returns nil when the deployment cannot hold a
// webhook source.
func Build(p *platform.Platform, address string, exec whtable.Executor) *Webhooks {
	if p == nil {
		return nil
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
func buildFrom(p platformSource, address string, exec whtable.Executor) *Webhooks {
	if p.DB() == nil || p.ResourceStore() == nil || p.ResourceS3Client() == nil || exec == nil {
		return nil
	}
	cfg := p.Config()
	client, err := resourcelayer.OpenS3(resourcelayer.Config{
		S3Connection: cfg.Resources.Managed.S3Connection, Toolkits: cfg.Toolkits,
		MaxObjectBytes: cfg.Resources.Managed.MaxUploadBytes,
	})
	if err != nil || client == nil {
		log.Printf("Webhooks disabled: no client on the managed-resources connection (%v)", err)
		return nil
	}
	uriScheme := cfg.Resources.Managed.URIScheme
	if uriScheme == "" {
		uriScheme = resource.DefaultURIScheme
	}
	writer := resourcewrite.New(resourcewrite.Deps{
		Store: p.ResourceStore(), Blobs: p.ResourceS3Client(), Bucket: cfg.Resources.Managed.S3Bucket,
		URIScheme: uriScheme, MaxVersions: cfg.Resources.Managed.MaxVersions,
		Registered: p.RegisterManagedResource, Unregistered: p.UnregisterManagedResource,
	})
	return assemble(parts{
		db: p, objects: objects{c: client}, bucket: cfg.Resources.Managed.S3Bucket,
		tables: whtable.New(exec, cfg.Resources.Managed.S3Bucket),
		resources: windowResources{
			w: writer, byURI: p.ResourceStore(), uriScheme: uriScheme, adminPersona: cfg.Admin.Persona,
		},
		encryptor: p.RestEncryptor(), metrics: p.Metrics(), cfg: cfg,
		replica: instanceheader.HostName(address), address: address,
		personaExists: personaLookup(p.PersonaRegistry()),
	})
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
	cfg       *platform.Config
	replica   string
	address   string
	// personaExists reports whether a persona is defined; nil accepts any.
	personaExists func(string) bool
}

// metricsSink is the platform's metrics, which report for both the receiver
// and the compactor.
type metricsSink interface {
	receiver.Metrics
	compactor.Metrics
}

// assemble builds the stores, the receiver, the compactor and the service.
func assemble(pt parts) *Webhooks {
	db := pt.db.DB()
	sources := whsource.NewStore(db, pt.encryptor)
	windows := whstore.New(db)
	wcfg := pt.cfg.Webhooks
	w := &Webhooks{address: wcfg.Receiver.Address}
	if wcfg.ReceiverEnabled() {
		w.receiver = receiver.New(receiver.Config{
			Sources: sources, Objects: pt.objects, Bucket: pt.bucket, Recorder: windows,
			RawWindows: rawWindows{tables: pt.tables}, Metrics: pt.metrics, Replica: pt.replica,
			WriteTimeout: wcfg.WriteTimeout(),
		})
	}
	if wcfg.CompactorEnabled() {
		w.compactor = compactor.New(wcfg.Tuning(), compactor.Deps{
			Windows: windows, Sources: sources, Objects: pt.objects, Bucket: pt.bucket,
			Tables: pt.tables, Resources: pt.resources, Metrics: pt.metrics,
		})
	}
	w.Service = whadmin.New(whadmin.Deps{
		Sources: sources, Tables: pt.tables, Registrations: regstore.NewPostgresStore(db),
		Windows: windows, Objects: pt.objects, Resources: pt.resources, Bucket: pt.bucket,
		Changed: w.receiver.Reload, NewID: newRegistrationID, Logger: slog.Default(),
		PersonaExists: pt.personaExists,
	})
	return w
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
	log.Println("Webhook receiver enabled on", receiver.PathPrefix)
}

// Start begins receiving, compacting, and serving the receiver's own
// listener when one is configured.
func (w *Webhooks) Start(ctx context.Context) {
	if w == nil {
		return
	}
	w.receiver.Start(ctx)
	w.compactor.Start(ctx)
	if w.receiver == nil || w.address == "" {
		return
	}
	w.listener = &http.Server{
		Addr:              w.address,
		Handler:           instanceheader.Middleware(instanceheader.HostName(w.address), w.receiver),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		if err := w.listener.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Webhook receiver listener on %s stopped: %v", w.address, err)
		}
	}()
	log.Println("Webhook receiver also listening on", w.address)
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
	w.compactor.Stop()
}
