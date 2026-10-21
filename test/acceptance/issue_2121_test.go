//go:build integration

package acceptance

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Acceptance for #2121: a webhook receiver with a local spool answers a
// request once its events are synced to its own disk, so a stalled object
// store does not delay the answer; a replica killed mid-stream loses nothing
// it acknowledged; a full spool asks senders to retry; and the spool's depth
// is reported as it rises and falls.
//
// The two replicas dev/start.sh runs have no spool. Each criterion starts a
// replica of its own -- the platform binary, on the dev stack's database and
// object store, with DEV_WEBHOOK_SPOOL_DIR set (dev/platform.yaml) -- and
// reaches the object store through a proxy the test can stop forwarding, so
// it can stall that replica's object store and kill it without touching the
// others. Every request to /hooks/{source} is raw HTTP, sent as JSON and as a
// form post.

const issue2121Secret = "whsec_2121"

// issue2121Bin builds the platform binary once for the criteria.
var issue2121Bin = sync.OnceValues(func() (string, error) {
	root, err := filepath.Abs("../..")
	if err != nil {
		return "", err
	}
	out := filepath.Join(root, "build", "2121", "bin", "mcp-data-platform")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/mcp-data-platform")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the platform: %w\n%s", err, b)
	}
	return out, nil
})

// issue2121Proxy forwards TCP to the object store until it is paused. Pausing
// cuts every connection open through it and holds new ones unanswered, which
// is an object store that does not answer; resuming forwards them again.
type issue2121Proxy struct {
	ln     net.Listener
	target string

	mu      sync.Mutex
	paused  bool
	resumed chan struct{}
	conns   map[net.Conn]struct{}
}

func issue2121NewProxy(t *testing.T, target string) *issue2121Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &issue2121Proxy{ln: ln, target: target, resumed: make(chan struct{}), conns: map[net.Conn]struct{}{}}
	close(p.resumed)
	go p.serve()
	t.Cleanup(func() {
		p.resume()
		_ = ln.Close()
	})
	return p
}

func (p *issue2121Proxy) port() string {
	return strconv.Itoa(p.ln.Addr().(*net.TCPAddr).Port)
}

func (p *issue2121Proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(c)
	}
}

func (p *issue2121Proxy) handle(c net.Conn) {
	p.mu.Lock()
	for p.paused {
		wait := p.resumed
		p.mu.Unlock()
		<-wait
		p.mu.Lock()
	}
	p.mu.Unlock()
	up, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = c.Close()
		return
	}
	p.mu.Lock()
	p.conns[c], p.conns[up] = struct{}{}, struct{}{}
	p.mu.Unlock()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
	_ = c.Close()
	_ = up.Close()
	p.mu.Lock()
	delete(p.conns, c)
	delete(p.conns, up)
	p.mu.Unlock()
}

func (p *issue2121Proxy) pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused {
		return
	}
	p.paused = true
	p.resumed = make(chan struct{})
	for c := range p.conns {
		_ = c.Close()
	}
}

func (p *issue2121Proxy) resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.paused {
		return
	}
	p.paused = false
	close(p.resumed)
}

// issue2121Store is the object store as a replica the test starts reaches
// it: both of its endpoints, plain and TLS, each behind a proxy, paused and
// resumed together.
type issue2121Store struct {
	plain, tls *issue2121Proxy
}

func issue2121NewStore(t *testing.T) *issue2121Store {
	t.Helper()
	return &issue2121Store{
		plain: issue2121NewProxy(t, "127.0.0.1:"+issue2121Env("DEV_S3_PORT", "9000")),
		tls:   issue2121NewProxy(t, "127.0.0.1:"+issue2121Env("DEV_S3_TLS_PORT", "9443")),
	}
}

func (s *issue2121Store) pause()  { s.plain.pause(); s.tls.pause() }
func (s *issue2121Store) resume() { s.plain.resume(); s.tls.resume() }

// issue2121Replica is one platform process with a spool.
type issue2121Replica struct {
	cmd     *exec.Cmd
	env     []string
	base    string
	metrics string
	name    string
	log     string
}

// issue2121FreePort is a port nothing listens on.
func issue2121FreePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck // released for the replica
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// issue2121Start starts a replica with a spool in dir, reaching the object
// store through proxy, and waits for it to answer. extra is added to its
// environment.
func issue2121Start(t *testing.T, dir string, store *issue2121Store, extra ...string) *issue2121Replica {
	t.Helper()
	bin, err := issue2121Bin()
	if err != nil {
		t.Fatal(err)
	}
	api, metrics := issue2121FreePort(t), issue2121FreePort(t)
	env := append(os.Environ(),
		"DEV_API_PORT="+api, "OTEL_METRICS_ADDR=127.0.0.1:"+metrics, "DEV_S3_PORT="+store.plain.port(), "DEV_S3_TLS_PORT="+store.tls.port(),
		"DEV_WEBHOOK_SPOOL_DIR="+dir,
	)
	// What dev/start.sh gives the replicas it runs and does not record in
	// dev/.dev-ports.env: the repository's .env (the field-encryption key a
	// source's secret is stored under), the local Trino it points the
	// replicas at when it started one, and the CA bundle of the object
	// store's TLS endpoint.
	root, _ := filepath.Abs("../..")
	env = append(env, issue2121DotEnv(filepath.Join(root, ".env"))...)
	env = append(env,
		"TRINO_ENABLED=true", "TRINO_HOST=localhost", "TRINO_PORT=9283", "TRINO_USER=dev", "TRINO_PASSWORD=",
		"TRINO_SSL=false", "TRINO_SSL_VERIFY=false",
		"AWS_CA_BUNDLE="+filepath.Join(root, "dev", ".tls", "seaweedfs", "public.crt"),
	)
	env = append(env, extra...)
	r := &issue2121Replica{env: env, base: "http://127.0.0.1:" + api, metrics: "http://127.0.0.1:" + metrics + "/metrics"}
	r.start(t, bin)
	t.Cleanup(r.stop)
	return r
}

func (r *issue2121Replica) start(t *testing.T, bin string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	r.log = filepath.Join(root, "build", "2121", "replica-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".log")
	logFile, err := os.Create(r.log)
	if err != nil {
		t.Fatal(err)
	}
	r.cmd = exec.Command(bin, "--config", filepath.Join(root, "dev", "platform.yaml"))
	r.cmd.Dir = root
	r.cmd.Env = r.env
	r.cmd.Stdout, r.cmd.Stderr = logFile, logFile
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = r.cmd.Wait(); _ = logFile.Close() }()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		res, err := http.Get(r.base + "/healthz") // #nosec G107 -- the replica this test started
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				r.name = res.Header.Get(instanceHeader)
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("the spooling replica did not answer within two minutes; its log is %s:\n%s", r.log, issue2121Tail(r.log))
}

// kill ends the replica with SIGKILL: nothing is sealed, closed or drained.
func (r *issue2121Replica) kill() {
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_, _ = r.cmd.Process.Wait()
	}
	r.cmd = nil
}

// stop ends the replica as a rollout does.
func (r *issue2121Replica) stop() {
	if r.cmd == nil || r.cmd.Process == nil {
		return
	}
	_ = r.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = r.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		_ = r.cmd.Process.Kill()
	}
	r.cmd = nil
}

// issue2121Tail is the end of a log.
func issue2121Tail(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer f.Close() //nolint:errcheck // read-only
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}

// post sends one signed event to the replica in a form, and returns the
// answer's status, Retry-After and body, or the error a sender sees.
func (r *issue2121Replica) post(source, form, id string, timeout time.Duration) (int, string, string, error) {
	f := issue1870Forms[0]
	if form == "form" {
		f = issue1870Forms[1]
	}
	body := f.encode(`{"id":"` + id + `","type":"order.created"}`)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/hooks/"+source, strings.NewReader(string(body)))
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Content-Type", f.contentType)
	req.Header.Set("X-Signature", issue1870Sign(issue2121Secret, body))
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the replica this test started
	if err != nil {
		return 0, "", "", err
	}
	defer res.Body.Close() //nolint:errcheck // read below
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Retry-After"), string(raw), nil
}

// gauge reads a metric with no source label off the replica's metrics.
func (r *issue2121Replica) gauge(name string) (float64, bool) {
	res, err := http.Get(r.metrics) // #nosec G107 -- the replica this test started
	if err != nil {
		return 0, false
	}
	defer res.Body.Close() //nolint:errcheck // read below
	raw, _ := io.ReadAll(res.Body)
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, name+" ") && !strings.HasPrefix(line, name+"{") {
			continue
		}
		fields := strings.Fields(line)
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

// issue2121Source creates a source over the main stack's admin API.
func issue2121Source(t *testing.T, c *client, prefix string) string {
	t.Helper()
	name := issue1870Name(prefix)
	issue1870Create(t, c, issue1870HMAC(name, issue2121Secret, map[string]any{"event_id_path": "$.id", "event_type_path": "$.type"}))
	return name
}

// issue2121AwaitExactlyOnce waits until every id is in the source's table
// exactly once, and checks the table holds no id twice.
func issue2121AwaitExactlyOnce(t *testing.T, c *client, source string, ids []string) {
	t.Helper()
	if len(ids) == 0 {
		t.Fatal("no event was acknowledged, so nothing can be checked")
	}
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, "'"+id+"'")
	}
	in := "event_id IN (" + strings.Join(quoted, ",") + ")"
	issue1870AwaitRows(t, c, source, in, len(ids))
	distinct := issue1870Count(t, c, "SELECT count(DISTINCT event_id) FROM "+issue1870Table(source)+" WHERE "+in)
	if distinct != len(ids) {
		t.Fatalf("%d acknowledged events are in the table as %d distinct ids", len(ids), distinct)
	}
	all := issue1870Count(t, c, "SELECT count(*) FROM "+issue1870Table(source))
	unique := issue1870Count(t, c, "SELECT count(DISTINCT event_id) FROM "+issue1870Table(source))
	if all != unique {
		t.Fatalf("the source's table holds %d rows for %d event ids: an event landed twice", all, unique)
	}
}

// issue2121Spool is the replica's line in the status route's spools.
func issue2121Spool(t *testing.T, c *client, replica string) map[string]any {
	t.Helper()
	status, ov := c.rest(http.MethodGet, "/api/v1/admin/webhooks/status", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("status route answered %d: %v", status, ov)
	}
	list, _ := ov["spools"].([]any)
	for _, s := range list {
		line, _ := s.(map[string]any)
		if line["replica"] == replica {
			return line
		}
	}
	return nil
}

// TestIssue2121_AStalledObjectStoreDoesNotDelayTheAnswer: with the replica's
// object store not answering, every request in both forms is answered 202
// within local-disk latency; the spool's depth rises on the metrics and the
// status route, and once the store answers again every event lands exactly
// once and the depth falls to zero.
func TestIssue2121_AStalledObjectStoreDoesNotDelayTheAnswer(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	name := issue2121Source(t, c, "acc2121-stall")
	proxy := issue2121NewStore(t)
	root, _ := filepath.Abs("../..")
	r := issue2121Start(t, filepath.Join(root, "build", "2121", "spool-stall-"+strconv.FormatInt(time.Now().UnixNano(), 36)), proxy)

	proxy.pause()
	var ids []string
	for i := range 6 {
		form := issue1870Forms[i%2].name
		id := fmt.Sprintf("stall-%s-%d", form, i)
		began := time.Now()
		status, _, body, err := r.post(name, form, id, 10*time.Second)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("%s delivery %s with the object store stalled: %d %s %v", form, id, status, body, err)
		}
		if took := time.Since(began); took > 2*time.Second {
			t.Fatalf("%s delivery %s waited %v for its answer: the answer waited on the stalled store", form, id, took)
		}
		ids = append(ids, id)
	}

	waitFor(t, "the spool's depth on the metrics and the status route", func() (bool, string) {
		events, ok := r.gauge("webhook_spool_events")
		line := issue2121Spool(t, c, r.name)
		return ok && events >= 6 && line != nil && line["events"].(float64) >= 6,
			fmt.Sprintf("webhook_spool_events %v (%v), status line %v", events, ok, line)
	})

	proxy.resume()
	issue2121AwaitExactlyOnce(t, c, name, ids)
	waitFor(t, "the spool drained to zero", func() (bool, string) {
		events, ok := r.gauge("webhook_spool_events")
		line := issue2121Spool(t, c, r.name)
		return ok && events == 0 && line != nil && line["events"] == float64(0) && line["drained_events"].(float64) >= 6,
			fmt.Sprintf("webhook_spool_events %v (%v), status line %v", events, ok, line)
	})
}

// TestIssue2121_AKilledReplicaLosesNothingItAcknowledged: requests stream to
// a replica whose object store is stalled, and the replica is killed with
// SIGKILL mid-stream. Restarted on the same spool, it drains what it held:
// every event it acknowledged lands, and none lands twice.
func TestIssue2121_AKilledReplicaLosesNothingItAcknowledged(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	name := issue2121Source(t, c, "acc2121-kill")
	proxy := issue2121NewStore(t)
	root, _ := filepath.Abs("../..")
	dir := filepath.Join(root, "build", "2121", "spool-kill-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	r := issue2121Start(t, dir, proxy)
	proxy.pause()

	var (
		mu    sync.Mutex
		acked []string
	)
	stream := make(chan struct{})
	go func() {
		defer close(stream)
		for i := 0; i < 200; i++ {
			form := issue1870Forms[i%2].name
			id := fmt.Sprintf("kill-%s-%d", form, i)
			status, _, _, err := r.post(name, form, id, 10*time.Second)
			if err != nil {
				return // the replica is gone
			}
			if status == http.StatusAccepted {
				mu.Lock()
				acked = append(acked, id)
				mu.Unlock()
			}
		}
	}()
	waitFor(t, "twenty acknowledged events before the kill", func() (bool, string) {
		mu.Lock()
		defer mu.Unlock()
		return len(acked) >= 20, fmt.Sprintf("%d acknowledged", len(acked))
	})
	r.kill()
	<-stream
	mu.Lock()
	kept := append([]string(nil), acked...)
	mu.Unlock()

	proxy.resume()
	restarted := issue2121Start(t, dir, proxy)
	issue2121AwaitExactlyOnce(t, c, name, kept)
	waitFor(t, "the restarted replica's spool drained", func() (bool, string) {
		line := issue2121Spool(t, c, restarted.name)
		return line != nil && line["events"] == float64(0), fmt.Sprintf("status line %v", line)
	})
}

// TestIssue2121_AFullSpoolAsksSendersToRetry: a replica whose spool is
// bounded to a few requests, with its object store stalled, answers 503 with
// Retry-After once the bound is reached, counts it spool_full, stores nothing
// for the refused request, and drops nothing it acknowledged.
func TestIssue2121_AFullSpoolAsksSendersToRetry(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	name := issue2121Source(t, c, "acc2121-full")
	proxy := issue2121NewStore(t)
	root, _ := filepath.Abs("../..")
	r := issue2121Start(t, filepath.Join(root, "build", "2121", "spool-full-"+strconv.FormatInt(time.Now().UnixNano(), 36)),
		proxy, "DEV_WEBHOOK_SPOOL_MAX_BYTES=4000")
	proxy.pause()

	var acked []string
	refused := ""
	for i := 0; i < 60 && refused == ""; i++ {
		form := issue1870Forms[i%2].name
		id := fmt.Sprintf("full-%s-%d", form, i)
		status, retry, body, err := r.post(name, form, id, 10*time.Second)
		if err != nil {
			t.Fatalf("delivery %s: %v", id, err)
		}
		switch status {
		case http.StatusAccepted:
			acked = append(acked, id)
		case http.StatusServiceUnavailable:
			if retry == "" || !strings.Contains(body, "spool is full") {
				t.Fatalf("a full spool answered 503 with Retry-After %q and body %s", retry, body)
			}
			refused = id
		default:
			t.Fatalf("delivery %s answered %d: %s", id, status, body)
		}
	}
	if refused == "" {
		t.Fatalf("%d deliveries were acknowledged and none refused: the spool's bound was not applied", len(acked))
	}

	waitFor(t, "the refusal counted spool_full", func() (bool, string) {
		status, ov := c.rest(http.MethodGet, "/api/v1/admin/webhooks/status?source="+name, http.NoBody)
		if status != http.StatusOK {
			return false, fmt.Sprintf("status %d", status)
		}
		line := issue2107SourceLine(ov, name)
		hour, _ := line["last_hour"].(map[string]any)
		return hour["spool_full"] != nil && hour["spool_full"].(float64) >= 1, fmt.Sprintf("last_hour %v", hour)
	})

	proxy.resume()
	issue2121AwaitExactlyOnce(t, c, name, acked)
	if n := issue1870Rows(t, c, name, "event_id = '"+refused+"'"); n != 0 {
		t.Fatalf("the refused delivery %s is in the table %d times", refused, n)
	}
}

// issue2121Env is an environment value, or def.
func issue2121Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// issue2121DotEnv reads KEY=VALUE lines from a .env file, as dev/start.sh
// sources it; a missing file is none.
func issue2121DotEnv(path string) []string {
	data, err := os.ReadFile(path) // #nosec G304 -- the repository's own .env
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" || strings.HasPrefix(k, "#") {
			continue
		}
		out = append(out, k+"="+strings.Trim(v, `"'`))
	}
	return out
}
