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
	markErr    error
	countErr   error
}

func newRecorder() *fakeRecorder {
	return &fakeRecorder{marks: map[string]int{}, counts: map[string]int64{}}
}

func (f *fakeRecorder) MarkSegment(_ context.Context, source string, start time.Time, length time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
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
	}
	return nil
}

func (f *fakeRecorder) RecordRejections(_ context.Context, r []whstore.Rejection) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejections = append(f.rejections, r...)
	return nil
}

// fakeMetrics records what was reported.
type fakeMetrics struct {
	mu        sync.Mutex
	requests  map[string]int
	events    int
	segments  int
	acks      int
	maxBuffer int
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

// fakeRawWindows records the windows registered, and can fail.
type fakeRawWindows struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeRawWindows) EnsureRawWindow(context.Context, whsource.Source, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

type staticSources struct {
	mu   sync.Mutex
	list []whsource.Source
	err  error
}

func (s *staticSources) List(context.Context) ([]whsource.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list, s.err
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
		raw:      &fakeRawWindows{},
	}
	h.r = New(Config{
		Sources: h.sources, Objects: h.objects, Bucket: "managed-resources", Recorder: h.recorder, RawWindows: h.raw,
		Metrics: h.metrics, Replica: "host-a:8080", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		WriteTimeout: 2 * time.Second, Refresh: time.Hour, StatsFlush: time.Hour,
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

// do sends req and closes the response body before returning its status and headers.
func do(t *testing.T, req *http.Request) reply {
	t.Helper()
	resp, err := http.DefaultClient.Do(req) // #nosec G704 -- the httptest server this test started
	require.NoError(t, err)
	_ = resp.Body.Close()
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}
}

func (h *harness) post(t *testing.T, path, contentType, body string, hdr ...string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return do(t, req)
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
	h.raw.err = errors.New("trino down")
	assert.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"1"}`).StatusCode,
		"a query engine that is down never stops events being stored")
	h.raw.err = nil
	assert.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"2"}`).StatusCode)
	assert.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"id":"3"}`).StatusCode)
	h.raw.mu.Lock()
	defer h.raw.mu.Unlock()
	assert.Equal(t, 2, h.raw.calls, "registered until it succeeds, then not again")
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
		{"form without payload", func() reply {
			return h.post(t, "/hooks/esp", "application/x-www-form-urlencoded", `a=b`, "X-Sig", sig(`a=b`))
		}, http.StatusBadRequest, "esp/invalid_body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.status, tt.resp().StatusCode)
		})
	}
	assert.Zero(t, h.objects.count(), "no refused request writes an object")
	assert.Zero(t, h.objects.puts.Load())

	h.r.flushStats(context.Background())
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
	h.r.flushStats(context.Background())
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
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Zero(t, h.objects.count(), "a segment whose hour could not be recorded is not written")
}

func TestStalledStoreBoundsTheBuffer(t *testing.T) {
	src := hmacSource("burst")
	src.Config.BufferLimit = 100
	h := newHarness(t, src)
	h.objects.stall = make(chan struct{})

	const requests = 400
	statuses := make(chan reply, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Go(func() {
			statuses <- h.signed(t, "burst", `{"id":"`+string(rune('a'+i%26))+`"}`)
		})
	}
	// Every request past the limit is answered while the store is stalled.
	require.Eventually(t, func() bool { return len(statuses) >= requests-100 }, 5*time.Second, 10*time.Millisecond)
	close(h.objects.stall)
	wg.Wait()
	close(statuses)

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
	assert.LessOrEqual(t, accepted, 100)
	assert.Equal(t, requests, accepted+full)
	assert.LessOrEqual(t, h.metrics.maxBuffer, 100, "the buffer never holds more than its limit")
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
	err := b.write([]*batch{{events: []whevent.Event{{ReceivedAt: h1, EventID: "a"}, {ReceivedAt: h2, EventID: "b"}}}})
	require.NoError(t, err)
	assert.Equal(t, 2, h.objects.count(), "events are filed under the UTC hour they were received in")

	src := hmacSource("fast")
	src.Config.CompactEveryMinutes = 5
	h = newHarness(t, src)
	b = newBuffer(h.r, "fast")
	at := time.Date(2026, 9, 24, 10, 12, 0, 0, time.UTC)
	err = b.write([]*batch{{events: []whevent.Event{
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
	h.r.count("good", OutcomeAccepted)
	h.r.flushStats(context.Background())
	h.recorder.countErr = nil
	h.r.flushStats(context.Background())
	assert.Equal(t, int64(1), h.recorder.counts["good/accepted"], "a failed write of counts keeps them for the next")

	for range maxPendingRejections + 5 {
		h.r.reject("good", OutcomeUnauthorized, "x")
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
	h.r.lastMiss = time.Time{}
	assert.Equal(t, http.StatusNotFound, h.signed(t, "missing", `{}`).StatusCode)
	h.sources.mu.Lock()
	h.sources.err = nil
	h.sources.mu.Unlock()
	h.sources.set(hmacSource("late"), hmacSource("again"))
	assert.Equal(t, http.StatusNotFound, h.signed(t, "again", `{"id":"y"}`).StatusCode,
		"a second miss within a second does not read the sources again")
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
