package receiver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// fakeObjects records every segment written, and can fail or stall a write.
type fakeObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	fail    error
	stall   chan struct{}
	puts    atomic.Int64
}

func newObjects() *fakeObjects { return &fakeObjects{objects: map[string][]byte{}} }

func (f *fakeObjects) PutObject(ctx context.Context, _, key string, data []byte, _ string) error {
	f.puts.Add(1)
	f.mu.Lock()
	stall, fail := f.stall, f.fail
	f.mu.Unlock()
	if stall != nil {
		select {
		case <-stall:
		case <-ctx.Done():
			return fmt.Errorf("put %s: %w", key, ctx.Err())
		}
	}
	if fail != nil {
		return fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = data
	return nil
}

func (f *fakeObjects) events(t *testing.T) []whevent.Stored {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []whevent.Stored
	for _, data := range f.objects {
		evs, err := whevent.DecodeSegment(data)
		require.NoError(t, err)
		out = append(out, evs...)
	}
	return out
}

func (f *fakeObjects) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects)
}

// fakeRecorder records the control data the receiver writes.
type fakeRecorder struct {
	mu         sync.Mutex
	marks      map[string]int
	counts     map[string]int64
	rejections []whstore.Rejection
	latency    []whstore.Latency
	markErr    error
	countErr   error
	latencyErr error
	spool      []whstore.SpoolState
	// statuses counts by source and HTTP status; bytes is the segment bytes
	// marked; minutes and minutesErr are the receiver minutes written.
	statuses   map[string]int64
	bytes      int64
	minutes    []whstore.ReplicaMinute
	minutesErr error
}

func newRecorder() *fakeRecorder {
	return &fakeRecorder{marks: map[string]int{}, counts: map[string]int64{}, statuses: map[string]int64{}}
}

func (f *fakeRecorder) MarkSegment(_ context.Context, source string, start time.Time, length time.Duration, bytes int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.bytes += bytes
	f.marks[source+"@"+start.Format(time.RFC3339)+"/"+length.String()]++
	return nil
}

func (f *fakeRecorder) RecordCounts(_ context.Context, counts []whstore.Count) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.countErr != nil {
		return f.countErr
	}
	for _, c := range counts {
		f.counts[c.Source+"/"+c.Outcome] += c.Count
		f.statuses[c.Source+"/"+strconv.Itoa(c.Status)] += c.Count
	}
	return nil
}

func (f *fakeRecorder) RecordReplicaMinutes(_ context.Context, m []whstore.ReplicaMinute) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.minutesErr != nil {
		return f.minutesErr
	}
	f.minutes = append(f.minutes, m...)
	return nil
}

func (f *fakeRecorder) RecordRejections(_ context.Context, r []whstore.Rejection) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejections = append(f.rejections, r...)
	return nil
}

func (f *fakeRecorder) RecordLatency(_ context.Context, l []whstore.Latency) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.latencyErr != nil {
		return f.latencyErr
	}
	f.latency = append(f.latency, l...)
	return nil
}

func (f *fakeRecorder) RecordSpool(_ context.Context, st whstore.SpoolState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spool = append(f.spool, st)
	return nil
}

func (f *fakeRecorder) markCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.marks[key]
}

// fakeMetrics records what was reported.
type fakeMetrics struct {
	mu        sync.Mutex
	requests  map[string]int
	events    int
	segments  int
	acks      int
	maxBuffer int
	drained   int
	drainFail int
	spoolSeen int
	responses map[int]int
	written   int
	stages    int
}

func (m *fakeMetrics) WebhookResponse(_ context.Context, _ string, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.responses == nil {
		m.responses = map[int]int{}
	}
	m.responses[status]++
}

func (m *fakeMetrics) WebhookWritten(_ context.Context, _ string, events, _ int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written += events
}

func (m *fakeMetrics) WebhookAckStages(context.Context, string, time.Duration, time.Duration, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stages++
}

func (m *fakeMetrics) WebhookRequest(_ context.Context, source, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[source+"/"+outcome]++
}

func (m *fakeMetrics) WebhookEvents(_ context.Context, _ string, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events += n
}

func (m *fakeMetrics) WebhookSegmentWritten(context.Context, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.segments++
}

func (m *fakeMetrics) WebhookAck(context.Context, string, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acks++
}

func (m *fakeMetrics) WebhookBuffer(_ context.Context, _ string, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxBuffer = max(m.maxBuffer, n)
}

func (m *fakeMetrics) WebhookSpool(context.Context, int64, int64, int, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spoolSeen++
}

func (m *fakeMetrics) WebhookSpoolDrained(_ context.Context, _ string, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drained += n
}

func (m *fakeMetrics) WebhookSpoolDrainFailed(context.Context, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drainFail++
}

// fakeRawWindows records the windows registered, and can fail or hold a
// registration until released. Every call is reported on called.
type fakeRawWindows struct {
	mu     sync.Mutex
	calls  int
	err    error
	hold   chan struct{}
	called chan error
}

func (f *fakeRawWindows) EnsureRawWindow(ctx context.Context, _ whsource.Source, _ time.Time) error {
	f.mu.Lock()
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	f.calls++
	err := f.err
	f.mu.Unlock()
	f.called <- err
	return err
}

func (f *fakeRawWindows) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// next waits for the next registration and returns its result.
func (f *fakeRawWindows) next(t *testing.T) error {
	t.Helper()
	select {
	case err := <-f.called:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("no window was registered")
		return nil
	}
}

// logBuffer is a log sink a test can read while the receiver writes to it.
type logBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, err := l.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("buffering a log line: %w", err)
	}
	return n, nil
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// lines are the log lines that contain msg.
func (l *logBuffer) lines(msg string) []string {
	var out []string
	for line := range strings.SplitSeq(l.String(), "\n") {
		if strings.Contains(line, msg) {
			out = append(out, line)
		}
	}
	return out
}

type staticSources struct {
	mu    sync.Mutex
	list  []whsource.Source
	err   error
	reads int
}

func (s *staticSources) List(context.Context) ([]whsource.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return s.list, s.err
}

func (s *staticSources) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func (s *staticSources) set(list ...whsource.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = list
}

type harness struct {
	r        *Receiver
	objects  *fakeObjects
	recorder *fakeRecorder
	metrics  *fakeMetrics
	sources  *staticSources
	raw      *fakeRawWindows
	logs     *logBuffer
	srv      *httptest.Server
}

func hmacSource(name string) whsource.Source {
	return whsource.Source{
		Name: name, Enabled: true, Connection: "scratch",
		Auth: whsource.Auth{Mode: whsource.AuthHMAC, Secret: "k", SignatureHeader: "X-Sig"},
		Config: whsource.Config{
			FlushMaxIntervalMS: 20, EventIDPath: "$.id", EventTypePath: "$.type", KeyPath: "$.email",
		},
	}
}

func newHarness(t *testing.T, sources ...whsource.Source) *harness {
	t.Helper()
	h := &harness{
		objects:  newObjects(),
		recorder: newRecorder(),
		metrics:  &fakeMetrics{requests: map[string]int{}},
		sources:  &staticSources{list: sources},
		raw:      &fakeRawWindows{called: make(chan error, 64)},
		logs:     &logBuffer{},
	}
	h.r = New(Config{
		Sources: h.sources, Objects: h.objects, Bucket: "managed-resources", Recorder: h.recorder, RawWindows: h.raw,
		Metrics: h.metrics, Replica: "host-a:8080", Logger: slog.New(slog.NewTextHandler(h.logs, nil)),
		WriteTimeout: 2 * time.Second, Refresh: time.Hour, StatsFlush: time.Hour, RegisterRetry: 10 * time.Millisecond, MissRefresh: 20 * time.Millisecond,
	})
	h.r.Start(context.Background())
	h.srv = httptest.NewServer(h.r)
	t.Cleanup(func() {
		h.srv.Close()
		h.r.Stop()
	})
	return h
}

func sig(body string) string {
	m := hmac.New(sha256.New, []byte("k"))
	_, _ = m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

// reply is what a test reads from a response whose body is already closed.
type reply struct {
	StatusCode int
	Header     http.Header
}

// testClient keeps every connection a test opens idle rather than closing the
// ones past http.DefaultClient's two per host. The side that closes a
// connection holds its port in TIME_WAIT, so a burst of requests through the
// default client left hundreds of client ports held after each run, and a
// repeated run exhausted them ("can't assign requested address"). With the
// connections idle, the test server closes them when it shuts down and no
// client port is held.
//
//nolint:gochecknoglobals // one client for every test in the package, so their connections are pooled the same way.
var testClient = &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1024}}

// send sends req and closes the response body before returning its status
// and headers.
func send(req *http.Request) (reply, error) {
	resp, err := testClient.Do(req) // #nosec G704 -- the httptest server this test started
	if err != nil {
		return reply{}, fmt.Errorf("sending %s: %w", req.URL.Path, err)
	}
	_ = resp.Body.Close()
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}, nil
}

// do is send for a test's own goroutine, where a failed request fails the test.
func do(t *testing.T, req *http.Request) reply {
	t.Helper()
	r, err := send(req)
	require.NoError(t, err)
	return r
}

func (h *harness) post(t *testing.T, path, contentType, body string, hdr ...string) reply {
	t.Helper()
	req := h.request(t.Context(), path, contentType, body, hdr...)
	return do(t, req)
}

// request builds a POST to the test server.
func (h *harness) request(ctx context.Context, path, contentType, body string, hdr ...string) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return req
}

func (h *harness) signed(t *testing.T, source, body string) reply {
	t.Helper()
	return h.post(t, "/hooks/"+source, "application/json", body, "X-Sig", sig(body))
}

func TestAcceptWritesBeforeAnswering(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	resp := h.signed(t, "esp", `{"id":"e1","type":"open","email":"a@example.com"}`)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	evs := h.objects.events(t)
	require.Len(t, evs, 1, "the segment is written by the time the 202 arrives")
	assert.Equal(t, "e1", evs[0].EventID)
	assert.Equal(t, "open", evs[0].EventType)
	assert.Equal(t, "a@example.com", evs[0].Key)
	assert.Equal(t, "host-a:8080", evs[0].Replica)
	for key := range h.objects.objects {
		assert.True(t, strings.HasPrefix(key, "webhooks/esp/raw/dt="), key)
		assert.Contains(t, key, "/host-a-8080-")
	}
	assert.Equal(t, 2, h.recorder.marks["esp@"+evs[0].Window(time.Hour).Format(time.RFC3339)+"/1h0m0s"],
		"the window is recorded before and after the write, at the source's default length")
	assert.Equal(t, 1, h.metrics.events)
	assert.Equal(t, 1, h.metrics.acks)
	assert.Equal(t, 1, h.metrics.requests["esp/accepted"])
}

func TestNewWindowIsRegisteredOnce(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	h.raw.setErr(errors.New("trino down"))
	assert.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"1"}`).StatusCode,
		"a query engine that is down never stops events being stored")
	require.Error(t, h.raw.next(t))
	h.raw.setErr(nil)
	// Retries already under way when the error is cleared may still fail;
	// the loop keeps trying until one succeeds.
	for err := h.raw.next(t); err != nil; err = h.raw.next(t) {
		t.Log("a retry under way when the error was cleared failed:", err)
	}
	failures := h.logs.lines("webhooks: registering a new window")
	require.NotEmpty(t, failures, "a failed registration is logged")
	assert.Contains(t, failures[0], "elapsed_ms=")

	h.raw.mu.Lock()
	registered := h.raw.calls
	h.raw.mu.Unlock()
	assert.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"2"}`).StatusCode)
	assert.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"3"}`).StatusCode)
	require.NoError(t, h.r.registerPending(context.Background()))
	h.raw.mu.Lock()
	defer h.raw.mu.Unlock()
	assert.Equal(t, registered, h.raw.calls, "registered until it succeeds, then not again")
}

// TestAcknowledgementDoesNotWaitForRegistration holds the window's
// registration and shows the delivery is answered 202 regardless (#2107).
func TestAcknowledgementDoesNotWaitForRegistration(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	h.raw.mu.Lock()
	h.raw.hold = make(chan struct{})
	h.raw.mu.Unlock()
	assert.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"1"}`).StatusCode,
		"a registration that has not finished never holds the acknowledgement")
	h.raw.mu.Lock()
	assert.Zero(t, h.raw.calls, "the registration is still held")
	close(h.raw.hold)
	h.raw.mu.Unlock()
	require.NoError(t, h.raw.next(t), "the window is registered once released")
}

// TestAWindowIsRecordedBeforeOnlyItsFirstWrite: the first segment this
// process writes into a window records it before and after the write; every
// later one only after (#2107).
func TestAWindowIsRecordedBeforeOnlyItsFirstWrite(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"1"}`).StatusCode)
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"2"}`).StatusCode)
	window := h.objects.events(t)[0].Window(time.Hour)
	assert.Equal(t, 3, h.recorder.markCount("esp@"+window.Format(time.RFC3339)+"/1h0m0s"),
		"two before-and-after records for the first write, one after for the second")
}

// TestOldWindowsAreForgotten: the windows remembered as recorded and
// registered are dropped once they are older than windowsKept.
func TestOldWindowsAreForgotten(t *testing.T) {
	h := newHarness(t)
	old, recent := time.Now().Add(-windowsKept-time.Hour), time.Now()
	h.r.markedWindow("esp", old)
	h.r.markedWindow("esp", recent)
	h.r.rawWindows.Store(windowKey("esp", old), old)
	h.r.rawWindows.Store("not-a-time", "x")
	h.r.forgetOld()
	assert.True(t, h.r.needsMark("esp", old))
	assert.False(t, h.r.needsMark("esp", recent))
	_, kept := h.r.rawWindows.Load(windowKey("esp", old))
	assert.False(t, kept)
	_, kept = h.r.rawWindows.Load("not-a-time")
	assert.True(t, kept, "a value that is not a start is left alone")
}

// TestARegistrationForARemovedSourceIsDropped: a window queued for a source
// no longer served is dropped rather than retried.
func TestARegistrationForARemovedSourceIsDropped(t *testing.T) {
	h := newHarness(t)
	h.r.regMu.Lock()
	h.r.regPending["gone@x"] = rawWindow{source: "gone", start: time.Now()}
	h.r.regMu.Unlock()
	require.NoError(t, h.r.registerPending(context.Background()))
	h.r.regMu.Lock()
	defer h.r.regMu.Unlock()
	assert.Empty(t, h.r.regPending)
}

func TestFormPayload(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	body := url.Values{"payload": {`{"id":"f1"}`}}.Encode()
	resp := h.post(t, "/hooks/esp", "application/x-www-form-urlencoded", body, "X-Sig", sig(body))
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	evs := h.objects.events(t)
	require.Len(t, evs, 1)
	assert.Equal(t, "f1", evs[0].EventID)
	assert.JSONEq(t, `{"id":"f1"}`, string(evs[0].Payload))
}

func TestSplitMakesOneEventPerElement(t *testing.T) {
	src := hmacSource("batch")
	src.Config.Split = "$.events"
	h := newHarness(t, src)
	resp := h.signed(t, "batch", `{"events":[{"id":"a"},{"id":"b"},{"id":"c"}]}`)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.Len(t, h.objects.events(t), 3)
}

func TestRefusalsStoreNothing(t *testing.T) {
	small := hmacSource("small")
	small.Config.MaxBodyBytes = 16
	h := newHarness(t, hmacSource("esp"), small)

	tests := []struct {
		name    string
		resp    func() reply
		status  int
		outcome string
	}{
		{"bad signature", func() reply {
			return h.post(t, "/hooks/esp", "application/json", `{"id":1}`, "X-Sig", sig(`{"id":2}`))
		}, http.StatusUnauthorized, "esp/unauthorized"},
		{"no signature", func() reply {
			return h.post(t, "/hooks/esp", "application/json", `{"id":1}`)
		}, http.StatusUnauthorized, "esp/unauthorized"},
		{"too large", func() reply {
			body := `{"id":"` + strings.Repeat("x", 32) + `"}`
			return h.post(t, "/hooks/small", "application/json", body, "X-Sig", sig(body))
		}, http.StatusRequestEntityTooLarge, "small/too_large"},
		{"not json", func() reply {
			return h.post(t, "/hooks/esp", "application/json", `nope`, "X-Sig", sig(`nope`))
		}, http.StatusBadRequest, "esp/invalid_body"},
		{"form with no fields", func() reply {
			return h.post(t, "/hooks/esp", "application/x-www-form-urlencoded", ``, "X-Sig", sig(``))
		}, http.StatusBadRequest, "esp/invalid_body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.status, tt.resp().StatusCode)
		})
	}
	assert.Zero(t, h.objects.count(), "no refused request writes an object")
	assert.Zero(t, h.objects.puts.Load())

	_ = h.r.flushStats(context.Background())
	assert.Equal(t, int64(2), h.recorder.counts["esp/unauthorized"])
	assert.Equal(t, int64(1), h.recorder.counts["small/too_large"])
	assert.Len(t, h.recorder.rejections, 5)
	for _, r := range h.recorder.rejections {
		assert.NotContains(t, r.Reason, `"id"`, "a rejection never carries the body")
	}
}

func TestTooLargeWithoutContentLength(t *testing.T) {
	src := hmacSource("small")
	src.Config.MaxBodyBytes = 8
	h := newHarness(t, src)
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(strings.Repeat("x", 64)))
		_ = pw.Close()
	}()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL+"/hooks/small", pr)
	require.NoError(t, err)
	resp := do(t, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestUnknownAndDisabledAnswerTheSame(t *testing.T) {
	disabled := hmacSource("off")
	disabled.Enabled = false
	h := newHarness(t, hmacSource("esp"), disabled)

	read := func(path string) (int, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL+path, strings.NewReader(`{}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	s1, b1 := read("/hooks/off")
	s2, b2 := read("/hooks/nope")
	s3, b3 := read("/hooks/esp/extra")
	s4, _ := read("/hooks/")
	s5, _ := read("/hooks/esp/a/b")
	assert.Equal(t, http.StatusNotFound, s1)
	assert.Equal(t, http.StatusNotFound, s2)
	assert.Equal(t, http.StatusNotFound, s3, "a token segment on a source that takes none")
	assert.Equal(t, http.StatusNotFound, s4)
	assert.Equal(t, http.StatusNotFound, s5)
	assert.Equal(t, b1, b2)
	assert.Equal(t, b1, b3)
	assert.Equal(t, 5, h.metrics.requests["/unknown_source"], "an unknown name is never a label")
	_ = h.r.flushStats(context.Background())
	assert.Empty(t, h.recorder.counts)
}

func TestMethodNotAllowed(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.srv.URL+"/hooks/esp", http.NoBody)
	require.NoError(t, err)
	resp := do(t, req)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, "POST", resp.Header.Get("Allow"))
}

func TestCloudEventsHandshake(t *testing.T) {
	ce := hmacSource("ce")
	ce.Config.Handshake = whsource.HandshakeCloudEvents
	ce.Config.RateLimitPerMinute = 120
	h := newHarness(t, ce, hmacSource("plain"))

	options := func(path, origin string) reply {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, h.srv.URL+path, http.NoBody)
		require.NoError(t, err)
		if origin != "" {
			req.Header.Set("WebHook-Request-Origin", origin)
		}
		return do(t, req)
	}
	resp := options("/hooks/ce", "sender.example.com")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "sender.example.com", resp.Header.Get("WebHook-Allowed-Origin"))
	assert.Equal(t, "120", resp.Header.Get("WebHook-Allowed-Rate"))

	assert.Equal(t, http.StatusMethodNotAllowed, options("/hooks/plain", "sender.example.com").StatusCode)
	assert.Equal(t, http.StatusMethodNotAllowed, options("/hooks/ce", "").StatusCode)
	assert.Equal(t, http.StatusNotFound, options("/hooks/nope", "sender.example.com").StatusCode)
}

func TestPathToken(t *testing.T) {
	src := whsource.Source{
		Name: "url-only", Enabled: true, Connection: "c",
		Auth: whsource.Auth{Mode: whsource.AuthPathToken, Secret: "tok"}, Config: whsource.Config{FlushMaxIntervalMS: 10},
	}
	h := newHarness(t, src)
	assert.Equal(t, http.StatusAccepted, h.post(t, "/hooks/url-only/tok", "application/json", `{}`).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, h.post(t, "/hooks/url-only/bad", "application/json", `{}`).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, h.post(t, "/hooks/url-only", "application/json", `{}`).StatusCode)
}

func TestRateLimit(t *testing.T) {
	src := hmacSource("limited")
	src.Config.RateLimitPerMinute = 1
	src.Config.RateLimitBurst = 1
	h := newHarness(t, src)
	assert.Equal(t, http.StatusAccepted, h.signed(t, "limited", `{"id":1}`).StatusCode)
	resp := h.signed(t, "limited", `{"id":2}`)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "60", resp.Header.Get("Retry-After"))

	// A change of limit replaces the limiter.
	src.Config.RateLimitPerMinute = 600
	src.Config.RateLimitBurst = 0
	h.sources.set(src)
	require.NoError(t, h.r.refresh(context.Background()))
	assert.Equal(t, http.StatusAccepted, h.signed(t, "limited", `{"id":3}`).StatusCode)
}

func TestWriteFailureAnswers503(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	h.objects.fail = errors.New("store down")
	resp := h.signed(t, "esp", `{"id":"x"}`)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "an event not in object storage is never acknowledged")
	assert.Equal(t, "5", resp.Header.Get("Retry-After"))

	h.objects.fail = nil
	h.recorder.markErr = errors.New("db down")
	resp = h.signed(t, "esp", `{"id":"y"}`)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"a segment whose window could not be recorded after the write is not acknowledged")

	fresh := newHarness(t, hmacSource("esp"))
	fresh.recorder.markErr = errors.New("db down")
	assert.Equal(t, http.StatusServiceUnavailable, fresh.signed(t, "esp", `{"id":"z"}`).StatusCode)
	assert.Zero(t, fresh.objects.count(), "the first segment of a window that could not be recorded is not written")
}

// TestWriteFailureIsLoggedAndRejectedWithItsStages: a write that fails is
// answered without the store's error, and logged and kept with it, the
// delivery's events and how long each stage took (#2107).
func TestWriteFailureIsLoggedAndRejectedWithItsStages(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	h.objects.fail = errors.New("bucket k: access denied")
	body := `{"id":"evt-9"}`
	req := h.request(t.Context(), "/hooks/esp", "application/json", body, "X-Sig", sig(body))
	resp, err := testClient.Do(req) // #nosec G704 -- the httptest server this test started
	require.NoError(t, err)
	answer, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.NotContains(t, string(answer), "access denied", "the store's error is never sent to the sender")

	lines := h.logs.lines("webhooks: request not accepted")
	require.Len(t, lines, 1)
	for _, want := range []string{
		"level=WARN", "outcome=write_failed", "source=esp", "replica=host-a:8080",
		"events=1", "event_ids=[evt-9]", "request_hash=" + sha(body), "buffer_ms=", "mark_ms=", "put_ms=", "access denied",
	} {
		assert.Contains(t, lines[0], want)
	}
	require.Len(t, h.r.rejections, 1)
	rej := h.r.rejections[0]
	assert.Equal(t, OutcomeWriteFailed, rej.Outcome)
	assert.Equal(t, "the events could not be written to object storage: writing segment: bucket k: access denied", rej.Reason)
	assert.Equal(t, sha(body), rej.RequestHash)
	assert.Equal(t, []string{"evt-9"}, rej.EventIDs)
}

// TestSenderGone: a sender that disconnects before its segment is written is
// counted sender_gone rather than write_failed, kept with its events, and
// logged twice: when it went, and when the write it left behind finished,
// saying whether the events were stored (#2107).
func TestSenderGone(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	calls := &storedCalls{}
	h.r.SetStored(calls.hook)
	h.objects.mu.Lock()
	h.objects.stall = make(chan struct{})
	h.objects.mu.Unlock()
	body := `{"id":"evt-1"}`
	ctx, cancel := context.WithCancel(context.Background())
	sent := make(chan error, 1)
	go func() {
		_, err := send(h.request(ctx, "/hooks/esp", "application/json", body, "X-Sig", sig(body)))
		sent <- err
	}()
	require.Eventually(t, func() bool { return h.objects.puts.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	require.Error(t, <-sent)
	require.Eventually(t, func() bool { return len(h.logs.lines("webhooks: request not accepted")) == 1 },
		5*time.Second, 5*time.Millisecond)
	gone := h.logs.lines("webhooks: request not accepted")[0]
	for _, want := range []string{"outcome=sender_gone", "event_ids=[evt-1]", "request_hash=" + sha(body), "elapsed_ms="} {
		assert.Contains(t, gone, want)
	}
	assert.Empty(t, h.logs.lines("write for a disconnected sender finished"), "the write has not finished")

	h.objects.mu.Lock()
	close(h.objects.stall)
	h.objects.stall = nil
	h.objects.mu.Unlock()
	require.Eventually(t, func() bool { return len(h.logs.lines("write for a disconnected sender finished")) == 1 },
		5*time.Second, 5*time.Millisecond)
	finished := h.logs.lines("write for a disconnected sender finished")[0]
	assert.Contains(t, finished, "stored=true")
	assert.Contains(t, finished, "put_ms=")
	assert.Len(t, h.objects.events(t), 1, "the write the sender left behind stored its event")
	require.Eventually(t, func() bool { return len(calls.of("esp")) == 1 }, 5*time.Second, 5*time.Millisecond,
		"#2332: events stored after the sender left still owe their runs")
	assert.Equal(t, []string{"evt-1"}, calls.of("esp"))

	h.r.statsMu.Lock()
	rejections := append([]whstore.Rejection(nil), h.r.rejections...)
	h.r.statsMu.Unlock()
	require.Len(t, rejections, 1)
	assert.Equal(t, OutcomeSenderGone, rejections[0].Outcome)
	assert.Contains(t, rejections[0].Reason, "the sender disconnected after ")
	assert.Equal(t, []string{"evt-1"}, rejections[0].EventIDs)
	h.metrics.mu.Lock()
	defer h.metrics.mu.Unlock()
	assert.Equal(t, 1, h.metrics.requests["esp/sender_gone"])
	assert.Zero(t, h.metrics.requests["esp/write_failed"])
}

// TestSenderGoneWhoseWriteFails says so on the second line.
func TestSenderGoneWhoseWriteFails(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	h.objects.mu.Lock()
	h.objects.stall = make(chan struct{})
	h.objects.fail = errors.New("store down")
	h.objects.mu.Unlock()
	body := `{"id":"evt-2"}`
	ctx, cancel := context.WithCancel(context.Background())
	sent := make(chan error, 1)
	go func() {
		_, err := send(h.request(ctx, "/hooks/esp", "application/json", body, "X-Sig", sig(body)))
		sent <- err
	}()
	require.Eventually(t, func() bool { return h.objects.puts.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	require.Error(t, <-sent)
	require.Eventually(t, func() bool { return len(h.logs.lines("webhooks: request not accepted")) == 1 },
		5*time.Second, 5*time.Millisecond)
	h.objects.mu.Lock()
	close(h.objects.stall)
	h.objects.stall = nil
	h.objects.mu.Unlock()
	require.Eventually(t, func() bool { return len(h.logs.lines("write for a disconnected sender finished")) == 1 },
		5*time.Second, 5*time.Millisecond)
	finished := h.logs.lines("write for a disconnected sender finished")[0]
	assert.Contains(t, finished, "stored=false")
	assert.Contains(t, finished, "store down")
}

// TestAcknowledgementLatencyIsRecorded: every delivery answered 202 adds to
// its minute's latency, which the stats flush writes, and a failed write
// keeps it for the next (#2107).
func TestAcknowledgementLatencyIsRecorded(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"1"}`).StatusCode)
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"2"}`).StatusCode)

	h.recorder.latencyErr = errors.New("db down")
	require.Error(t, h.r.flushStats(context.Background()))
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"3"}`).StatusCode)
	h.recorder.latencyErr = nil
	require.NoError(t, h.r.flushStats(context.Background()))

	var total int64
	for _, l := range h.recorder.latency {
		assert.Equal(t, "esp", l.Source)
		for _, n := range l.Buckets {
			total += n
		}
		assert.False(t, l.Slowest.At.IsZero(), "the slowest delivery is kept with its time")
	}
	assert.Equal(t, int64(3), total, "a failed write of latency keeps it for the next")
}

// TestRefusalsAreLoggedAtWarn: a refusal before the body is read into events
// is logged and kept without naming any.
func TestRefusalsAreLoggedAtWarn(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	resp := h.post(t, "/hooks/esp", "application/json", `{"id":"x"}`, "X-Sig", "00")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	lines := h.logs.lines("webhooks: request not accepted")
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "level=WARN")
	assert.Contains(t, lines[0], "outcome=unauthorized")
	assert.NotContains(t, lines[0], "event_ids")
	require.Len(t, h.r.rejections, 1)
	assert.Empty(t, h.r.rejections[0].EventIDs)
	assert.Empty(t, h.r.rejections[0].RequestHash)
}

func sha(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func TestStalledStoreBoundsTheBuffer(t *testing.T) {
	src := hmacSource("burst")
	// Three times the limit is enough to show the bound: the limit's worth
	// held while the store is stalled and the rest refused. A burst far
	// larger only spends sockets, and a machine's ephemeral ports run out
	// across repeated runs.
	const limit = 20
	src.Config.BufferLimit = limit
	h := newHarness(t, src)
	h.objects.stall = make(chan struct{})

	const requests = 3 * limit
	statuses := make(chan reply, requests)
	failures := make(chan error, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Go(func() {
			body := `{"id":"` + string(rune('a'+i%26)) + `"}`
			r, err := send(h.request(context.Background(), "/hooks/burst", "application/json", body, "X-Sig", sig(body)))
			if err != nil {
				failures <- err
				return
			}
			statuses <- r
		})
	}
	// Every request past the limit is answered while the store is stalled.
	// The burst is released and waited for whatever this finds, so no request
	// outlives the test.
	answered := assert.Eventually(t, func() bool { return len(statuses) >= requests-limit }, 5*time.Second, 10*time.Millisecond)
	close(h.objects.stall)
	wg.Wait()
	close(statuses)
	close(failures)
	for err := range failures {
		t.Errorf("a request in the burst failed: %v", err)
	}
	if !answered {
		t.FailNow()
	}

	var accepted, full int
	for resp := range statuses {
		switch resp.StatusCode {
		case http.StatusAccepted:
			accepted++
		case http.StatusServiceUnavailable:
			full++
			assert.NotEmpty(t, resp.Header.Get("Retry-After"))
		default:
			t.Fatalf("unexpected status %d", resp.StatusCode)
		}
	}
	assert.LessOrEqual(t, accepted, limit)
	assert.Equal(t, requests, accepted+full)
	// The buffer's writer records the gauge after it answers, so the value is
	// read under the fake's lock.
	h.metrics.mu.Lock()
	maxBuffer := h.metrics.maxBuffer
	h.metrics.mu.Unlock()
	assert.LessOrEqual(t, maxBuffer, limit, "the buffer never holds more than its limit")
}

func TestStopAnswersWaitingRequests(t *testing.T) {
	src := hmacSource("slow")
	src.Config.FlushMaxIntervalMS = 60_000
	h := newHarness(t, src)
	done := make(chan int, 1)
	go func() { done <- h.signed(t, "slow", `{"id":1}`).StatusCode }()
	require.Eventually(t, func() bool {
		b := h.r.bufferFor("slow")
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.pendingEvents == 1
	}, 2*time.Second, 5*time.Millisecond)
	h.r.Stop()
	assert.Equal(t, http.StatusAccepted, <-done, "stopping writes what is pending")
	assert.Nil(t, h.r.bufferFor("other"), "a stopped receiver takes no more events")

	rec := httptest.NewRecorder()
	h.r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/hooks/slow", strings.NewReader(`{"id":2}`)))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	body := `{"id":2}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/hooks/slow", strings.NewReader(body))
	req.Header.Set("X-Sig", sig(body))
	rec = httptest.NewRecorder()
	h.r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestClosedBufferAdmitsNothing(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	b := newBuffer(h.r, "esp")
	b.drain()
	_, err := b.admit([]whevent.Event{{EventID: "a"}}, 10)
	assert.ErrorIs(t, err, errClosed)

	h.r.mu.Lock()
	h.r.buffers["esp"] = b
	h.r.mu.Unlock()
	resp := h.signed(t, "esp", `{"id":"x"}`)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "5", resp.Header.Get("Retry-After"))
}

func TestFlushByCount(t *testing.T) {
	src := hmacSource("count")
	src.Config.FlushMaxEvents = 2
	src.Config.FlushMaxIntervalMS = 60_000
	src.Config.Split = "$"
	h := newHarness(t, src)
	resp := h.signed(t, "count", `[{"id":1},{"id":2}]`)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode, "reaching max events writes without waiting for the interval")
}

func TestSegmentsPerWindow(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	b := newBuffer(h.r, "esp")
	h1 := time.Date(2026, 9, 24, 10, 59, 59, 0, time.UTC)
	h2 := h1.Add(2 * time.Second)
	_, err := b.write([]*batch{{events: []whevent.Event{{ReceivedAt: h1, EventID: "a"}, {ReceivedAt: h2, EventID: "b"}}}})
	require.NoError(t, err)
	assert.Equal(t, 2, h.objects.count(), "events are filed under the UTC hour they were received in")

	src := hmacSource("fast")
	src.Config.CompactEveryMinutes = 5
	h = newHarness(t, src)
	b = newBuffer(h.r, "fast")
	at := time.Date(2026, 9, 24, 10, 12, 0, 0, time.UTC)
	_, err = b.write([]*batch{{events: []whevent.Event{
		{ReceivedAt: at, EventID: "a"},
		{ReceivedAt: at.Add(2 * time.Minute), EventID: "b"},
		{ReceivedAt: at.Add(4 * time.Minute), EventID: "c"},
	}}})
	require.NoError(t, err)
	keys := make([]string, 0, 2)
	for key := range h.objects.objects {
		keys = append(keys, key[:strings.LastIndex(key, "/")+1])
	}
	sort.Strings(keys)
	assert.Equal(t, []string{
		"webhooks/fast/raw/dt=2026-09-24/hour=10/minute=10/",
		"webhooks/fast/raw/dt=2026-09-24/hour=10/minute=15/",
	}, keys, "a five-minute source files 10:12 and 10:14 under 10:10, and 10:16 under 10:15")
	assert.Equal(t, 2, h.recorder.marks["fast@2026-09-24T10:10:00Z/5m0s"])
	assert.Equal(t, 2, h.recorder.marks["fast@2026-09-24T10:15:00Z/5m0s"])
}

func TestRefreshAndStats(t *testing.T) {
	h := newHarness(t)
	h.sources.err = errors.New("db down")
	assert.Error(t, h.r.refresh(context.Background()))

	bad := hmacSource("bad")
	bad.Config.KeyPath = "$["
	h.sources.err = nil
	h.sources.set(bad, hmacSource("good"))
	require.NoError(t, h.r.refresh(context.Background()))
	_, ok := h.r.lookup("bad")
	assert.False(t, ok, "a source whose path does not parse is not served")
	_, ok = h.r.lookup("good")
	assert.True(t, ok)

	h.r.Reload()
	h.r.Reload()

	h.recorder.countErr = errors.New("db down")
	h.r.count("good", OutcomeAccepted, http.StatusAccepted)
	_ = h.r.flushStats(context.Background())
	h.recorder.countErr = nil
	_ = h.r.flushStats(context.Background())
	assert.Equal(t, int64(1), h.recorder.counts["good/accepted"], "a failed write of counts keeps them for the next")
	assert.Equal(t, int64(1), h.recorder.statuses["good/202"], "the status is kept through the retry")

	for range maxPendingRejections + 5 {
		h.r.reject(delivery{source: "good"}, OutcomeUnauthorized, "x")
	}
	assert.Len(t, h.r.rejections, maxPendingRejections)
}

func TestNilReceiver(_ *testing.T) {
	var r *Receiver
	r.Start(context.Background())
	r.Stop()
	r.Reload()
	New(Config{}).Stop()
}

func TestSplitPath(t *testing.T) {
	for path, want := range map[string][3]any{
		"/hooks/a":     {"a", "", true},
		"/hooks/a/t":   {"a", "t", true},
		"/hooks/a/":    {"a", "", false},
		"/hooks/":      {"", "", false},
		"/other/a":     {"", "", false},
		"/hooks/a/b/c": {"a", "", false},
	} {
		name, token, ok := splitPath(path)
		assert.Equal(t, want, [3]any{name, token, ok}, path)
	}
}

func TestANewSourceIsServedOnItsFirstRequest(t *testing.T) {
	h := newHarness(t)
	h.sources.set(hmacSource("late"))
	resp := h.signed(t, "late", `{"id":"x"}`)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode, "a source created elsewhere is read on a miss, not at the next refresh")

	h.sources.mu.Lock()
	h.sources.err = errors.New("db down")
	h.sources.mu.Unlock()
	assert.Equal(t, http.StatusNotFound, h.signed(t, "missing", `{}`).StatusCode)
	h.sources.mu.Lock()
	h.sources.err = nil
	h.sources.mu.Unlock()
	h.sources.set(hmacSource("late"), hmacSource("again"))
	assert.Equal(t, http.StatusAccepted, h.signed(t, "again", `{"id":"y"}`).StatusCode,
		"a second new source straight after another miss is served on its first request (#2108)")
}

// TestMissesShareSpacedReReads: a burst of requests for names no source has
// is one read of the sources, a later miss waits out the interval and reads
// again, and a source created between the two is served (#2108).
func TestMissesShareSpacedReReads(t *testing.T) {
	sources := &staticSources{}
	r := New(Config{
		Sources: sources, Objects: newObjects(), Recorder: newRecorder(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Refresh: time.Hour, StatsFlush: time.Hour,
		MissRefresh: 200 * time.Millisecond,
	})
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	started := sources.readCount()

	const interval = 200 * time.Millisecond
	var wg sync.WaitGroup
	burst := time.Now()
	for i := range 50 {
		wg.Go(func() {
			_, ok := r.lookupFresh(context.Background(), fmt.Sprintf("invented-%d", i))
			assert.False(t, ok)
		})
	}
	wg.Wait()
	reads := sources.readCount() - started
	assert.LessOrEqual(t, reads, 1+int(time.Since(burst)/interval), "at most one read per interval, whatever the burst")

	sources.set(hmacSource("second"))
	before := sources.readCount()
	_, ok := r.lookupFresh(context.Background(), "second")
	assert.True(t, ok, "a source created after the last read is found by the next")
	assert.Equal(t, before+1, sources.readCount(), "one more read finds it")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok = r.lookupFresh(ctx, "gone-sender")
	assert.False(t, ok, "a request whose sender left does not wait for the read")
}

func TestConcurrentMissesWaitForTheReRead(t *testing.T) {
	h := newHarness(t)
	h.sources.set(hmacSource("fresh"))
	var wg sync.WaitGroup
	codes := make(chan int, 40)
	for range 40 {
		wg.Go(func() {
			codes <- h.signed(t, "fresh", `{"id":"x"}`).StatusCode
		})
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		assert.Equal(t, http.StatusAccepted, code, "a request arriving while the re-read runs waits for it")
	}
}

func TestABatchLargerThanTheBufferIsRefusedOnce(t *testing.T) {
	src := hmacSource("small-buffer")
	src.Config.BufferLimit = 2
	src.Config.Split = "$"
	h := newHarness(t, src)
	body := `[{"id":1},{"id":2},{"id":3}]`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL+"/hooks/small-buffer", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Sig", sig(body))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode,
		"a batch the buffer can never hold is not answered 503, which a sender retries forever")
	assert.Contains(t, string(raw), "buffer_limit of 2")
	assert.Empty(t, resp.Header.Get("Retry-After"))
}

// TestTheNextWindowIsRegisteredAhead: a source that wrote into the current
// window has the next one queued within the lead of its start, half a short
// window's length at most; a quiet or removed source does not (#2107).
func TestTheNextWindowIsRegisteredAhead(t *testing.T) {
	var now time.Time
	fast := hmacSource("fast")
	fast.Config.CompactEveryMinutes = 5
	r := New(Config{
		Sources: &staticSources{list: []whsource.Source{hmacSource("esp"), fast}}, RawWindows: &fakeRawWindows{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now },
	})
	require.NoError(t, r.refresh(context.Background()))
	hour := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	queued := func() []string {
		r.regMu.Lock()
		defer r.regMu.Unlock()
		out := make([]string, 0, len(r.regPending))
		for k := range r.regPending {
			out = append(out, k)
		}
		sort.Strings(out)
		r.regPending = map[string]rawWindow{}
		return out
	}

	now = hour.Add(10 * time.Second)
	r.queueRawWindow("esp", hour)
	r.queueRawWindow("esp", hour.Add(-time.Hour))
	assert.Equal(t, []string{windowKey("esp", hour.Add(-time.Hour)), windowKey("esp", hour)}, queued())
	assert.Equal(t, hour, r.latest["esp"], "an older window never replaces the latest")

	now = hour.Add(30 * time.Minute)
	r.queueNext()
	assert.Empty(t, queued(), "the next window is not near yet")

	now = hour.Add(59 * time.Minute)
	r.queueNext()
	assert.Equal(t, []string{windowKey("esp", hour.Add(time.Hour))}, queued(), "a minute ahead of an hourly window")

	now = hour.Add(61 * time.Minute)
	r.queueNext()
	assert.Empty(t, queued(), "a source that wrote nothing in the current window has gone quiet")

	five := hour.Add(10 * time.Minute)
	r.queueRawWindow("fast", five)
	_ = queued()
	now = five.Add(3*time.Minute + 29*time.Second)
	r.queueNext()
	assert.Empty(t, queued(), "a five-minute window is registered a minute ahead, not sooner")
	now = five.Add(4*time.Minute + 1*time.Second)
	r.queueNext()
	assert.Equal(t, []string{windowKey("fast", five.Add(5*time.Minute))}, queued())

	r.latest["gone"] = hour
	r.queueNext()
	_, kept := r.latest["gone"]
	assert.False(t, kept, "a source no longer served is forgotten")
}

// A window queued again by a write that landed while its registration was
// finishing is not registered a second time.
func TestARegisteredWindowQueuedAgainIsNotRegisteredAgain(t *testing.T) {
	h := newHarness(t, hmacSource("esp"))
	start := time.Now().UTC().Truncate(time.Hour)
	key := windowKey("esp", start)
	h.r.rawWindows.Store(key, start)
	h.r.regMu.Lock()
	h.r.regPending[key] = rawWindow{source: "esp", start: start}
	h.r.regMu.Unlock()
	require.NoError(t, h.r.registerPending(context.Background()))
	h.raw.mu.Lock()
	defer h.raw.mu.Unlock()
	assert.Zero(t, h.raw.calls, "the window is registered already")
	h.r.regMu.Lock()
	defer h.r.regMu.Unlock()
	assert.NotContains(t, h.r.regPending, key, "and is no longer owed a registration")
}
