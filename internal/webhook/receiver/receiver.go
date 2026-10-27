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
//
// A receiver given a spool (#2121) answers 202 once the events are synced to
// its own disk instead, so a sender's wait is local disk latency rather than
// object storage's, and drains them to object storage behind the answer; see
// spool.go.
package receiver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whspool"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// Defaults for the receiver's own timing.
const (
	DefaultWriteTimeout = 30 * time.Second
	DefaultRefresh      = 5 * time.Second
	DefaultStatsFlush   = 10 * time.Second
)

// loops is how many background loops Start runs beside the buffers' writers:
// the source refresh, the stats flush and the window registration.
const loops = 3

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
	// RegisterRetry is how often a window whose raw partition could not be
	// registered is tried again.
	RegisterRetry time.Duration
	// MissRefresh is the least time between two re-reads of the sources made
	// for requests naming a source this replica does not serve.
	MissRefresh time.Duration
	// Spool, when set, is the receiver's local spool (#2121): a request is
	// answered once its events are synced to it, and drained to object
	// storage behind the answer. Nil answers once they are in object storage.
	Spool       Spool
	SpoolLimits SpoolLimits
	Now         func() time.Time
}

// Receiver serves the webhook routes.
type Receiver struct {
	cfg    Config
	writer string
	seq    atomic.Uint64

	mu       sync.RWMutex
	passMu   sync.Mutex // one registerPending pass at a time
	onStored StoredFunc
	sources  map[string]served
	buffers  map[string]*buffer
	drainers map[string]*drainer
	limiters map[string]*limiter

	// started is when the receiver was built; spool is how its drain has
	// gone since.
	started time.Time
	spool   spoolTally
	// volumeOf reads the filesystem the spool is on (#2148).
	volumeOf func(dir string) (whspool.Volume, error)
	// minutes is this replica's events accepted and written per minute
	// since the last stats flush, guarded by statsMu (#2150).
	minutes map[time.Time]*whstore.ReplicaMinute

	// missMu guards the re-reads requests for a name not served wait on:
	// lastMiss is when the last of them started, and nextMiss the one not
	// yet started that a new miss joins.
	missMu   sync.Mutex
	lastMiss time.Time
	nextMiss *missRead

	// rawWindows holds the windows whose raw partition this process has seen
	// registered, so it registers each once rather than per segment, and
	// marked those it recorded before a write; both map a window's key to its
	// start.
	rawWindows sync.Map
	marked     sync.Map

	// regPending holds the windows owed a registration, which the
	// registration loop works through when regKick wakes it, and latest each
	// source's newest window written, whose successor the loop registers
	// ahead of time.
	regMu      sync.Mutex
	regPending map[string]rawWindow
	latest     map[string]time.Time
	regKick    chan struct{}

	statsMu    sync.Mutex
	counts     map[countKey]int64
	latency    map[latencyKey]*whstore.Latency
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
	status  int
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
	if cfg.RegisterRetry <= 0 {
		cfg.RegisterRetry = DefaultRegisterRetry
	}
	if cfg.MissRefresh <= 0 {
		cfg.MissRefresh = DefaultMissRefresh
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	start := cfg.Now()
	return &Receiver{
		cfg:        cfg,
		writer:     whlayout.ReplicaSlug(cfg.Replica) + "-" + strconv.FormatInt(start.UnixNano(), writerIDBase),
		sources:    map[string]served{},
		buffers:    map[string]*buffer{},
		drainers:   map[string]*drainer{},
		limiters:   map[string]*limiter{},
		started:    start,
		volumeOf:   whspool.VolumeOf,
		minutes:    map[time.Time]*whstore.ReplicaMinute{},
		counts:     map[countKey]int64{},
		latency:    map[latencyKey]*whstore.Latency{},
		regPending: map[string]rawWindow{},
		latest:     map[string]time.Time{},
		regKick:    make(chan struct{}, 1),
		reload:     make(chan struct{}, 1),
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
	r.wg.Add(loops)
	go r.refreshLoop()
	go r.statsLoop()
	go r.registerLoop()
	r.startDrains()
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
	if err := r.flushStats(context.Background()); err != nil {
		slog.Warn("webhook receiver: writing request counts at shutdown failed", "error", logsan.SanitizeForLog(err.Error()))
	}
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

// refreshLoop re-reads the sources on the refresh interval and when an
// administrator's change on this replica asks, until Stop.
func (r *Receiver) refreshLoop() {
	defer r.wg.Done()
	bgloop.Run(r.ctx, bgloop.Loop{
		Name: bgloop.NameWebhookRefresh, Every: r.cfg.Refresh, Wake: r.reload,
		Body: func(ctx context.Context) error {
			err := r.refresh(ctx)
			if err != nil {
				r.cfg.Logger.WarnContext(ctx, "webhooks: refreshing sources", logKeyError, logsan.SanitizeForLog(err.Error()))
			}
			return err
		},
	})
}

// statsLoop writes the request counts and rejections until Stop.
func (r *Receiver) statsLoop() {
	defer r.wg.Done()
	bgloop.Run(r.ctx, bgloop.Loop{Name: bgloop.NameWebhookStats, Every: r.cfg.StatsFlush, Body: r.flushStats})
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
		if s.IsIMAP() {
			// A polled mailbox is not posted to; a request naming it is
			// answered as for an unknown source (#2247).
			continue
		}
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
func (r *Receiver) count(source, outcome string, status int) {
	if outcome == OutcomeUnknownSource {
		r.metricRequest("", outcome)
		return
	}
	r.metricRequest(source, outcome)
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.WebhookResponse(context.Background(), source, status)
	}
	k := countKey{source: source, minute: r.now().UTC().Truncate(time.Minute), outcome: outcome, status: status}
	r.statsMu.Lock()
	r.counts[k]++
	r.statsMu.Unlock()
}

// reject keeps a refused request for the source's page, naming its events
// when they were read.
func (r *Receiver) reject(d delivery, outcome, reason string) {
	rej := whstore.Rejection{
		Source: d.source, At: r.now(), Outcome: outcome, Reason: reason,
		RequestHash: d.hash, EventIDs: d.eventIDs(),
	}
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	if len(r.rejections) >= maxPendingRejections {
		r.rejections = r.rejections[1:]
	}
	r.rejections = append(r.rejections, rej)
}

// flushStats writes the counts and rejections gathered since the last write.
// A failed write puts them back to be written with the next: the counts
// summed into what arrived since, the rejections ahead of the newer ones, the
// whole still bounded by maxPendingRejections.
func (r *Receiver) flushStats(ctx context.Context) error {
	r.statsMu.Lock()
	counts := make([]whstore.Count, 0, len(r.counts))
	for k, v := range r.counts {
		counts = append(counts, whstore.Count{Source: k.source, Minute: k.minute, Outcome: k.outcome, Status: k.status, Count: v})
	}
	rejections := r.rejections
	latency := make([]whstore.Latency, 0, len(r.latency))
	for _, l := range r.latency {
		latency = append(latency, *l)
	}
	r.counts = map[countKey]int64{}
	r.latency = map[latencyKey]*whstore.Latency{}
	r.rejections = nil
	r.statsMu.Unlock()

	var errs []error
	if err := r.flushSpool(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := r.flushMinutes(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := r.cfg.Recorder.RecordLatency(ctx, latency); err != nil {
		r.cfg.Logger.WarnContext(ctx, "webhooks: writing acknowledgement latency", logKeyError, logsan.SanitizeForLog(err.Error()))
		r.restoreLatency(latency)
		errs = append(errs, err)
	}
	if err := r.cfg.Recorder.RecordCounts(ctx, counts); err != nil {
		r.cfg.Logger.WarnContext(ctx, "webhooks: writing request counts", logKeyError, logsan.SanitizeForLog(err.Error()))
		r.statsMu.Lock()
		for _, c := range counts {
			r.counts[countKey{source: c.Source, minute: c.Minute, outcome: c.Outcome, status: c.Status}] += c.Count
		}
		r.statsMu.Unlock()
		errs = append(errs, err)
	}
	if err := r.cfg.Recorder.RecordRejections(ctx, rejections); err != nil {
		r.cfg.Logger.WarnContext(ctx, "webhooks: writing rejections", logKeyError, logsan.SanitizeForLog(err.Error()))
		r.statsMu.Lock()
		kept := make([]whstore.Rejection, 0, maxPendingRejections)
		kept = append(kept, rejections...)
		kept = append(kept, r.rejections...)
		if over := len(kept) - maxPendingRejections; over > 0 {
			kept = kept[over:]
		}
		r.rejections = kept
		r.statsMu.Unlock()
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("webhooks: writing request stats: %w", err)
	}
	return nil
}

// restoreLatency puts back latency a failed write did not store, merged into
// what arrived since.
func (r *Receiver) restoreLatency(latency []whstore.Latency) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	for _, l := range latency {
		k := latencyKey{source: l.Source, minute: l.Minute}
		if cur := r.latency[k]; cur != nil {
			cur.Merge(l)
			continue
		}
		kept := l
		r.latency[k] = &kept
	}
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
