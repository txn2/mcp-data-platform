// Package receiver serves POST /hooks/{source} (#1870): it authenticates each
// request, turns its body into events, buffers them per source, writes them to
// object storage as gzipped JSON-lines segments, and answers 202 only once the
// segment holding them is written.
//
// A 2xx therefore means the events are in object storage. A replica that dies
// holding a buffer answered nobody for those events, so their senders retry,
// and backpressure is the sender's own retry queue: a full buffer or a failed
// write is answered 503 with Retry-After, and the platform keeps no backlog of
// its own beyond each source's buffer limit.
package receiver

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// Defaults for the receiver's own timing.
const (
	DefaultWriteTimeout = 30 * time.Second
	DefaultRefresh      = 5 * time.Second
	DefaultStatsFlush   = 10 * time.Second
)

// writerIDBase is the base the process start time is written in, in the
// name of every segment this process writes.
const writerIDBase = 36

// logKeyError is the log attribute an error is reported under.
const logKeyError = "error"

// maxPendingRejections bounds the rejections held between two writes of them.
// The source's page shows the newest fifty, so a burst of refusals beyond
// this loses nothing a person could see.
const maxPendingRejections = 500

// Config is what a Receiver is built from.
type Config struct {
	Sources    SourceLister
	Objects    ObjectWriter
	Bucket     string
	Recorder   Recorder
	RawWindows RawWindows
	Metrics    Metrics
	// Replica is this process's identity, the value of X-Platform-Instance.
	Replica string
	Logger  *slog.Logger
	// WriteTimeout bounds one segment write, including its retries by the
	// S3 client. A write that has not finished by then is answered 503.
	WriteTimeout time.Duration
	// Refresh is how often the source list is re-read, which is how a
	// change made on another replica reaches this one.
	Refresh    time.Duration
	StatsFlush time.Duration
	Now        func() time.Time
}

// Receiver serves the webhook routes.
type Receiver struct {
	cfg    Config
	writer string
	seq    atomic.Uint64

	mu       sync.RWMutex
	sources  map[string]served
	buffers  map[string]*buffer
	limiters map[string]*limiter

	// missMu serializes the re-reads a request for a name not served makes,
	// and lastMiss is when the last of them ran.
	missMu   sync.Mutex
	lastMiss time.Time

	// rawWindows holds the windows whose raw partition this process has seen
	// registered, so it registers each once rather than per segment.
	rawWindows sync.Map

	statsMu    sync.Mutex
	counts     map[countKey]int64
	rejections []whstore.Rejection

	ctx     context.Context
	cancel  context.CancelFunc
	stopped bool
	wg      sync.WaitGroup
	reload  chan struct{}
}

// served is a source with its paths parsed once.
type served struct {
	whsource.Source
	paths whevent.Paths
}

// limiter is a source's rate limiter and the settings it was built with, so a
// change of settings replaces it.
type limiter struct {
	l          *ratelimit.Limiter
	rpm, burst int
}

type countKey struct {
	source  string
	minute  time.Time
	outcome string
}

// New builds a receiver. Start begins serving.
func New(cfg Config) *Receiver {
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = DefaultWriteTimeout
	}
	if cfg.Refresh <= 0 {
		cfg.Refresh = DefaultRefresh
	}
	if cfg.StatsFlush <= 0 {
		cfg.StatsFlush = DefaultStatsFlush
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	start := cfg.Now()
	return &Receiver{
		cfg:      cfg,
		writer:   whlayout.ReplicaSlug(cfg.Replica) + "-" + strconv.FormatInt(start.UnixNano(), writerIDBase),
		sources:  map[string]served{},
		buffers:  map[string]*buffer{},
		limiters: map[string]*limiter{},
		counts:   map[countKey]int64{},
		reload:   make(chan struct{}, 1),
	}
}

func (r *Receiver) now() time.Time { return r.cfg.Now() }

// Start loads the sources and begins refreshing them and writing counts.
func (r *Receiver) Start(ctx context.Context) {
	if r == nil {
		return
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	if err := r.refresh(r.ctx); err != nil {
		r.cfg.Logger.Warn("webhooks: loading sources", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
	r.wg.Add(1)
	go r.background()
}

// Stop writes every pending buffer, answering the requests waiting on them,
// then the counts, and releases the limiters.
func (r *Receiver) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.cancel()
	r.wg.Wait()
	r.flushStats(context.Background())
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.limiters {
		l.l.Close()
	}
	r.limiters = map[string]*limiter{}
}

// Reload re-reads the sources now rather than at the next refresh: it is what
// an administrator's change on this replica calls.
func (r *Receiver) Reload() {
	if r == nil {
		return
	}
	select {
	case r.reload <- struct{}{}:
	default:
	}
}

// background refreshes the sources and writes the counts until Stop.
func (r *Receiver) background() {
	defer r.wg.Done()
	refresh := time.NewTicker(r.cfg.Refresh)
	stats := time.NewTicker(r.cfg.StatsFlush)
	defer refresh.Stop()
	defer stats.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-refresh.C:
		case <-r.reload:
		case <-stats.C:
			r.flushStats(r.ctx)
			continue
		}
		if err := r.refresh(r.ctx); err != nil {
			r.cfg.Logger.Warn("webhooks: refreshing sources", logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// refresh replaces the served sources with the stored ones. A source that
// fails to parse is not served, and says so in the log.
func (r *Receiver) refresh(ctx context.Context) error {
	list, err := r.cfg.Sources.List(ctx)
	if err != nil {
		return fmt.Errorf("listing webhook sources: %w", err)
	}
	next := make(map[string]served, len(list))
	for _, s := range list {
		s = s.WithDefaults()
		paths, err := whevent.ParsePaths(s.Config)
		if err != nil {
			r.cfg.Logger.Warn("webhooks: source not served", "source", logsan.SanitizeForLog(s.Name),
				logKeyError, logsan.SanitizeForLog(err.Error()))
			continue
		}
		next[s.Name] = served{Source: s, paths: paths}
	}
	r.mu.Lock()
	r.sources = next
	r.mu.Unlock()
	return nil
}

// missRefresh bounds how often a request for a name this replica does not
// serve re-reads the sources. A source created on another replica is served
// here on its first request rather than at the next refresh, and a caller
// inventing names costs the database at most one read per interval.
const missRefresh = time.Second

// lookupFresh is lookup, re-reading the sources first when the name is not
// served and the last re-read for a miss is older than missRefresh.
//
// Misses are handled one at a time: a request that misses while another is
// re-reading waits for that re-read and looks again, rather than answering
// 404 for a source the re-read under way is about to find.
func (r *Receiver) lookupFresh(ctx context.Context, name string) (served, bool) {
	if s, ok := r.lookup(name); ok {
		return s, true
	}
	r.missMu.Lock()
	defer r.missMu.Unlock()
	if s, ok := r.lookup(name); ok {
		return s, true
	}
	now := r.now()
	if now.Sub(r.lastMiss) < missRefresh {
		return served{}, false
	}
	r.lastMiss = now
	if err := r.refresh(ctx); err != nil {
		r.cfg.Logger.Warn("webhooks: refreshing sources", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
	return r.lookup(name)
}

// lookup returns a source that is served: stored and enabled.
func (r *Receiver) lookup(name string) (served, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[name]
	if !ok || !s.Enabled {
		return served{}, false
	}
	return s, true
}

// bufferFor returns a source's buffer, starting its writer the first time,
// and nil once the receiver is stopping.
func (r *Receiver) bufferFor(name string) *buffer {
	r.mu.RLock()
	b, stopped := r.buffers[name], r.stopped
	r.mu.RUnlock()
	if stopped {
		return nil
	}
	if b != nil {
		return b
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.ctx == nil {
		return nil
	}
	if existing := r.buffers[name]; existing != nil {
		return existing
	}
	b = newBuffer(r, name)
	r.buffers[name] = b
	r.wg.Add(1)
	go b.run(r.ctx)
	return b
}

// allow applies a source's rate limit, and reports how many seconds a refused
// sender should wait.
func (r *Receiver) allow(s whsource.Source) (allowed bool, retryAfter int) {
	rpm, burst := s.Config.RateLimitPerMinute, s.Config.RateLimitBurst
	if rpm <= 0 {
		return true, 0
	}
	if burst <= 0 {
		burst = rpm
	}
	r.mu.Lock()
	l := r.limiters[s.Name]
	if l == nil || l.rpm != rpm || l.burst != burst {
		if l != nil {
			l.l.Close()
		}
		l = &limiter{l: ratelimit.New(ratelimit.Config{RequestsPerMinute: rpm, BurstSize: burst}), rpm: rpm, burst: burst}
		r.limiters[s.Name] = l
	}
	r.mu.Unlock()
	if l.l.Allow(s.Name) {
		return true, 0
	}
	return false, max(1, (60+rpm-1)/rpm)
}

// count adds one request with outcome to this minute's totals. A request to
// no source is reported under an empty source label and never stored: the
// name came from the request, and a label per name a caller invents would be
// a metric series per request.
func (r *Receiver) count(source, outcome string) {
	if outcome == OutcomeUnknownSource {
		r.metricRequest("", outcome)
		return
	}
	r.metricRequest(source, outcome)
	k := countKey{source: source, minute: r.now().UTC().Truncate(time.Minute), outcome: outcome}
	r.statsMu.Lock()
	r.counts[k]++
	r.statsMu.Unlock()
}

// reject keeps a refused request for the source's page.
func (r *Receiver) reject(source, outcome, reason string) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	if len(r.rejections) >= maxPendingRejections {
		r.rejections = r.rejections[1:]
	}
	r.rejections = append(r.rejections, whstore.Rejection{Source: source, At: r.now(), Outcome: outcome, Reason: reason})
}

// flushStats writes the counts and rejections gathered since the last write.
// A failed write puts the counts back to be written with the next.
func (r *Receiver) flushStats(ctx context.Context) {
	r.statsMu.Lock()
	counts := make([]whstore.Count, 0, len(r.counts))
	for k, v := range r.counts {
		counts = append(counts, whstore.Count{Source: k.source, Minute: k.minute, Outcome: k.outcome, Count: v})
	}
	rejections := r.rejections
	r.counts = map[countKey]int64{}
	r.rejections = nil
	r.statsMu.Unlock()

	if err := r.cfg.Recorder.RecordCounts(ctx, counts); err != nil {
		r.cfg.Logger.Warn("webhooks: writing request counts", logKeyError, logsan.SanitizeForLog(err.Error()))
		r.statsMu.Lock()
		for _, c := range counts {
			r.counts[countKey{source: c.Source, minute: c.Minute, outcome: c.Outcome}] += c.Count
		}
		r.statsMu.Unlock()
	}
	if err := r.cfg.Recorder.RecordRejections(ctx, rejections); err != nil {
		r.cfg.Logger.Warn("webhooks: writing rejections", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// ensureRawWindow registers a source's raw partition for the window starting
// at start the first time this process writes into it. It is best effort: a query engine that is down
// never stops events being stored, and the compactor's sync registers what
// this missed.
func (r *Receiver) ensureRawWindow(ctx context.Context, source string, start time.Time) {
	if r.cfg.RawWindows == nil {
		return
	}
	key := source + "@" + start.UTC().Format(time.RFC3339)
	if _, done := r.rawWindows.Load(key); done {
		return
	}
	src, ok := r.lookup(source)
	if !ok {
		return
	}
	if err := r.cfg.RawWindows.EnsureRawWindow(ctx, src.Source, start); err != nil {
		r.cfg.Logger.Warn("webhooks: registering a new window", "source", logsan.SanitizeForLog(source),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return
	}
	r.rawWindows.Store(key, struct{}{})
}

func (r *Receiver) metricRequest(source, outcome string) {
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.WebhookRequest(context.Background(), source, outcome)
	}
}

func (r *Receiver) metricSegment(source string) {
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.WebhookSegmentWritten(context.Background(), source)
	}
}

func (r *Receiver) metricBuffer(source string, events int) {
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.WebhookBuffer(context.Background(), source, events)
	}
}
