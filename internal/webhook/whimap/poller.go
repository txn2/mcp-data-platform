package whimap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/mailtrust"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// Tick is how often the poller looks for a source whose poll is due.
const Tick = 15 * time.Second

// maxPerPoll bounds the messages one poll of one folder files; the rest wait
// for the next poll, oldest first.
const maxPerPoll = 200

// headerBatch is how many messages' headers one FETCH asks for.
const headerBatch = 100

// sessionTimeout bounds one source's whole poll.
const sessionTimeout = 5 * time.Minute

// dialTimeout bounds reaching the source's server.
const dialTimeout = 30 * time.Second

// segmentContentType is what a raw segment is stored as, the HTTP receiver's.
const segmentContentType = "application/gzip"

// Config assembles a poller.
type Config struct {
	Sources    Sources
	Objects    Objects
	Bucket     string
	Recorder   Recorder
	RawWindows RawWindows
	Marks      MarkStore
	Files      Files
	Lock       Lock
	Replica    string
	Logger     *slog.Logger
	// Open opens a session; nil is imapmail.Open.
	Open func(ctx context.Context, a imapmail.Account) (*imapmail.Session, error)
	Now  func() time.Time
}

// Poller polls every imap source on its interval.
type Poller struct {
	cfg  Config
	seq  atomic.Uint64
	stop chan struct{}
	wg   sync.WaitGroup

	mu   sync.Mutex
	last map[string]time.Time
	// onStored is told each window's events once their segment is written
	// (#2332); nil tells nothing.
	onStored func(ctx context.Context, source string, events []whevent.Event)
}

// SetStored is what is told each written segment's events from here on: the
// hook the runs of an automation naming the source are enqueued through
// (#2332). It is set before the poller starts.
func (p *Poller) SetStored(fn func(ctx context.Context, source string, events []whevent.Event)) {
	p.mu.Lock()
	p.onStored = fn
	p.mu.Unlock()
}

// New returns a poller, nil when a dependency is missing.
func New(cfg Config) *Poller {
	if cfg.Sources == nil || cfg.Objects == nil || cfg.Recorder == nil || cfg.Marks == nil || cfg.Files == nil || cfg.Lock == nil {
		return nil
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Open == nil {
		cfg.Open = imapmail.Open
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	p := &Poller{cfg: cfg, stop: make(chan struct{}), last: map[string]time.Time{}}
	p.seq.Store(uint64(cfg.Now().UnixNano())) //nolint:gosec // a clock reading seeds a counter
	return p
}

// Start runs the poll loop until Stop.
func (p *Poller) Start(ctx context.Context) {
	if p == nil {
		return
	}
	p.wg.Go(func() {
		bgloop.Run(ctx, bgloop.Loop{Name: bgloop.NameWebhookIMAPPoll, Every: Tick, Stop: p.stop, Body: p.Pass})
	})
}

// Stop ends the loop and waits for a poll in progress.
func (p *Poller) Stop() {
	if p == nil {
		return
	}
	close(p.stop)
	p.wg.Wait()
}

// Pass polls every imap source whose interval has passed.
func (p *Poller) Pass(ctx context.Context) error {
	list, err := p.cfg.Sources.List(ctx)
	if err != nil {
		return fmt.Errorf("listing webhook sources: %w", err)
	}
	var errs []error
	for _, src := range list {
		src = src.WithDefaults()
		if !src.Enabled || !src.IsIMAP() || !p.due(src) {
			continue
		}
		if err := p.pollLocked(ctx, src); err != nil {
			errs = append(errs, err)
		}
	}
	return joined("every source", errs)
}

// joined reports the failed polls of what as one error, nil when there are
// none.
func joined(what string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("imap poll of %s: %w", what, errors.Join(errs...))
}

func (p *Poller) due(src whsource.Source) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	last, ok := p.last[src.Name]
	return !ok || p.cfg.Now().Sub(last) >= time.Duration(src.Config.IMAP.PollSeconds)*time.Second
}

func (p *Poller) polled(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last[name] = p.cfg.Now()
}

// pollLocked polls a source under its one-replica lock; a source another
// replica is polling is left to it.
func (p *Poller) pollLocked(ctx context.Context, src whsource.Source) error {
	release, ok, err := p.cfg.Lock(ctx, src.Name)
	if err != nil {
		return fmt.Errorf("locking imap source %s: %w", src.Name, err)
	}
	if !ok {
		return nil
	}
	defer release()
	p.polled(src.Name)
	return p.Poll(ctx, src)
}

// Poll reads every folder of one source once.
func (p *Poller) Poll(ctx context.Context, src whsource.Source) error {
	ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()
	s, err := p.cfg.Open(ctx, account(src))
	if err != nil {
		p.recordError(ctx, src, err)
		return fmt.Errorf("imap source %s: %w", src.Name, err)
	}
	defer s.Close() //nolint:errcheck // logout after the poll
	var errs []error
	for _, folder := range src.Config.IMAP.Folders {
		if err := p.pollFolder(ctx, s, src, folder); err != nil {
			p.cfg.Logger.WarnContext(ctx, "webhooks: imap poll failed", "source", logsan.SanitizeForLog(src.Name),
				"folder", logsan.SanitizeForLog(folder), "error", logsan.SanitizeForLog(err.Error()))
			errs = append(errs, err)
		}
	}
	return joined(src.Name, errs)
}

// recordError notes a failed session on every folder's mark.
func (p *Poller) recordError(ctx context.Context, src whsource.Source, cause error) {
	now := p.cfg.Now()
	for _, folder := range src.Config.IMAP.Folders {
		mk, err := p.cfg.Marks.Get(ctx, src.Name, folder)
		if err != nil {
			continue
		}
		mk.LastPolledAt, mk.LastError = &now, cause.Error()
		_ = p.cfg.Marks.Put(ctx, mk)
	}
}

// folderPoll is one folder of one source being read: the session, the
// folder's UID validity, the sender rules and the mark its counts go on.
type folderPoll struct {
	s        *imapmail.Session
	src      whsource.Source
	folder   string
	validity uint32
	policy   mailtrust.Policy
	mark     *Mark
}

// pollFolder files the folder's mail above its watermark, oldest first, and
// moves the watermark past what it read.
func (p *Poller) pollFolder(ctx context.Context, s *imapmail.Session, src whsource.Source, folder string) error {
	mk, err := p.cfg.Marks.Get(ctx, src.Name, folder)
	if err != nil {
		return fmt.Errorf("reading the mark of %s: %w", folder, err)
	}
	validity, _, err := s.Examine(folder)
	if err != nil {
		return p.fail(ctx, mk, err)
	}
	if validity != mk.UIDValidity {
		mk.UIDValidity, mk.LastUID = validity, 0
	}
	uids, err := s.Search(imapmail.Criteria{SinceUID: mk.LastUID})
	if err != nil {
		return p.fail(ctx, mk, err)
	}
	if len(uids) > maxPerPoll {
		uids = uids[:maxPerPoll]
	}
	fp := folderPoll{s: s, src: src, folder: folder, validity: validity, policy: policyOf(*src.Config.IMAP), mark: &mk}
	events, err := p.read(ctx, fp, uids)
	if err != nil {
		return p.fail(ctx, mk, err)
	}
	if err := p.write(ctx, src, events); err != nil {
		return p.fail(ctx, mk, err)
	}
	if len(uids) > 0 {
		mk.LastUID = slices.Max(uids)
	}
	now := p.cfg.Now()
	mk.Stored += int64(len(events))
	mk.LastPolledAt, mk.LastError = &now, ""
	if err := p.cfg.Marks.Put(ctx, mk); err != nil {
		return fmt.Errorf("writing the mark of %s: %w", folder, err)
	}
	return nil
}

// fail records a folder's failed poll, leaving its watermark where it was.
func (p *Poller) fail(ctx context.Context, mk Mark, cause error) error {
	now := p.cfg.Now()
	mk.LastPolledAt, mk.LastError = &now, cause.Error()
	if err := p.cfg.Marks.Put(ctx, mk); err != nil {
		return fmt.Errorf("poll failed: %w; recording it failed: %w", cause, err)
	}
	return cause
}

// read turns the messages at uids into events, counting the ones the sender
// rules withhold.
func (p *Poller) read(ctx context.Context, fp folderPoll, uids []uint32) ([]whevent.Event, error) {
	var events []whevent.Event
	for start := 0; start < len(uids); start += headerBatch {
		envs, err := fp.s.Headers(uids[start:min(start+headerBatch, len(uids))])
		if err != nil {
			return nil, err //nolint:wrapcheck // the protocol package names itself
		}
		for _, env := range envs {
			ev, kept, err := p.one(ctx, fp, env)
			if err != nil {
				return nil, err
			}
			if kept {
				events = append(events, ev)
			}
		}
	}
	return events, nil
}

// one reads one message, reporting whether it became an event.
func (p *Poller) one(ctx context.Context, fp folderPoll, env imapmail.Envelope) (ev whevent.Event, kept bool, err error) {
	h, err := imapmail.ReadHeaders(env.Header)
	if err != nil {
		fp.mark.HiddenNotAllowed++
		return whevent.Event{}, false, nil //nolint:nilerr // a message whose headers do not parse is withheld like an unknown sender
	}
	j := fp.policy.Judge(h.From, h.AuthResults)
	switch j.Verdict {
	case mailtrust.Trusted:
	case mailtrust.NotAuthenticated:
		fp.mark.HiddenUnauthenticated++
		return whevent.Event{}, false, nil
	default:
		fp.mark.HiddenNotAllowed++
		return whevent.Event{}, false, nil
	}
	m := message{src: fp.src, folder: fp.folder, validity: fp.validity, env: env, headers: h, judgment: j}
	if limit := fp.src.Config.IMAP.MaxMessageBytes; env.Size <= limit {
		raw, err := fp.s.Whole(env.UID, limit)
		if err != nil {
			return whevent.Event{}, false, err //nolint:wrapcheck // the protocol package names itself
		}
		m.raw = raw
	}
	ev, err = m.event(ctx, p.cfg.Files, p.cfg.Replica)
	return ev, err == nil, err
}

// write files the events as segments, one per window, as the HTTP receiver
// does: the window recorded before and after the write, its raw partition
// registered after.
func (p *Poller) write(ctx context.Context, src whsource.Source, events []whevent.Event) error {
	if len(events) == 0 {
		return nil
	}
	length := src.Config.Window()
	byWindow := map[time.Time][]whevent.Event{}
	for _, e := range events {
		w := e.Window(length)
		byWindow[w] = append(byWindow[w], e)
	}
	starts := make([]time.Time, 0, len(byWindow))
	for w := range byWindow {
		starts = append(starts, w)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	for _, w := range starts {
		if err := p.writeWindow(ctx, src, w, length, byWindow[w]); err != nil {
			return err
		}
	}
	return nil
}

func (p *Poller) writeWindow(ctx context.Context, src whsource.Source, start time.Time, length time.Duration, events []whevent.Event) error {
	data, err := whevent.EncodeSegment(events, p.cfg.Now())
	if err != nil {
		return fmt.Errorf("encoding the segment: %w", err)
	}
	if err := p.cfg.Recorder.MarkSegment(ctx, src.Name, start, length, 0); err != nil {
		return fmt.Errorf("recording segment: %w", err)
	}
	key := whlayout.SegmentKey(src.Name, start, "imap-"+p.cfg.Replica, p.seq.Add(1))
	if err := p.cfg.Objects.PutObject(ctx, p.cfg.Bucket, key, data, segmentContentType); err != nil {
		return fmt.Errorf("writing segment: %w", err)
	}
	if err := p.cfg.Recorder.MarkSegment(ctx, src.Name, start, length, int64(len(data))); err != nil {
		return fmt.Errorf("recording segment: %w", err)
	}
	p.mu.Lock()
	stored := p.onStored
	p.mu.Unlock()
	if stored != nil {
		stored(ctx, src.Name, events)
	}
	if p.cfg.RawWindows != nil {
		if err := p.cfg.RawWindows.EnsureRawWindow(ctx, src, start); err != nil {
			p.cfg.Logger.WarnContext(ctx, "webhooks: registering an imap window's raw partition",
				"source", logsan.SanitizeForLog(src.Name), "error", logsan.SanitizeForLog(err.Error()))
		}
	}
	return nil
}

func policyOf(m whsource.IMAP) mailtrust.Policy {
	return mailtrust.Policy{
		Allow: mailtrust.NewAllowlist(m.AllowSenders), RequireAuth: m.AuthRequired(), AuthServID: m.TrustedAuthServID,
	}
}

// account is how a session reaches the source's server.
func account(src whsource.Source) imapmail.Account {
	m := src.Config.IMAP
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.Host}
	if m.TLSCABundlePEM != "" {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(m.TLSCABundlePEM))
		cfg.RootCAs = pool
	}
	return imapmail.Account{
		Host: m.Host, Port: m.Port, TLS: m.TLS, Username: src.Auth.Username, Password: src.Auth.Secret,
		TLSConfig: cfg, DialTimeout: dialTimeout,
	}
}
