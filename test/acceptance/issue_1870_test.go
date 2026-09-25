//go:build integration

package acceptance

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Acceptance for #1870: inbound webhook sources.
//
// Every request to /hooks/{source} is sent as raw HTTP bytes with the
// Content-Type a sender declares. Wire forms: application/json, and
// application/x-www-form-urlencoded carrying the JSON in a `payload` field;
// criteria 1 to 7 send both. The admin API takes one JSON object per route.
// The platform's tools are called through the MCP client: trino_query and
// trino_execute with string `connection`, `sql` and `purpose`; search with
// string `intent` and a `sources` list; fetch with string `reference`.
//
// The criteria run against the local stack: both platform replicas behind the
// proxy, Trino, the MinIO store the managed resources live in, and Postgres.

const (
	issue1870Conn    = "acme-scratch-resources"
	issue1870Schema  = "scratch_resources.uploads"
	issue1870Purpose = "Acceptance for #1870: inbound webhook sources."
	issue1870Store   = "acme-dev-minio"
)

// issue1870Name is a source name no other run has used.
func issue1870Name(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 36)
}

// issue1870Create creates a source over the admin API and deletes it when the
// test ends.
func issue1870Create(t *testing.T, c *client, body map[string]any) {
	t.Helper()
	status, out := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, body))
	if status != http.StatusCreated {
		t.Fatalf("creating webhook source %v: status %d: %v", body["name"], status, out)
	}
	name, _ := body["name"].(string)
	t.Cleanup(func() {
		if st, del := c.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+name, http.NoBody); st != http.StatusNoContent {
			t.Logf("deleting webhook source %s: status %d: %v", name, st, del)
		}
	})
}

// issue1870HMAC is an HMAC source's settings with the given config.
func issue1870HMAC(name, secret string, config map[string]any) map[string]any {
	return map[string]any{
		"name": name, "connection": issue1870Conn,
		"auth":   map[string]any{"mode": "hmac", "secret": secret, "signature_header": "X-Signature", "prefix": "sha256="},
		"config": config,
	}
}

// issue1870Sign is what a sender puts in X-Signature.
func issue1870Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// issue1870Form is the same JSON as a form post carries it.
func issue1870Form(jsonText string) []byte {
	return []byte(url.Values{"payload": {jsonText}}.Encode())
}

// issue1870Forms are the two ways a sender posts: the JSON as the body, and
// the JSON in a form's payload field.
var issue1870Forms = []struct {
	name        string
	contentType string
	encode      func(string) []byte
}{
	{"json", "application/json", func(s string) []byte { return []byte(s) }},
	{"form", "application/x-www-form-urlencoded", issue1870Form},
}

// issue1870Client posts to the receiver. Its timeout outlasts the receiver's
// 30-second write timeout, so a stalled store is answered by the platform
// rather than by the client giving up.
var issue1870Client = &http.Client{Timeout: 90 * time.Second}

// issue1870Post sends raw bytes to /hooks/{path} at base.
func issue1870Post(t *testing.T, base, path, contentType string, body []byte, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/hooks/"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := issue1870Client.Do(req)
	if err != nil {
		t.Fatalf("POST /hooks/%s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	raw, _ := io.ReadAll(res.Body)
	return res, string(raw)
}

// issue1870Signed posts body to an HMAC source, signed with secret.
func issue1870Signed(t *testing.T, source, secret, contentType string, body []byte) (*http.Response, string) {
	t.Helper()
	return issue1870Post(t, baseURL(), source, contentType, body, map[string]string{"X-Signature": issue1870Sign(secret, body)})
}

// issue1870Count runs a count through trino_query, as a reader does.
func issue1870Count(t *testing.T, c *client, sqlText string) int {
	t.Helper()
	got := c.call("trino_query", map[string]any{"connection": issue1870Conn, "purpose": issue1870Purpose, "sql": sqlText})
	rows, _ := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the count returned %d rows: %v", len(rows), got)
	}
	row, _ := rows[0].(map[string]any)
	for _, v := range row {
		if n, ok := v.(float64); ok {
			return int(n)
		}
	}
	t.Fatalf("the count returned no number: %v", row)
	return 0
}

// issue1870Table is a source's view, qualified.
func issue1870Table(source string) string {
	return issue1870Schema + "." + "webhook_" + strings.ReplaceAll(source, "-", "_")
}

// issue1870Rows is how many events of a source the view returns.
func issue1870Rows(t *testing.T, c *client, source, where string) int {
	t.Helper()
	sqlText := "SELECT count(*) FROM " + issue1870Table(source)
	if where != "" {
		sqlText += " WHERE " + where
	}
	return issue1870Count(t, c, sqlText)
}

// issue1870Docker pauses or unpauses the store the managed resources live in.
// Pausing freezes the process with its sockets open, so a write to it stalls
// rather than failing fast: the store is there and does not answer.
func issue1870Docker(t *testing.T, action string) {
	t.Helper()
	container := os.Getenv("ISSUE_1870_STORE_CONTAINER")
	if container == "" {
		container = issue1870Store
	}
	if out, err := exec.Command("docker", action, container).CombinedOutput(); err != nil {
		t.Fatalf("docker %s %s: %v\n%s", action, container, err, out)
	}
}

// issue1870Pause stalls the store until the returned function is called, and
// in any case when the test ends.
func issue1870Pause(t *testing.T) func() {
	t.Helper()
	issue1870Docker(t, "pause")
	var once sync.Once
	resume := func() { once.Do(func() { issue1870Docker(t, "unpause") }) }
	t.Cleanup(resume)
	return resume
}

// TestIssue1870_AnHMACSourceAcceptsASignedRequestAndRefusesAForgedOne is
// criteria 1 and 17: an administrator creates a source, a signed request is
// acknowledged, and a request whose signature is wrong is refused before
// anything is stored.
func TestIssue1870_AnHMACSourceAcceptsASignedRequestAndRefusesAForgedOne(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-hmac")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_1870", map[string]any{"event_id_path": "$.id"}))

	for _, f := range issue1870Forms {
		t.Run(f.name, func(t *testing.T) {
			forged := f.encode(`{"id":"forged-` + f.name + `"}`)
			for range 5 {
				res, body := issue1870Post(t, baseURL(), name, f.contentType, forged,
					map[string]string{"X-Signature": issue1870Sign("not-the-secret", forged)})
				if res.StatusCode != http.StatusUnauthorized {
					t.Fatalf("a forged request was answered %d, want 401: %s", res.StatusCode, body)
				}
			}
			if n := issue1870Rows(t, c, name, "event_id = 'forged-"+f.name+"'"); n != 0 {
				t.Fatalf("a refused request stored %d events", n)
			}

			good := f.encode(`{"id":"signed-` + f.name + `"}`)
			res, body := issue1870Signed(t, name, "whsec_1870", f.contentType, good)
			if res.StatusCode != http.StatusAccepted {
				t.Fatalf("a signed request was answered %d, want 202: %s", res.StatusCode, body)
			}
			if n := issue1870Rows(t, c, name, "event_id = 'signed-"+f.name+"'"); n != 1 {
				t.Fatalf("the acknowledged event is returned %d times, want 1", n)
			}
		})
	}
	if status, list := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources", http.NoBody); status != http.StatusOK ||
		!strings.Contains(fmt.Sprint(list), name) {
		t.Fatalf("the admin listing does not show the source: %d %v", status, list)
	}
	if n := issue1870Rows(t, c, name, ""); n != 2 {
		t.Fatalf("the source holds %d events, want exactly the 2 acknowledged", n)
	}
}

// TestIssue1870_ATimestampOutsideTheWindowIsRefused is criterion 2.
func TestIssue1870_ATimestampOutsideTheWindowIsRefused(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-ts")
	src := issue1870HMAC(name, "whsec_ts", nil)
	auth := src["auth"].(map[string]any)
	auth["timestamp_header"] = "X-Timestamp"
	auth["signed"] = "timestamp.body"
	auth["tolerance_seconds"] = 300
	issue1870Create(t, c, src)

	sign := func(ts string, body []byte) string {
		return issue1870Sign("whsec_ts", append([]byte(ts+"."), body...))
	}
	for _, f := range issue1870Forms {
		t.Run(f.name, func(t *testing.T) {
			body := f.encode(`{"n":1}`)
			stale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
			res, text := issue1870Post(t, baseURL(), name, f.contentType, body,
				map[string]string{"X-Timestamp": stale, "X-Signature": sign(stale, body)})
			if res.StatusCode != http.StatusUnauthorized || !strings.Contains(text, "tolerance") {
				t.Fatalf("a valid signature over a stale timestamp was answered %d: %s", res.StatusCode, text)
			}
			fresh := strconv.FormatInt(time.Now().Unix(), 10)
			res, text = issue1870Post(t, baseURL(), name, f.contentType, body,
				map[string]string{"X-Timestamp": fresh, "X-Signature": sign(fresh, body)})
			if res.StatusCode != http.StatusAccepted {
				t.Fatalf("a fresh signed request was answered %d: %s", res.StatusCode, text)
			}
		})
	}
}

// TestIssue1870_ABodyOverTheLimitIsRefusedAndNotStored is criterion 3.
func TestIssue1870_ABodyOverTheLimitIsRefusedAndNotStored(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-size")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_size", map[string]any{"max_body_bytes": 256, "event_id_path": "$.id"}))
	for _, f := range issue1870Forms {
		t.Run(f.name, func(t *testing.T) {
			body := f.encode(`{"id":"big-` + f.name + `","pad":"` + strings.Repeat("x", 400) + `"}`)
			res, text := issue1870Signed(t, name, "whsec_size", f.contentType, body)
			if res.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("a body over max_body_bytes was answered %d: %s", res.StatusCode, text)
			}
		})
	}
	if n := issue1870Rows(t, c, name, ""); n != 0 {
		t.Fatalf("refused bodies stored %d events", n)
	}
}

// TestIssue1870_NothingIsAcknowledgedThatIsNotWritten is criterion 4: with the
// object store stalled, a request is answered 503 with Retry-After, not 202.
func TestIssue1870_NothingIsAcknowledgedThatIsNotWritten(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-down")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_down", map[string]any{"event_id_path": "$.id"}))

	resume := issue1870Pause(t)
	for _, f := range issue1870Forms {
		res, text := issue1870Signed(t, name, "whsec_down", f.contentType, f.encode(`{"id":"while-down-`+f.name+`"}`))
		if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("Retry-After") == "" {
			t.Errorf("%s: with the store stalled the request was answered %d (Retry-After %q): %s",
				f.name, res.StatusCode, res.Header.Get("Retry-After"), text)
		}
	}
	resume()
	issue1870WaitForStore(t, name)
}

// issue1870WaitForStore waits until the store answers writes again, by
// posting until one is acknowledged.
func issue1870WaitForStore(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		body := []byte(`{"id":"after-resume"}`)
		res, _ := issue1870Signed(t, name, "whsec_down", "application/json", body)
		if res.StatusCode == http.StatusAccepted {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the store did not answer writes again within two minutes of resuming")
}

// TestIssue1870_AFullBufferRefusesAndStaysBounded is criterion 5: with a
// buffer limit of 100 and the store stalled, 1,000 concurrent requests to one
// replica are refused past the limit with Retry-After, and the replica's
// webhook_buffer_events gauge never exceeds the limit.
func TestIssue1870_AFullBufferRefusesAndStaysBounded(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-buffer")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_buf", map[string]any{"buffer_limit": 100, "event_id_path": "$.id"}))
	replica := issue1870MetricsReplica(t)

	resume := issue1870Pause(t)
	stop := make(chan struct{})
	maxGauge := make(chan int, 1)
	go func() {
		highest := 0
		for {
			select {
			case <-stop:
				maxGauge <- highest
				return
			default:
			}
			if v, ok := issue1870Gauge(name); ok && v > highest {
				highest = v
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	const requests = 1000
	type answer struct {
		status     int
		retryAfter string
	}
	answers := make(chan answer, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := issue1870Forms[i%2]
			body := f.encode(fmt.Sprintf(`{"id":"burst-%d"}`, i))
			res, _ := issue1870Post(t, replica, name, f.contentType, body,
				map[string]string{"X-Signature": issue1870Sign("whsec_buf", body)})
			answers <- answer{res.StatusCode, res.Header.Get("Retry-After")}
		}()
	}
	// Every request past the limit is answered while the store is stalled.
	deadline := time.Now().Add(20 * time.Second)
	for len(answers) < requests-100 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if got := len(answers); got < requests-100 {
		t.Errorf("only %d of %d requests were answered while the store was stalled", got, requests)
	}
	resume()
	wg.Wait()
	close(answers)
	close(stop)

	var accepted, refused int
	for a := range answers {
		switch a.status {
		case http.StatusAccepted:
			accepted++
		case http.StatusServiceUnavailable:
			refused++
			if a.retryAfter == "" {
				t.Errorf("a 503 carried no Retry-After")
			}
		default:
			t.Errorf("a request was answered %d", a.status)
		}
	}
	if accepted > 100 {
		t.Errorf("%d requests were acknowledged; the buffer holds 100", accepted)
	}
	if refused < requests-100 {
		t.Errorf("%d requests were refused; want every one past the limit (%d)", refused, requests-100)
	}
	highest := <-maxGauge
	if highest > 100 {
		t.Errorf("webhook_buffer_events reached %d; the limit is 100", highest)
	}
	if highest == 0 {
		t.Errorf("webhook_buffer_events was never read above 0; the gauge was not observed during the burst")
	}
	t.Logf("accepted %d, refused %d, highest buffer gauge %d", accepted, refused, highest)
}

// issue1870MetricsReplica is the replica whose /metrics the local stack
// exposes: the one that listens on the metrics port, which is the one on the
// API port.
func issue1870MetricsReplica(t *testing.T) string {
	t.Helper()
	port := os.Getenv("DEV_API_PORT")
	if port == "" {
		port = "8080"
	}
	return "http://localhost:" + port
}

// issue1870Gauge reads webhook_buffer_events for a source off the metrics
// endpoint.
func issue1870Gauge(source string) (int, bool) {
	addr := os.Getenv("ISSUE_1870_METRICS")
	if addr == "" {
		addr = "http://localhost:9464/metrics"
	}
	res, err := http.Get(addr)
	if err != nil {
		return 0, false
	}
	defer res.Body.Close() //nolint:errcheck // read below
	raw, _ := io.ReadAll(res.Body)
	prefix := `webhook_buffer_events{source="` + source + `"`
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			return int(v), true
		}
	}
	return 0, false
}

// TestIssue1870_ABatchSplitsIntoOneRowPerEvent is criterion 7.
func TestIssue1870_ABatchSplitsIntoOneRowPerEvent(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-split")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_split", map[string]any{
		"split": "$", "event_id_path": "$.id", "event_type_path": "$.event",
	}))
	for _, f := range issue1870Forms {
		t.Run(f.name, func(t *testing.T) {
			events := make([]map[string]any, 500)
			for i := range events {
				events[i] = map[string]any{"id": fmt.Sprintf("%s-%d", f.name, i), "event": "delivered"}
			}
			raw, _ := json.Marshal(events)
			res, text := issue1870Signed(t, name, "whsec_split", f.contentType, f.encode(string(raw)))
			if res.StatusCode != http.StatusAccepted || !strings.Contains(text, `"accepted":500`) {
				t.Fatalf("a batch of 500 was answered %d: %s", res.StatusCode, text)
			}
			if n := issue1870Rows(t, c, name, "event_id LIKE '"+f.name+"-%' AND event_type = 'delivered'"); n != 500 {
				t.Fatalf("a batch of 500 produced %d rows", n)
			}
		})
	}
}

// TestIssue1870_DisabledAndUnknownAnswerTheSame is criterion 13.
func TestIssue1870_DisabledAndUnknownAnswerTheSame(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-off")
	src := issue1870HMAC(name, "whsec_off", nil)
	src["enabled"] = false
	issue1870Create(t, c, src)

	body := []byte(`{"n":1}`)
	disabled, disabledBody := issue1870Signed(t, name, "whsec_off", "application/json", body)
	unknown, unknownBody := issue1870Signed(t, issue1870Name("acc1870-none"), "whsec_off", "application/json", body)
	if disabled.StatusCode != http.StatusNotFound || unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled answered %d, unknown answered %d; both must be 404", disabled.StatusCode, unknown.StatusCode)
	}
	if disabledBody != unknownBody {
		t.Fatalf("the bodies differ, which says which names exist:\n%s\n%s", disabledBody, unknownBody)
	}
}

// TestIssue1870_ARotatedSecretOverlapsThenEnds is criterion 15.
func TestIssue1870_ARotatedSecretOverlapsThenEnds(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-rotate")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_old", nil))
	status, out := c.rest(http.MethodPut, "/api/v1/admin/webhooks/sources/"+name, jsonBody(t, map[string]any{
		"auth":                     map[string]any{"mode": "hmac", "secret": "whsec_new", "signature_header": "X-Signature", "prefix": "sha256="},
		"config":                   map[string]any{},
		"rotation_overlap_seconds": 15,
	}))
	if status != http.StatusOK {
		t.Fatalf("rotating the secret: %d %v", status, out)
	}
	if strings.Contains(fmt.Sprint(out), "whsec_") {
		t.Fatalf("the update answered with a secret: %v", out)
	}
	body := []byte(`{"n":1}`)
	for _, secret := range []string{"whsec_old", "whsec_new"} {
		if res, text := issue1870Signed(t, name, secret, "application/json", body); res.StatusCode != http.StatusAccepted {
			t.Fatalf("during the overlap %s was answered %d: %s", secret, res.StatusCode, text)
		}
	}
	// The receiver re-reads sources every five seconds, so the end of the
	// overlap reaches every replica within that of the moment it passes.
	time.Sleep(22 * time.Second)
	if res, _ := issue1870Signed(t, name, "whsec_old", "application/json", body); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after the overlap the old secret was answered %d, want 401", res.StatusCode)
	}
	if res, text := issue1870Signed(t, name, "whsec_new", "application/json", body); res.StatusCode != http.StatusAccepted {
		t.Fatalf("after the overlap the new secret was answered %d: %s", res.StatusCode, text)
	}
}

// TestIssue1870_TheCloudEventsHandshakeIsAnsweredOnlyWhereDeclared is
// criterion 16.
func TestIssue1870_TheCloudEventsHandshakeIsAnsweredOnlyWhereDeclared(t *testing.T) {
	c := connect(t)
	withIt := issue1870Name("acc1870-ce")
	issue1870Create(t, c, issue1870HMAC(withIt, "whsec_ce", map[string]any{"handshake": "cloudevents"}))
	without := issue1870Name("acc1870-noce")
	issue1870Create(t, c, issue1870HMAC(without, "whsec_ce", nil))

	options := func(name string) *http.Response {
		req, err := http.NewRequest(http.MethodOptions, baseURL()+"/hooks/"+name, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("WebHook-Request-Origin", "sender.example.com")
		res, err := issue1870Client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res
	}
	res := options(withIt)
	if res.StatusCode != http.StatusOK || res.Header.Get("WebHook-Allowed-Origin") != "sender.example.com" {
		t.Fatalf("the handshake was answered %d with WebHook-Allowed-Origin %q", res.StatusCode, res.Header.Get("WebHook-Allowed-Origin"))
	}
	if res := options(without); res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("a source without the handshake answered OPTIONS %d, want 405", res.StatusCode)
	}
}

// TestIssue1870_ASourceWithNoPersonaIsFoundByAnAdministrator is criterion 14
// for a source that names no persona: its compacted windows are in the
// administrator persona's library, so an administrator's search finds them
// with the source's table, and a window's own resource page reports that
// table. The window is an hour three days old, of a source compacting by the
// default hour, which the compactor takes as soon as it is recorded.
func TestIssue1870_ASourceWithNoPersonaIsFoundByAnAdministrator(t *testing.T) {
	c := connect(t)
	db := issue1870DB(t)
	store := issue1870S3(t)
	name := issue1870Name("acc1870-admin")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_admin", nil))
	hour := issue1870WriteOldHour(t, db, store, name)
	resourceID := issue1870ResourceOf(t, db, name, hour)

	issue1870SearchFindsTable(t, c, name, resourceID)

	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+resourceID+"/tables", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the window's tables: %d %v", status, body)
	}
	regs, _ := body["table_registrations"].([]any)
	found := false
	for _, r := range regs {
		reg := r.(map[string]any)
		if reg["source_kind"] == "webhook" && reg["query_table"] == issue1870Table(name) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the window's resource does not report the source's table: %v", body)
	}
}

// TestIssue1870_ABatchLargerThanTheBufferIsRefusedOnce holds that a request
// the buffer could never hold is answered 413 naming the limit, not 503, which
// a sender would retry forever.
func TestIssue1870_ABatchLargerThanTheBufferIsRefusedOnce(t *testing.T) {
	c := connect(t)
	name := issue1870Name("acc1870-bigbatch")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_big", map[string]any{"split": "$", "buffer_limit": 10}))
	for _, f := range issue1870Forms {
		batch := make([]map[string]any, 11)
		for i := range batch {
			batch[i] = map[string]any{"id": fmt.Sprintf("%s-%d", f.name, i)}
		}
		raw, _ := json.Marshal(batch)
		res, text := issue1870Signed(t, name, "whsec_big", f.contentType, f.encode(string(raw)))
		if res.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(text, "buffer_limit of 10") {
			t.Errorf("%s: a batch of 11 on a buffer of 10 was answered %d: %s", f.name, res.StatusCode, text)
		}
	}
	if n := issue1870Rows(t, c, name, ""); n != 0 {
		t.Fatalf("the refused batches stored %d events", n)
	}
}

// TestIssue1870_TheCompactionWindowIsASourceSetting holds that
// compact_every_minutes is refused unless it divides an hour, and that an
// event is filed under the window it was received in: the minute partition of
// a five-minute source is the minute its window starts at.
func TestIssue1870_TheCompactionWindowIsASourceSetting(t *testing.T) {
	c := connect(t)
	for _, form := range []any{7, 0.5, "5"} {
		body := issue1870HMAC(issue1870Name("acc1870-badwindow"), "whsec_w", map[string]any{"compact_every_minutes": form})
		status, out := c.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, body))
		if status != http.StatusBadRequest {
			t.Errorf("compact_every_minutes %#v was answered %d, want 400: %v", form, status, out)
		}
	}

	name := issue1870Name("acc1870-window")
	issue1870Create(t, c, issue1870HMAC(name, "whsec_w", map[string]any{"compact_every_minutes": 5, "event_id_path": "$.id"}))
	status, got := c.rest(http.MethodGet, "/api/v1/admin/webhooks/sources/"+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the source: %d %v", status, got)
	}
	cfg, _ := got["source"].(map[string]any)["config"].(map[string]any)
	if cfg["compact_every_minutes"] != float64(5) {
		t.Fatalf("the source reads back compact_every_minutes %v, want 5", cfg["compact_every_minutes"])
	}
	if res, text := issue1870Signed(t, name, "whsec_w", "application/json", []byte(`{"id":"w1"}`)); res.StatusCode != http.StatusAccepted {
		t.Fatalf("the event was answered %d: %s", res.StatusCode, text)
	}
	now := time.Now().UTC()
	want := fmt.Sprintf("%02d", now.Truncate(5*time.Minute).Minute())
	// Either side of a window boundary the event is in one of two windows.
	prev := fmt.Sprintf("%02d", now.Add(-10*time.Second).Truncate(5*time.Minute).Minute())
	if n := issue1870Rows(t, c, name, fmt.Sprintf("event_id = 'w1' AND minute IN ('%s', '%s')", want, prev)); n != 1 {
		t.Fatalf("the event is not filed under its five-minute window (%s or %s)", want, prev)
	}
}
