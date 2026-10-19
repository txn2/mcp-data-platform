package whalert

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// AlertFamily is the levelalert family the webhook alerts are recorded under.
const AlertFamily = "webhook"

// LockKey is the advisory lock one replica evaluates the alerts under. The
// storage alerts hold 4713210402.
const LockKey int64 = 4713210403

// DefaultInterval is how often the conditions are evaluated: a sender does
// not wait, so this is minutes, not the storage alerts' quarter hour.
const DefaultInterval = time.Minute

// Reader is what an evaluation reads. *whstore.Store satisfies it.
type Reader interface {
	SpoolStates(ctx context.Context) ([]whstore.SpoolState, error)
	AlertReadings(ctx context.Context, countsSince, ackSince time.Time) (map[string]*whstore.SourceReadings, error)
}

// SourceLister lists the sources. *whsource.Store satisfies it.
type SourceLister interface {
	List(ctx context.Context) ([]whsource.Source, error)
}

// Deps is what the service reads and sends through.
type Deps struct {
	DB       *sql.DB
	Settings SettingsStore
	Reader   Reader
	Sources  SourceLister
	Engine   *levelalert.Engine
	// Grace is the compactor's grace after a window ends.
	Grace    time.Duration
	BaseURL  string
	Interval time.Duration
	Now      func() time.Time
}

// Service evaluates the alerts on one replica at a time.
type Service struct {
	d    Deps
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// New returns the service, or nil without a database.
func New(d Deps) *Service {
	if d.DB == nil || d.Settings == nil || d.Reader == nil || d.Sources == nil {
		return nil
	}
	if d.Interval <= 0 {
		d.Interval = DefaultInterval
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, stop: make(chan struct{}), done: make(chan struct{})}
}

// Start runs the evaluation loop. Nil-safe.
func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		defer close(s.done)
		bgloop.Run(ctx, bgloop.Loop{
			Name: bgloop.NameWebhookAlert, Every: s.d.Interval, Stop: s.stop,
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

// Pass is one evaluation: read, judge, and route what it found.
func (s *Service) Pass(ctx context.Context) error {
	view, err := Read(ctx, s.d.Settings)
	if err != nil {
		return err
	}
	r, err := s.readings(ctx, view.Settings)
	if err != nil {
		return err
	}
	if _, err := s.d.Engine.Apply(ctx, AlertFamily, view.Channels, Evaluate(view.Settings, r)); err != nil {
		return fmt.Errorf("routing webhook alerts: %w", err)
	}
	return nil
}

// readings reads what the settings' conditions judge.
func (s *Service) readings(ctx context.Context, settings Settings) (Readings, error) {
	now := s.d.Now().UTC()
	r := Readings{Now: now, Grace: s.d.Grace, BaseURL: s.d.BaseURL, Sources: map[string]bool{}}
	sources, err := s.d.Sources.List(ctx)
	if err != nil {
		return r, fmt.Errorf("listing webhook sources: %w", err)
	}
	for _, src := range sources {
		r.Sources[src.Name] = src.Enabled
	}
	if r.Spools, err = s.d.Reader.SpoolStates(ctx); err != nil {
		return r, fmt.Errorf("reading webhook spool states: %w", err)
	}
	span := UnauthorizedSpan
	for _, rules := range append([]SourceRules{settings.normalized().Defaults}, mapValues(settings.Sources)...) {
		if rules.RefusalMinutes != nil {
			span = max(span, time.Duration(*rules.RefusalMinutes)*time.Minute)
		}
	}
	if r.PerSource, err = s.d.Reader.AlertReadings(ctx, now.Add(-span).Truncate(time.Minute), now.Add(-AckSpan)); err != nil {
		return r, fmt.Errorf("reading webhook alert readings: %w", err)
	}
	return r, nil
}

func mapValues(m map[string]SourceRules) []SourceRules {
	out := make([]SourceRules, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
