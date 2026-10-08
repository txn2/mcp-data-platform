// Package platformstate reports what the platform depends on and how its
// connections stand (#1898): dependency_up per dependency, connections by the
// state their last call found them in, the persona count, how long ago each search source last
// finished indexing, and mcp_platform_config_info. Before it every one of
// these was pull-only, answered by an admin route somebody had to think to
// open.
//
// The probes run off the scrape path. A scrape reads the last result and,
// when that result is older than the probe interval, starts one refresh in
// the background, so a dependency that hangs costs a goroutine and never a
// scrape. Each replica probes for itself: dependency_up answers whether THIS
// replica reaches the dependency.
//
// Nothing here gates readiness. /readyz reports the process state alone, so an
// upstream outage never takes every replica out of a load balancer at once,
// including the routes that do not need that upstream.
package platformstate

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Defaults for the probe cadence.
const (
	// DefaultInterval is how old a result may be before a scrape refreshes it.
	DefaultInterval = time.Minute
	// DefaultProbeTimeout bounds one dependency probe.
	DefaultProbeTimeout = 5 * time.Second
	// refreshTimeout bounds a whole refresh, every probe run in parallel.
	refreshTimeout = 30 * time.Second
	// probeParallelism bounds how many probes run at once.
	probeParallelism = 8
)

// Pinger is a dependency that can say whether it answers.
type Pinger interface {
	Ping(ctx context.Context) error
}

// PingFunc adapts a function to Pinger.
type PingFunc func(ctx context.Context) error

// Ping calls f.
func (f PingFunc) Ping(ctx context.Context) error { return f(ctx) }

// Deps is what the sampler reads.
type Deps struct {
	// DB is the platform database, nil without one. It is pinged, and the
	// index-job history it holds is read for the search-index ages.
	DB *sql.DB
	// Dependencies maps a dependency name (observability.Dependency*) to its
	// probe. A dependency this deployment does not configure is absent.
	Dependencies map[string]Pinger
	// Toolkits is the toolkit registry whose connections are counted by the
	// state their last call found them in.
	Toolkits *registry.Registry
	// Personas is the persona registry counted.
	Personas *persona.Registry
	// Interval and ProbeTimeout override the defaults when positive.
	Interval     time.Duration
	ProbeTimeout time.Duration
}

// Sampler holds the last probe results and refreshes them.
type Sampler struct {
	deps    Deps
	now     func() time.Time
	mu      sync.Mutex
	last    observability.PlatformStateSample
	at      time.Time
	running bool
	done    chan struct{}
}

// New returns a sampler over deps. It has probed nothing until the first
// Refresh or scrape.
func New(deps Deps) *Sampler {
	if deps.Interval <= 0 {
		deps.Interval = DefaultInterval
	}
	if deps.ProbeTimeout <= 0 {
		deps.ProbeTimeout = DefaultProbeTimeout
	}
	return &Sampler{deps: deps, now: time.Now}
}

// Start registers s as m's platform-state sampler and starts its first
// refresh. Nil-safe on m: with metrics off nothing is probed.
func Start(m *observability.Metrics, deps Deps) *Sampler {
	s := New(deps)
	if m == nil {
		return s
	}
	m.RegisterPlatformState(s.Sample)
	s.kick()
	return s
}

// Sample is the scrape-time read: the last result, with the persona count and
// the connection states read now (both in memory), and a background refresh
// started when the result is stale.
func (s *Sampler) Sample(_ context.Context) observability.PlatformStateSample {
	s.mu.Lock()
	out := s.last
	stale := s.now().Sub(s.at) >= s.deps.Interval
	s.mu.Unlock()
	if stale {
		s.kick()
	}
	if s.deps.Personas != nil {
		out.Personas = int64(len(s.deps.Personas.All()))
		out.PersonasKnown = true
	}
	out.Connections = connectionStates(s.deps.Toolkits)
	sortSample(&out)
	return out
}

// kick starts a refresh unless one is running.
func (s *Sampler) kick() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	done := make(chan struct{})
	s.done = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		s.Refresh(ctx)
	}()
}

// Refresh pings every dependency and reads the index ages, in parallel, and
// stores the result.
func (s *Sampler) Refresh(ctx context.Context) {
	var (
		mu   sync.Mutex
		next observability.PlatformStateSample
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(probeParallelism)
	for name, p := range s.dependencies() {
		g.Go(func() error {
			up := s.ping(gctx, p)
			mu.Lock()
			next.Dependencies = append(next.Dependencies, observability.DependencySample{Dependency: name, Up: up})
			mu.Unlock()
			return nil
		})
	}
	g.Go(func() error {
		ages := indexAges(gctx, s.deps.DB)
		mu.Lock()
		next.IndexAges = ages
		mu.Unlock()
		return nil
	})
	_ = g.Wait()
	sortSample(&next)
	s.mu.Lock()
	s.last, s.at, s.running = next, s.now(), false
	s.mu.Unlock()
}

// dependencies is the configured probes plus the database.
func (s *Sampler) dependencies() map[string]Pinger {
	out := make(map[string]Pinger, len(s.deps.Dependencies))
	for name, p := range s.deps.Dependencies {
		if p != nil {
			out[name] = p
		}
	}
	if s.deps.DB != nil {
		out[opsobs.DependencyDatabase] = PingFunc(s.deps.DB.PingContext)
	}
	return out
}

// ping runs one probe under the probe timeout.
func (s *Sampler) ping(ctx context.Context, p Pinger) bool {
	ctx, cancel := context.WithTimeout(ctx, s.deps.ProbeTimeout)
	defer cancel()
	return p.Ping(ctx) == nil
}
