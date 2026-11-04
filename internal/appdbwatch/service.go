// Package appdbwatch samples every application database on an interval
// (#2406, #2409): its health reduced to numbers, and its schema against what
// the applied migrations build. One replica samples at a time under an
// advisory lock; the samples are kept for the connection page's history and
// the gauges, and the leveled alerts are judged on them.
package appdbwatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/appdbdrift"
	"github.com/txn2/mcp-data-platform/internal/appdbhealth"
	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// AlertFamily is the levelalert family the application database alerts are
// recorded under.
const AlertFamily = "appdb"

// LockKey is the advisory lock one replica samples under. Storage holds
// 4713210402 and the webhook alerts 4713210403.
const LockKey int64 = 4713210404

// DefaultTick is how often each replica asks whether a pass is due; the pass
// itself runs once per the settings' interval.
const DefaultTick = 10 * time.Second

// recentFraction is the share of the interval a sample counts as recent for,
// so a timer firing a little early does not skip its turn.
const recentFraction = 0.9

// errReadHistory wraps a failed read of the stored samples.
const errReadHistory = "reading application database health history: %w"

// day is a day.
const day = 24 * time.Hour

// Kit is the postgres toolkit as the loop reads it.
type Kit interface {
	appdbhealth.Querier
	appdbdrift.Kit
	ListConnections() []toolkit.ConnectionDetail
}

// Deps is what the service reads and sends through.
type Deps struct {
	DB       *sql.DB
	Settings SettingsStore
	// Kit is the live postgres toolkit, nil when none is registered.
	Kit func() Kit
	// Migrations lists the platform's migrations, which name the schemas a
	// connection's drift is checked in.
	Migrations appdbdrift.Migrations
	// Engine routes the alerts; nil records nothing and sends nothing.
	Engine *levelalert.Engine
	// PageURL is the connection page an alert links to; nil omits it.
	PageURL func(connection string) string
	Tick    time.Duration
	Now     func() time.Time
}

// Sample is one connection's reading as the history keeps it.
type Sample struct {
	appdbhealth.Summary
	// Reachable is false when the database did not answer the server read.
	Reachable bool `json:"reachable"`
	// DriftObjects is how many objects differ from what the applied
	// migrations build, across the schemas that could be checked, and
	// DriftUnchecked how many schemas the platform keeps migrations for could
	// not be (their Drift result says why).
	DriftObjects   int `json:"drift_objects"`
	DriftUnchecked int `json:"drift_unchecked"`
	// Drift is the check of each schema the platform keeps migrations for.
	Drift []appdbdrift.Result `json:"drift"`
}

// Point is one stored sample.
type Point struct {
	Connection string    `json:"connection"`
	SampledAt  time.Time `json:"sampled_at"`
	Sample     Sample    `json:"sample"`
}

// Service samples the databases, keeps their history and evaluates the
// alerts.
type Service struct {
	d    Deps
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// New returns the service, or nil without a database.
func New(d Deps) *Service {
	if d.DB == nil || d.Settings == nil || d.Kit == nil {
		return nil
	}
	if d.Tick <= 0 {
		d.Tick = DefaultTick
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
			Name: bgloop.NameAppDBHealth, Every: s.d.Tick, Immediate: true, Stop: s.stop,
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

// Settings reads the stored settings.
func (s *Service) Settings(ctx context.Context) (View, error) { return Read(ctx, s.d.Settings) }

// SaveSettings validates and writes the settings.
func (s *Service) SaveSettings(ctx context.Context, v Settings, author string) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if err := s.d.Settings.Set(ctx, v.normalized(), author); err != nil {
		return fmt.Errorf("saving application database health settings: %w", err)
	}
	return nil
}

// Pass is one iteration: sample every application database, keep the
// samples, drop the history past the settings' period, and evaluate the
// alerts. A pass within most of the interval of the last one is skipped:
// another replica, or this one, took it.
func (s *Service) Pass(ctx context.Context) error {
	view, err := Read(ctx, s.d.Settings)
	if err != nil {
		return err
	}
	settings := view.Settings
	recent, err := s.sampledWithin(ctx, time.Duration(float64(settings.Interval())*recentFraction))
	if err != nil {
		return err
	}
	if recent {
		return bgloop.ErrSkipped
	}
	kit := s.d.Kit()
	if kit == nil {
		return bgloop.ErrSkipped
	}
	now := s.d.Now().UTC()
	conns := kit.ListConnections()
	samples := s.sampleAll(ctx, kit, conns, settings)
	findings, unjudged := []levelalert.Finding{}, []string{}
	for i, c := range conns {
		sample := samples[i]
		if err := s.record(ctx, c.Name, now, sample); err != nil {
			return err
		}
		if !sample.Reachable {
			unjudged = append(unjudged, keyPrefix(c.Name))
			continue
		}
		findings = append(findings, Findings(c.Name, sample, settings, s.pageURL(c.Name))...)
	}
	if _, err := s.d.DB.ExecContext(ctx, `DELETE FROM appdb_health_samples WHERE sampled_at < $1`,
		now.Add(-time.Duration(settings.HistoryDays)*day)); err != nil {
		return fmt.Errorf("pruning application database health history: %w", err)
	}
	if _, err := s.d.Engine.Apply(ctx, AlertFamily, settings.Channels, findings, unjudged...); err != nil {
		return fmt.Errorf("evaluating application database alerts: %w", err)
	}
	return nil
}

// sampleParallel is how many connections a pass samples at once: each is a
// different database, and one slow to answer (or holding many schemas to
// build) does not hold back the others' samples and alerts.
const sampleParallel = 4

// sampleAll samples every connection, at most sampleParallel at a time, in
// the order conns names them.
func (s *Service) sampleAll(ctx context.Context, kit Kit, conns []toolkit.ConnectionDetail, settings Settings) []Sample {
	out := make([]Sample, len(conns))
	var g errgroup.Group
	g.SetLimit(sampleParallel)
	for i, c := range conns {
		g.Go(func() error {
			out[i] = s.sample(ctx, kit, c.Name, settings)
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// sample reads one connection's health and drift.
func (s *Service) sample(ctx context.Context, kit Kit, connection string, settings Settings) Sample {
	report := appdbhealth.Read(ctx, kit, connection, appdbhealth.Options{
		LongRunning: time.Duration(settings.LongRunningSeconds) * time.Second,
	})
	out := Sample{Summary: appdbhealth.Summarize(report), Reachable: true, Drift: []appdbdrift.Result{}}
	for _, e := range report.Errors {
		if e.Section == appdbhealth.SectionServer {
			out.Reachable = false
			return out
		}
	}
	out.Drift = DriftOf(ctx, kit, s.d.Migrations, connection)
	for _, d := range out.Drift {
		if !d.Checked {
			out.DriftUnchecked++
			continue
		}
		out.DriftObjects += len(d.Differences)
	}
	return out
}

// DriftOf checks every schema the platform keeps migrations for on
// connection.
func DriftOf(ctx context.Context, kit appdbdrift.Kit, store appdbdrift.Migrations, connection string) []appdbdrift.Result {
	out := []appdbdrift.Result{}
	if store == nil {
		return out
	}
	all, err := store.List(ctx, connection, "")
	if err != nil {
		return append(out, appdbdrift.Result{
			Connection: connection, CheckedAt: time.Now().UTC(), Reason: "reading the migrations: " + err.Error(),
			Differences: []appdbdrift.Difference{},
		})
	}
	schemas := []string{}
	for _, m := range all {
		if !slices.Contains(schemas, m.Schema) {
			schemas = append(schemas, m.Schema)
		}
	}
	for _, schema := range schemas {
		out = append(out, appdbdrift.Check(ctx, kit, store, connection, schema))
	}
	return out
}

// record keeps one sample.
func (s *Service) record(ctx context.Context, connection string, at time.Time, sample Sample) error {
	raw, err := json.Marshal(sample)
	if err != nil {
		return fmt.Errorf("encoding an application database health sample: %w", err)
	}
	if _, err := s.d.DB.ExecContext(ctx, `INSERT INTO appdb_health_samples (connection, sampled_at, summary)
		VALUES ($1, $2, $3) ON CONFLICT (connection, sampled_at) DO UPDATE SET summary = EXCLUDED.summary`,
		connection, at, raw); err != nil {
		return fmt.Errorf("recording an application database health sample: %w", err)
	}
	return nil
}

// sampledWithin reports whether any sample was taken within d.
func (s *Service) sampledWithin(ctx context.Context, d time.Duration) (bool, error) {
	var recent bool
	if err := s.d.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM appdb_health_samples WHERE sampled_at > $1)`,
		s.d.Now().UTC().Add(-d)).Scan(&recent); err != nil {
		return false, fmt.Errorf(errReadHistory, err)
	}
	return recent, nil
}

// History is connection's samples over the last days, oldest first.
func (s *Service) History(ctx context.Context, connection string, days int) ([]Point, error) {
	rows, err := s.d.DB.QueryContext(ctx, `SELECT connection, sampled_at, summary FROM appdb_health_samples
		WHERE connection = $1 AND sampled_at > $2 ORDER BY sampled_at`,
		connection, s.d.Now().UTC().Add(-time.Duration(days)*day))
	if err != nil {
		return nil, fmt.Errorf(errReadHistory, err)
	}
	return scanPoints(rows)
}

// Latest is each connection's newest sample taken within the window.
func (s *Service) Latest(ctx context.Context, window time.Duration) ([]Point, error) {
	rows, err := s.d.DB.QueryContext(ctx, `SELECT DISTINCT ON (connection) connection, sampled_at, summary
		FROM appdb_health_samples WHERE sampled_at > $1 ORDER BY connection, sampled_at DESC`, s.d.Now().UTC().Add(-window))
	if err != nil {
		return nil, fmt.Errorf(errReadHistory, err)
	}
	return scanPoints(rows)
}

func scanPoints(rows *sql.Rows) ([]Point, error) {
	defer func() { _ = rows.Close() }()
	out := []Point{}
	for rows.Next() {
		var p Point
		var raw []byte
		if err := rows.Scan(&p.Connection, &p.SampledAt, &raw); err != nil {
			return nil, fmt.Errorf(errReadHistory, err)
		}
		if err := json.Unmarshal(raw, &p.Sample); err != nil {
			return nil, fmt.Errorf("decoding an application database health sample: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errReadHistory, err)
	}
	return out, nil
}

// Gauges is what the health gauges report: each connection's newest sample
// within three intervals. A read that fails reports nothing.
func (s *Service) Gauges(ctx context.Context) []observability.AppDBHealthSample {
	view, err := Read(ctx, s.d.Settings)
	if err != nil {
		return nil
	}
	points, err := s.Latest(ctx, 3*view.Interval())
	if err != nil {
		return nil
	}
	out := make([]observability.AppDBHealthSample, 0, len(points))
	for _, p := range points {
		x := p.Sample
		out = append(out, observability.AppDBHealthSample{
			Connection: p.Connection, SizeBytes: x.SizeBytes, TransactionIDAge: x.TransactionIDAge,
			Connections: x.Connections, MaxConnections: x.MaxConnections, IdleInTransaction: int64(x.IdleInTransaction),
			LongRunning: int64(x.LongRunning), Blocked: int64(x.Blocked), InvalidIndexes: int64(x.InvalidIndexes),
			UnusedIndexes: int64(x.UnusedIndexes), DriftObjects: int64(x.DriftObjects),
			DeadTupleRatio: x.MaxDeadRatio, CacheHitRatio: x.CacheHitRatio,
		})
	}
	return out
}

func (s *Service) pageURL(connection string) string {
	if s.d.PageURL == nil {
		return ""
	}
	return s.d.PageURL(connection)
}
