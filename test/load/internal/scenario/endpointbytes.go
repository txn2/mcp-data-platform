package scenario

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/harness"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
)

// The two ways one view of the page's data endpoint is fetched (#2655): with
// no compression accepted, the bytes before, and accepting brotli and gzip,
// the bytes after.
const (
	bytesOpIdentity   = "endpoint_view_identity"
	bytesOpCompressed = "endpoint_view_compressed"
)

// endpointBytesHandler answers a schedule of about 50 KB of JSON, the shape of
// the page data endpoint #2655 was measured on.
const endpointBytesHandler = `def handle(req):
    """Answers the day's schedule."""
    return {"json": {"slots": [{"time": "%02d:%02d" % (9 + i // 60, i % 60), "room": "Hall " + "ABCD"[i % 4],
        "talk": "Session " + str(i), "speaker": "Speaker " + str(i % 37), "track": "Track " + str(i % 5)} for i in range(400)]}}

def main():
    """Drafts one request."""
    platform.result(handle({"params": {}, "caller": None}))
`

// endpointBytes records the bytes each view of a JSON endpoint transfers with
// and without compression (#2655): the load harness's before and after.
type endpointBytes struct {
	path                 string
	identity, compressed atomic.Int64
	views                atomic.Int64
}

func (*endpointBytes) Name() string { return "endpoint-bytes" }
func (*endpointBytes) Description() string {
	return "a JSON endpoint fetched with and without compression, recording the bytes per view"
}

func (*endpointBytes) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 8, Duration: 20 * time.Second, Warmup: 5 * time.Second, RatePerSec: 50}
}

// Setup saves the handler and binds it.
func (s *endpointBytes) Setup(ctx context.Context, env *harness.Env) error {
	session, err := env.MCP.Connect(ctx)
	if err != nil {
		return fmt.Errorf("endpoint-bytes setup: %w", err)
	}
	defer func() { _ = session.Close() }()
	name := "load-bytes-" + strconv.FormatInt(time.Now().Unix(), 36)
	if err := saveHandler(ctx, session, name, endpointBytesHandler); err != nil {
		return err
	}
	if _, err := call(ctx, session, "manage_script", map[string]any{
		"command": "handler_set", "kind": "endpoint", "name": name, "endpoint": name, "methods": []string{"GET"}, "path": "/",
		"auth_mode": "authenticated", "callers": []string{"*"}, "rate_limit": 100000, "caller_rate_limit": 100000,
	}); err != nil {
		return err
	}
	s.path = "/endpoints/" + name + "/"
	return nil
}

func (*endpointBytes) Teardown(context.Context, *harness.Env) {}

func (s *endpointBytes) NewWorker(_ context.Context, env *harness.Env, _ int) (harness.Worker, error) {
	return &bytesWorker{s: s, url: env.Target.URL(s.path), env: env}, nil
}

// Assess holds every request to success and states the bytes per view before
// and after, passing when compression sends fewer.
func (s *endpointBytes) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	out := []report.Assertion{errorRateAssertion(rep, bytesOpIdentity, 0), errorRateAssertion(rep, bytesOpCompressed, 0)}
	return append(out, bytesAssertion(s.views.Load(), s.identity.Load(), s.compressed.Load()))
}

// bytesAssertion is the bytes per view before and after.
func bytesAssertion(views, identity, compressed int64) report.Assertion {
	const name = "bytes-per-view"
	if views == 0 || identity == 0 {
		return report.Assertion{Name: name, Message: "no views recorded"}
	}
	before, after := identity/views, compressed/views
	return report.Assertion{
		Name: name, Passed: after < before,
		Message: fmt.Sprintf("%d bytes per view uncompressed, %d compressed (%.0f%%) over %d views", before, after, 100*float64(after)/float64(before), views),
	}
}

// bytesWorker fetches one view both ways per iteration.
type bytesWorker struct {
	s   *endpointBytes
	url string
	env *harness.Env
}

// Iterate fetches the view uncompressed and then compressed, counting the
// bytes on the wire: an explicit Accept-Encoding leaves the body as sent.
func (w *bytesWorker) Iterate(ctx context.Context) {
	idOK := w.env.Timed(bytesOpIdentity, func() error { return w.fetch(ctx, "identity", &w.s.identity) }) == nil
	brOK := w.env.Timed(bytesOpCompressed, func() error { return w.fetch(ctx, "br, gzip", &w.s.compressed) }) == nil
	if idOK && brOK {
		w.s.views.Add(1)
	}
}

func (w *bytesWorker) fetch(ctx context.Context, accept string, total *atomic.Int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.url, http.NoBody)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Accept-Encoding", accept)
	res, err := w.env.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("sending the request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	n, err := io.Copy(io.Discard, res.Body)
	if err != nil {
		return fmt.Errorf("reading the response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", errEndpointStatus, res.StatusCode)
	}
	total.Add(n)
	return nil
}

func (*bytesWorker) Close() {}
