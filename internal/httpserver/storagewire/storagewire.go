// Package storagewire assembles storage capacity (#2148) from the platform:
// the settings, the receivers' spool states, the client the SeaweedFS cluster
// is read through, the alert engine posting to notification channels, and the
// admin routes.
package storagewire

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/admin/storageapi"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/platform/capacity"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/storagecap"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// EnvInterval overrides how often the stores are sampled and the alerts
// evaluated (default 15m), as a Go duration: a local stack proves an alert
// in seconds rather than a quarter hour.
const EnvInterval = "MCP_PLATFORM_STORAGE_HISTORY_INTERVAL"

// clusterTimeout bounds one read of the SeaweedFS cluster.
const clusterTimeout = 10 * time.Second

// pagePath is the admin storage page an alert links to.
const pagePath = "/portal/admin/storage"

// Build returns the storage service, or nil without a database.
func Build(p *platform.Platform, notify *notifydelivery.Handle) *storagecap.Service {
	if p == nil || p.DB() == nil {
		return nil
	}
	return assemble(p.DB(), AlertSender(notify), p.Config().Portal.PublicBaseURL, bucketEndpoints(p.Config()))
}

// bucketEndpoints is the S3 endpoint of each bucket the platform writes to:
// the portal's and the managed resources'.
func bucketEndpoints(cfg *platform.Config) map[string]string {
	out := map[string]string{}
	add := func(bucket, conn string) {
		if bucket == "" {
			return
		}
		if endpoint, ok := capacity.Endpoint(cfg.Toolkits, conn); ok {
			out[bucket] = endpoint
		}
	}
	add(cfg.Portal.S3Bucket, cfg.Portal.S3Connection)
	add(cfg.Resources.Managed.S3Bucket, cfg.Resources.Managed.S3Connection)
	return out
}

// redetect is how long a store that did not answer as SeaweedFS waits before
// it is asked again: one asked while it was starting answers as nothing.
const redetect = 5 * time.Minute

// onSeaweedFS reports whether a bucket's endpoint is a SeaweedFS store
// (capacity.DetectBackend). A yes is kept; a no is asked again after
// redetect. The question is asked outside the lock, so a slow store does not
// hold back every other report.
func onSeaweedFS(endpoints map[string]string, now func() time.Time) func(ctx context.Context, bucket string) bool {
	var mu sync.Mutex
	seaweed := map[string]bool{}
	askedAt := map[string]time.Time{}
	return func(ctx context.Context, bucket string) bool {
		endpoint, ok := endpoints[bucket]
		if !ok {
			return false
		}
		mu.Lock()
		yes, last := seaweed[endpoint], askedAt[endpoint]
		mu.Unlock()
		if yes || (!last.IsZero() && now().Sub(last) < redetect) {
			return yes
		}
		yes = capacity.DetectBackend(ctx, endpoint) == observability.StorageBackendSeaweedFS
		mu.Lock()
		seaweed[endpoint], askedAt[endpoint] = yes, now()
		mu.Unlock()
		return yes
	}
}

// assemble builds the service over db, posting alerts through sender and
// linking them to the storage page under base.
func assemble(db *sql.DB, sender levelalert.Sender, base string, endpoints map[string]string) *storagecap.Service {
	env, err := capacity.ConfigFromEnv()
	if err != nil {
		slog.Warn("storage: capacity configuration value ignored", "error", logsan.SanitizeForLog(err.Error()))
	}
	budgets := make(map[string]int64, len(env.Budgets))
	for _, b := range env.Budgets {
		budgets[b.Bucket] = b.Bytes
	}
	buckets := make([]string, 0, len(endpoints))
	for bucket := range endpoints {
		buckets = append(buckets, bucket)
	}
	page := ""
	if base = strings.TrimRight(base, "/"); base != "" {
		page = base + pagePath
	}
	return storagecap.New(storagecap.Deps{
		DB: db, Settings: storagecap.NewSettingsStore(db), Spools: whstore.New(db).SpoolStates,
		HTTP:       outbound.NewClient(outbound.Options{Kind: outbound.KindStorage, Timeout: clusterTimeout}),
		EnvBudgets: budgets, Engine: levelalert.New(levelalert.NewPostgresStore(db), sender),
		PageURL: page, Interval: interval(), OnSeaweedFS: onSeaweedFS(endpoints, time.Now), Buckets: buckets,
	})
}

// Mount registers the storage routes behind the admin API's authentication
// when the admin API is on.
func Mount(mux *http.ServeMux, p *platform.Platform, svc *storagecap.Service, adminAuth func() func(http.Handler) http.Handler) {
	if svc == nil || !p.Config().Admin.IsEnabled() {
		return
	}
	storageapi.Register(mux, adminAuth(), svc)
	slog.Info("Storage capacity admin API enabled on /api/v1/admin/storage")
}

// interval reads EnvInterval, or zero (the default) when it is unset or not a
// positive duration.
func interval() time.Duration {
	raw := os.Getenv(EnvInterval)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("ignoring "+EnvInterval+"; it must be a positive duration such as 30s", "value", logsan.SanitizeForLog(raw))
		return 0
	}
	return d
}

// AlertSender is the notification substrate's alert sender as the alert
// engine takes it: nil, never a typed nil, when notifications are off.
func AlertSender(notify *notifydelivery.Handle) levelalert.Sender {
	s := notify.AlertChannels()
	if s == nil {
		return nil
	}
	return s
}
