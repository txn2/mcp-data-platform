package storagecap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// The kinds of store a report and its alerts cover.
const (
	KindDatabase  = "database"
	KindSeaweedFS = "seaweedfs"
	KindBucket    = "bucket"
	KindSpool     = "spool"
)

// AlertFamily is the levelalert family the storage alerts are recorded under.
const AlertFamily = "storage"

// LockKey is the advisory lock one replica samples and alerts under. The
// capacity scan holds 4713210401.
const LockKey int64 = 4713210402

// DefaultInterval is how often a store is sampled and its alerts evaluated.
const DefaultInterval = 15 * time.Minute

// clusterTimeout bounds one read of the SeaweedFS cluster.
const clusterTimeout = 10 * time.Second

// spoolStale is how long since a receiver's last report its spool is still
// read as current, as the webhooks page reads it.
const spoolStale = 2 * time.Minute

// percent scales a fraction to the percent a store's use is shown in.
const percent = 100

// errReadingHistory wraps a failed read of the stored samples.
const errReadingHistory = "reading storage history: %w"

// Deps is what the service reads and sends through.
type Deps struct {
	DB       *sql.DB
	Settings SettingsStore
	// Spools reads every receiver's spool state; nil without webhooks.
	Spools func(ctx context.Context) ([]whstore.SpoolState, error)
	// HTTP reads the SeaweedFS cluster.
	HTTP *http.Client
	// EnvBudgets are the budgets MCP_PLATFORM_STORAGE_BUDGETS names, which
	// a budget in the settings overrides bucket by bucket.
	EnvBudgets map[string]int64
	// OnSeaweedFS reports whether a bucket is on the SeaweedFS cluster: such
	// a bucket with no budget can grow into the cluster's free space, which
	// is then its free space. Nil names none.
	OnSeaweedFS func(ctx context.Context, bucket string) bool
	// Engine routes the alerts; nil records nothing and sends nothing.
	Engine *levelalert.Engine
	// PageURL is the admin storage page an alert links to; empty omits it.
	PageURL  string
	Interval time.Duration
	Now      func() time.Time
}

// Service reads the stores, keeps their history and evaluates the alerts.
type Service struct {
	d    Deps
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// New returns the service, or nil without a database.
func New(d Deps) *Service {
	if d.DB == nil || d.Settings == nil {
		return nil
	}
	if d.HTTP == nil {
		d.HTTP = outbound.NewClient(outbound.Options{Kind: outbound.KindStorage, Timeout: clusterTimeout})
	}
	if d.Interval <= 0 {
		d.Interval = DefaultInterval
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, stop: make(chan struct{}), done: make(chan struct{})}
}

// Start runs the sampling loop. Nil-safe.
func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		defer close(s.done)
		bgloop.Run(ctx, bgloop.Loop{
			Name: bgloop.NameStorageHistory, Every: s.d.Interval, Immediate: true, Stop: s.stop,
			Body: func(ctx context.Context) error { return levelalert.Locked(ctx, s.d.DB, LockKey, s.Pass) },
		})
	}()
}

// Stop ends the loop and waits for it. Nil-safe; a second call is a no-op.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

// Pass is one iteration: read every store, record a sample of each, drop the
// history past the settings' period, and evaluate the alerts.
func (s *Service) Pass(ctx context.Context) error {
	settings, err := Read(ctx, s.d.Settings)
	if err != nil {
		return err
	}
	report, err := s.buildReport(ctx, settings.Settings)
	if err != nil {
		return err
	}
	if err := s.record(ctx, report); err != nil {
		return err
	}
	if _, err := s.d.DB.ExecContext(ctx, `DELETE FROM storage_samples WHERE sampled_at < $1`,
		report.GeneratedAt.Add(-time.Duration(settings.HistoryDays)*day)); err != nil {
		return fmt.Errorf("pruning storage history: %w", err)
	}
	if _, err := s.d.Engine.Apply(ctx, AlertFamily, settings.Channels, Findings(report, settings.Settings, s.d.PageURL),
		Unjudged(report)...); err != nil {
		return fmt.Errorf("evaluating storage alerts: %w", err)
	}
	return nil
}

// spoolGone is how long a replica's spool goes unreported before its volume
// is taken to be gone rather than unread: a replica replaced in a rollout
// does not keep its alerts open.
const spoolGone = 15 * time.Minute

// Unjudged is the alert key prefixes of the stores this report could not
// read: the SeaweedFS cluster and every bucket whose capacity it gives when
// the cluster read failed, and each spool volume whose replica missed its
// recent reports without being gone. Their conditions are carried as they
// stand rather than resolved.
func Unjudged(r Report) []string {
	out := []string{}
	if r.SeaweedFS != nil && r.SeaweedFS.Error != "" {
		out = append(out, "storage:"+KindSeaweedFS+":")
		for _, st := range r.Stores {
			if st.Kind == KindBucket && st.CapacityFrom != "budget" {
				out = append(out, "storage:"+st.Store+":")
			}
		}
	}
	for _, sp := range r.Spools {
		if !sp.Reporting && r.GeneratedAt.Sub(sp.UpdatedAt) < spoolGone {
			out = append(out, "storage:"+KindSpool+":"+sp.Replica+":")
		}
	}
	return out
}

// record writes one sample per store read.
func (s *Service) record(ctx context.Context, r Report) error {
	for _, st := range r.Stores {
		if _, err := s.d.DB.ExecContext(ctx,
			`INSERT INTO storage_samples (store, sampled_at, used_bytes, total_bytes) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (store, sampled_at) DO UPDATE SET used_bytes = EXCLUDED.used_bytes, total_bytes = EXCLUDED.total_bytes`,
			st.Store, r.GeneratedAt, st.UsedBytes, st.TotalBytes); err != nil {
			return fmt.Errorf("recording a storage sample: %w", err)
		}
	}
	return nil
}

// Report reads every store now, with each one's growth and projection from
// its history.
func (s *Service) Report(ctx context.Context) (Report, error) {
	settings, err := Read(ctx, s.d.Settings)
	if err != nil {
		return Report{}, err
	}
	return s.buildReport(ctx, settings.Settings)
}

// History is a store's samples over the last days, oldest first.
func (s *Service) History(ctx context.Context, store string, days int) ([]Point, error) {
	since := s.d.Now().UTC().Add(-time.Duration(days) * day)
	rows, err := s.d.DB.QueryContext(ctx,
		`SELECT sampled_at, used_bytes, total_bytes FROM storage_samples
		  WHERE store = $1 AND sampled_at >= $2 ORDER BY sampled_at`, store, since)
	if err != nil {
		return nil, fmt.Errorf(errReadingHistory, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Point, 0)
	for rows.Next() {
		var p Point
		var total sql.NullInt64
		if err := rows.Scan(&p.At, &p.Used, &total); err != nil {
			return nil, fmt.Errorf("scanning storage history: %w", err)
		}
		p.At = p.At.UTC()
		if total.Valid {
			p.Total = &total.Int64
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errReadingHistory, err)
	}
	return out, nil
}

// buildReport reads the stores under settings.
func (s *Service) buildReport(ctx context.Context, settings Settings) (Report, error) {
	now := s.d.Now().UTC()
	r := Report{
		GeneratedAt: now, HistoryDays: settings.HistoryDays,
		Stores: []StoreView{}, Buckets: []BucketView{}, Reconciles: []ReconcileView{}, Spools: []SpoolView{},
	}
	var err error
	if r.Database, err = readDatabase(ctx, s.d.DB, settings.DatabaseLimitBytes); err != nil {
		return r, err
	}
	r.Stores = append(r.Stores, StoreView{
		Store: KindDatabase, Kind: KindDatabase, Name: "Database", UsedBytes: r.Database.Bytes, TotalBytes: positive(settings.DatabaseLimitBytes),
	})
	objects, err := readObjects(ctx, s.d.DB)
	if err != nil {
		return r, err
	}
	r.Buckets, r.Reconciles, r.LastScan = objects.buckets, objects.reconciles, objects.lastScan
	buckets := bucketStores(r.Buckets, s.budgets(settings))
	if settings.SeaweedFS.MasterURL != "" {
		r.SeaweedFS = s.readCluster(ctx, settings.SeaweedFS)
		if r.SeaweedFS.Error == "" {
			c := r.SeaweedFS.Capacity
			r.Stores = append(r.Stores, StoreView{
				Store: KindSeaweedFS, Kind: KindSeaweedFS, Name: "SeaweedFS cluster", UsedBytes: c.Total - c.Free, TotalBytes: positive(c.Total),
			})
			s.onCluster(ctx, buckets, c.Free)
		}
	}
	r.Stores = append(r.Stores, buckets...)
	spools, spoolStores, err := s.readSpools(ctx, now)
	if err != nil {
		return r, err
	}
	r.Spools = spools
	r.Stores = append(r.Stores, spoolStores...)
	if err := s.project(ctx, &r); err != nil {
		return r, err
	}
	// The findings are discarded here; computing them marks each store's
	// level under the stored thresholds, which the page shows.
	_ = Findings(r, settings, s.d.PageURL)
	return r, nil
}

// budgets is each bucket's budget: the environment's, overridden by the
// settings'.
func (s *Service) budgets(settings Settings) map[string]int64 {
	out := make(map[string]int64, len(settings.Budgets))
	maps.Copy(out, s.d.EnvBudgets)
	maps.Copy(out, settings.Budgets)
	return out
}

// onCluster gives each bucket on the SeaweedFS cluster that has no budget the
// cluster's free space: what it holds, and what it can still grow into.
func (s *Service) onCluster(ctx context.Context, buckets []StoreView, free int64) {
	if s.d.OnSeaweedFS == nil {
		return
	}
	for i := range buckets {
		b := &buckets[i]
		if b.TotalBytes != nil || !s.d.OnSeaweedFS(ctx, strings.TrimPrefix(b.Store, KindBucket+":")) {
			continue
		}
		b.TotalBytes = positive(b.UsedBytes + free)
		b.CapacityFrom = KindSeaweedFS
	}
}

// readCluster reads the SeaweedFS cluster, reporting a failure on the view
// rather than failing the report: the rest of the page is still true.
func (s *Service) readCluster(ctx context.Context, cfg SeaweedFS) *ClusterView {
	c, err := ReadSeaweedFS(ctx, s.d.HTTP, cfg)
	v := &ClusterView{MasterURL: cfg.MasterURL, Capacity: c}
	if err != nil {
		v.Error = err.Error()
		slog.WarnContext(ctx, "storage: reading the SeaweedFS cluster", "error", logsan.SanitizeForLog(err.Error()))
	}
	return v
}

// readSpools is each receiver's spool and, for each one reporting its volume,
// a store.
func (s *Service) readSpools(ctx context.Context, now time.Time) ([]SpoolView, []StoreView, error) {
	views, stores := []SpoolView{}, []StoreView{}
	if s.d.Spools == nil {
		return views, stores, nil
	}
	states, err := s.d.Spools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook spool states: %w", err)
	}
	for _, st := range states {
		reporting := now.Sub(st.UpdatedAt) <= spoolStale
		views = append(views, SpoolView{
			Replica: st.Replica, Dir: st.Dir, Bytes: st.Bytes, MaxBytes: st.MaxBytes,
			FsTotal: st.FsTotal, FsFree: st.FsFree, Reporting: reporting, UpdatedAt: st.UpdatedAt.UTC(),
		})
		if reporting && st.FsTotal != nil && st.FsFree != nil {
			stores = append(stores, StoreView{
				Store: KindSpool + ":" + st.Replica, Kind: KindSpool, Name: "Webhook spool on " + st.Replica,
				UsedBytes: *st.FsTotal - *st.FsFree, TotalBytes: positive(*st.FsTotal),
			})
		}
	}
	return views, stores, nil
}

// project adds each store's percent used, growth and projection.
func (s *Service) project(ctx context.Context, r *Report) error {
	history, err := s.historySince(ctx, r.GeneratedAt.Add(-longWindow))
	if err != nil {
		return err
	}
	for i := range r.Stores {
		st := &r.Stores[i]
		if st.TotalBytes != nil {
			free := max(*st.TotalBytes-st.UsedBytes, 0)
			pct := percent * float64(st.UsedBytes) / float64(*st.TotalBytes)
			st.FreeBytes, st.PercentUsed = &free, &pct
		}
		points := slices.Concat(history[st.Store], []Point{{At: r.GeneratedAt, Used: st.UsedBytes, Total: st.TotalBytes}})
		st.Forecast = Project(points, r.GeneratedAt)
	}
	return nil
}

// historySince is every store's samples since since, oldest first.
func (s *Service) historySince(ctx context.Context, since time.Time) (map[string][]Point, error) {
	rows, err := s.d.DB.QueryContext(ctx,
		`SELECT store, sampled_at, used_bytes, total_bytes FROM storage_samples
		  WHERE sampled_at >= $1 ORDER BY store, sampled_at`, since)
	if err != nil {
		return nil, fmt.Errorf(errReadingHistory, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]Point{}
	for rows.Next() {
		var (
			store string
			p     Point
			total sql.NullInt64
		)
		if err := rows.Scan(&store, &p.At, &p.Used, &total); err != nil {
			return nil, fmt.Errorf("scanning storage history: %w", err)
		}
		p.At = p.At.UTC()
		if total.Valid {
			p.Total = &total.Int64
		}
		out[store] = append(out[store], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errReadingHistory, err)
	}
	return out, nil
}

// readDatabase is the database's size, its largest tables and indexes.
func readDatabase(ctx context.Context, db *sql.DB, limit int64) (DatabaseView, error) {
	v := DatabaseView{LimitBytes: positive(limit), Tables: []TableView{}}
	if err := db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&v.Bytes); err != nil {
		return v, fmt.Errorf("reading the database size: %w", err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT c.relname, c.relkind = 'i', pg_total_relation_size(c.oid), GREATEST(c.reltuples, 0)::bigint
		   FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p', 'i') AND NOT c.relispartition
		  ORDER BY pg_total_relation_size(c.oid) DESC LIMIT 25`)
	if err != nil {
		return v, fmt.Errorf("reading table sizes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var t TableView
		if err := rows.Scan(&t.Name, &t.Index, &t.Bytes, &t.Rows); err != nil {
			return v, fmt.Errorf("scanning a table size: %w", err)
		}
		v.Tables = append(v.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return v, fmt.Errorf("reading table sizes: %w", err)
	}
	return v, nil
}

// objectReadings is what the capacity scan (#1899) recorded: the bucket
// usage, the last reconcile, and the last full listing, nil before the first.
type objectReadings struct {
	buckets    []BucketView
	reconciles []ReconcileView
	lastScan   *ScanView
}

// readObjects is the bucket usage, the last reconcile and the last full
// listing, as the capacity scan (#1899) recorded them.
func readObjects(ctx context.Context, db *sql.DB) (objectReadings, error) {
	buckets, err := readBuckets(ctx, db)
	if err != nil {
		return objectReadings{}, err
	}
	reconciles, err := readReconciles(ctx, db)
	if err != nil {
		return objectReadings{}, err
	}
	var scan ScanView
	err = db.QueryRowContext(ctx, `SELECT duration_seconds, objects, finished_at FROM storage_scans WHERE id = 1`).
		Scan(&scan.DurationSeconds, &scan.Objects, &scan.FinishedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return objectReadings{buckets: buckets, reconciles: reconciles}, nil
	case err != nil:
		return objectReadings{}, fmt.Errorf("reading the last storage listing: %w", err)
	}
	scan.FinishedAt = scan.FinishedAt.UTC()
	return objectReadings{buckets: buckets, reconciles: reconciles, lastScan: &scan}, nil
}

func readBuckets(ctx context.Context, db *sql.DB) ([]BucketView, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT bucket, purpose, bytes, objects, listed_at FROM storage_usage ORDER BY bucket, purpose`)
	if err != nil {
		return nil, fmt.Errorf("reading bucket usage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []BucketView{}
	for rows.Next() {
		var b BucketView
		var listed sql.NullTime
		if err := rows.Scan(&b.Bucket, &b.Purpose, &b.Bytes, &b.Objects, &listed); err != nil {
			return nil, fmt.Errorf("scanning bucket usage: %w", err)
		}
		if listed.Valid {
			at := listed.Time.UTC()
			b.ListedAt = &at
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading bucket usage: %w", err)
	}
	return out, nil
}

func readReconciles(ctx context.Context, db *sql.DB) ([]ReconcileView, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT purpose, orphaned, dangling, reconciled_at FROM storage_reconciles ORDER BY purpose`)
	if err != nil {
		return nil, fmt.Errorf("reading the storage reconcile: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ReconcileView{}
	for rows.Next() {
		var c ReconcileView
		if err := rows.Scan(&c.Purpose, &c.Orphaned, &c.Dangling, &c.ReconciledAt); err != nil {
			return nil, fmt.Errorf("scanning the storage reconcile: %w", err)
		}
		c.ReconciledAt = c.ReconciledAt.UTC()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the storage reconcile: %w", err)
	}
	return out, nil
}

// bucketStores is one store per bucket the usage rows name, its usage the sum
// of its purposes, against its budget when it has one.
func bucketStores(rows []BucketView, budgets map[string]int64) []StoreView {
	used := map[string]int64{}
	for _, b := range rows {
		used[b.Bucket] += b.Bytes
	}
	for b := range budgets {
		if _, ok := used[b]; !ok {
			used[b] = 0
		}
	}
	names := make([]string, 0, len(used))
	for b := range used {
		names = append(names, b)
	}
	sort.Strings(names)
	out := make([]StoreView, 0, len(names))
	for _, b := range names {
		v := StoreView{
			Store: KindBucket + ":" + b, Kind: KindBucket, Name: "Bucket " + b, UsedBytes: used[b], TotalBytes: positive(budgets[b]),
		}
		if v.TotalBytes != nil {
			v.CapacityFrom = "budget"
		}
		out = append(out, v)
	}
	return out
}

// positive is v as a known capacity, nil when it is not one.
func positive(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}
