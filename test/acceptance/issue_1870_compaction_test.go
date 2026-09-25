//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
)

// The criteria about what happens to a window after it ends: compaction, a
// segment that lands late, a reader that runs every minute, and retention.
//
// The sources compact every minute (config.compact_every_minutes = 1) and the
// local stack's compactor waits five seconds after a window ends, so a window
// is compacted within about a minute of its last event. Nothing here waits for
// the wall clock beyond that.
//
// Three situations cannot be produced by posting to the receiver, and are
// written as exactly what they leave behind, for the platform's own compactor
// and retention to process:
//
//   - A segment that lands for a window after the window was compacted is what
//     a replica that stalled or recovered writes. The test writes that segment
//     to the store, and records it against its window the way a receiver does.
//   - An hour days old, of a source compacting by the default hour, is written
//     as its segment and its window row.
//   - A retention pass that stopped between unregistering a window's partition
//     and deleting its objects is the partition unregistered and the step
//     recorded, with the objects still there.

const (
	issue1870BurstEvents = 100_000
	issue1870BatchSize   = 100
	issue1870Senders     = 50
	issue1870LateNew     = 10
	issue1870LateDupes   = 5
)

func TestIssue1870_WindowsCompactOnceAndRetentionFollows(t *testing.T) {
	c := connectFor(t, 30*time.Minute)
	db := issue1870DB(t)
	store := issue1870S3(t)

	burst := issue1870Name("acc1870-burst")
	issue1870Create(t, c, map[string]any{
		"name": burst, "connection": issue1870Conn,
		"auth": map[string]any{"mode": "hmac", "secret": "whsec_burst", "signature_header": "X-Signature", "prefix": "sha256="},
		"config": map[string]any{
			"split": "$", "event_id_path": "$.id", "event_type_path": "$.event", "key_path": "$.email",
			"flush_max_interval_ms": 50, "persona": "admin", "compact_every_minutes": 1,
		},
	})
	quiet := issue1870Name("acc1870-noreader")
	issue1870Create(t, c, issue1870HMAC(quiet, "whsec_quiet", map[string]any{"split": "$", "event_id_path": "$.id", "compact_every_minutes": 1}))
	old := issue1870Name("acc1870-expire")
	zero := 0
	issue1870Create(t, c, issue1870HMAC(old, "whsec_old", map[string]any{"compacted_retention_days": zero}))

	reader := issue1870StartReader(t, c, burst)

	// Criterion 9, first half: an event is returned as soon as its 202 is.
	first := []map[string]any{{"id": "first-seen", "event": "delivered", "email": "a@example.com"}}
	raw, _ := json.Marshal(first)
	if res, text := issue1870Signed(t, burst, "whsec_burst", "application/json", raw); res.StatusCode != http.StatusAccepted {
		t.Fatalf("the first event was answered %d: %s", res.StatusCode, text)
	}
	if n := issue1870Rows(t, c, burst, "event_id = 'first-seen'"); n != 1 {
		t.Fatalf("an acknowledged event is returned %d times before its window is compacted, want 1", n)
	}

	issue1870SendBurst(t, burst)
	issue1870SendQuiet(t, quiet)
	oldHour := issue1870WriteOldHour(t, db, store, old)

	// Wait for every window the burst landed in to end and be compacted.
	issue1870WaitAllCompacted(t, db, burst, 4*time.Minute)
	total := issue1870BurstEvents + 1
	windows := issue1870Windows(t, db, burst)
	t.Logf("the burst landed in %d one-minute windows", len(windows))

	t.Run("criterion 6: a burst with duplicates is every event exactly once", func(t *testing.T) {
		all := issue1870Rows(t, c, burst, "true")
		distinct := issue1870Count(t, c, "SELECT count(DISTINCT event_id) FROM "+issue1870Table(burst))
		if all != total || distinct != total {
			t.Fatalf("count(*) = %d, count(DISTINCT event_id) = %d; want %d for both", all, distinct, total)
		}
	})

	t.Run("criterion 12: both replicas' segments are compacted into the source's windows", func(t *testing.T) {
		replicas := issue1870Count(t, c, "SELECT count(DISTINCT replica) FROM "+issue1870Table(burst))
		if replicas < 2 {
			t.Fatalf("the windows hold events from %d replica(s); the burst went through the proxy to two", replicas)
		}
		for _, w := range windows {
			if w.resourceID == "" {
				t.Errorf("window %s was compacted into no resource", w.start.Format(time.RFC3339))
			}
		}
	})

	t.Run("criterion 9: after compaction the event is returned once", func(t *testing.T) {
		if n := issue1870Rows(t, c, burst, "event_id = 'first-seen'"); n != 1 {
			t.Fatalf("the event is returned %d times after compaction, want 1", n)
		}
	})

	firstWindow := windows[0]
	inFirst := issue1870InWindow(firstWindow.start)

	t.Run("criteria 14 and 20: the window is a resource, found by search with its table", func(t *testing.T) {
		issue1870SearchFindsTable(t, c, burst, firstWindow.resourceID)
		rows := issue1870ParquetRows(t, c, firstWindow.resourceID)
		if n := issue1870Rows(t, c, burst, inFirst); int64(n) != rows {
			t.Fatalf("the window's Parquet file holds %d rows and the table returns %d for the window", rows, n)
		}
	})

	t.Run("criterion 8: a segment written after compaction is compacted in", func(t *testing.T) {
		before := issue1870Rows(t, c, burst, inFirst)
		issue1870WriteLateSegment(t, db, store, burst, firstWindow.start)
		issue1870WaitCompacted(t, db, burst, firstWindow.start, 2*time.Minute)
		if n := issue1870Rows(t, c, burst, inFirst); n != before+issue1870LateNew {
			t.Fatalf("after the late segment the window returns %d events, want %d", n, before+issue1870LateNew)
		}
		want := total + issue1870LateNew
		all := issue1870Rows(t, c, burst, "true")
		distinct := issue1870Count(t, c, "SELECT count(DISTINCT event_id) FROM "+issue1870Table(burst))
		if all != want || distinct != want {
			t.Fatalf("after the late segment count(*) = %d, count(DISTINCT event_id) = %d; want %d", all, distinct, want)
		}
	})

	t.Run("criterion 10: a reader every minute processes each event exactly once", func(t *testing.T) {
		reader.waitFor(t, c, total+issue1870LateNew, 8*time.Minute)
		reader.assertFailedRunRecovered(t, c)
	})

	t.Run("criterion 11: a source with no reader lands and compacts the same", func(t *testing.T) {
		issue1870WaitAllCompacted(t, db, quiet, 2*time.Minute)
		if n := issue1870Rows(t, c, quiet, "true"); n != 20 {
			t.Fatalf("the source nobody reads holds %d events, want 20", n)
		}
	})

	t.Run("criterion 18: raw segments go only for a compacted window", func(t *testing.T) {
		issue1870RawRetention(t, c, db, store, burst, quiet)
	})

	t.Run("criterion 19: an expired window leaves, and a stopped pass is finished", func(t *testing.T) {
		issue1870Expiry(t, c, db, store, old, oldHour)
	})
}

// issue1870Window is one window's row.
type issue1870Window struct {
	start      time.Time
	resourceID string
}

// issue1870Windows lists a source's windows, oldest first.
func issue1870Windows(t *testing.T, db *sql.DB, source string) []issue1870Window {
	t.Helper()
	rows, err := db.Query(`SELECT window_start, resource_id FROM webhook_windows WHERE source = $1 ORDER BY window_start`, source)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck // read below
	var out []issue1870Window
	for rows.Next() {
		var w issue1870Window
		if err := rows.Scan(&w.start, &w.resourceID); err != nil {
			t.Fatal(err)
		}
		w.start = w.start.UTC()
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("source %s recorded no windows", source)
	}
	return out
}

// issue1870InWindow is the predicate selecting one window's rows by its
// partition values.
func issue1870InWindow(start time.Time) string {
	dt, hh, mm := whlayout.PartitionValues(start)
	return fmt.Sprintf("dt = '%s' AND hour = '%s' AND minute = '%s'", dt, hh, mm)
}

// issue1870WaitAllCompacted waits until a source has windows and every one of
// them has its latest segment in its Parquet file.
func issue1870WaitAllCompacted(t *testing.T, db *sql.DB, source string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var total, owed int
		var lastErr sql.NullString
		err := db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE generation <> compacted_generation),
		        max(last_error) FILTER (WHERE last_error <> '')
		   FROM webhook_windows WHERE source = $1 AND expired_at IS NULL`, source).Scan(&total, &owed, &lastErr)
		if err == nil && total > 0 && owed == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("source %s: %d of %d windows still owed a compaction after %s (last error %q, read error %v)",
				source, owed, total, within.Round(time.Second), lastErr.String, err)
		}
		time.Sleep(5 * time.Second)
	}
}

// issue1870DB opens the local platform database, which holds the control
// data the criteria read and the situations they write.
func issue1870DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the local database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// issue1870S3 is a client on the store the managed resources live in, which
// is where every source writes.
func issue1870S3(t *testing.T) *s3client.Client {
	t.Helper()
	port := os.Getenv("DEV_S3_TLS_PORT")
	if port == "" {
		port = "9443"
	}
	ca, err := filepath.Abs("../../dev/.tls/minio/public.crt")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CA_BUNDLE", ca)
	c, err := s3client.New(context.Background(), &s3client.Config{
		Region: "us-east-1", Endpoint: "https://localhost:" + port, UsePathStyle: true,
		AccessKeyID: "dev-access-key", SecretAccessKey: "dev-secret-key", Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("a client on the managed-resources store: %v", err)
	}
	return c
}

const issue1870Bucket = "managed-resources"

// issue1870SendBurst posts 100,000 events in batches of 100 from 50 senders
// through the proxy, and sends 5% of them a second time, as a sender retrying
// does. Every request must be acknowledged.
func issue1870SendBurst(t *testing.T, source string) {
	t.Helper()
	batches := make(chan []map[string]any, issue1870Senders)
	go func() {
		defer close(batches)
		for b := 0; b < issue1870BurstEvents/issue1870BatchSize; b++ {
			batch := make([]map[string]any, 0, issue1870BatchSize)
			for i := range issue1870BatchSize {
				n := b*issue1870BatchSize + i
				batch = append(batch, map[string]any{
					"id": fmt.Sprintf("evt-%06d", n), "event": "delivered",
					"email": fmt.Sprintf("person%d@example.com", n%5000),
				})
			}
			batches <- batch
			if b%20 == 0 {
				// 5 of every 100 batches go again: 5% of the events.
				batches <- batch
			}
		}
	}()
	var sent, failed atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for s := range issue1870Senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := issue1870Forms[s%2]
			for batch := range batches {
				raw, _ := json.Marshal(batch)
				for attempt := 0; ; attempt++ {
					res, text := issue1870Signed(t, source, "whsec_burst", f.contentType, f.encode(string(raw)))
					if res.StatusCode == http.StatusAccepted {
						sent.Add(1)
						break
					}
					// A sender retries what it did not see acknowledged; a
					// criterion about the burst counts only final answers.
					if attempt >= 5 || res.StatusCode != http.StatusServiceUnavailable {
						failed.Add(1)
						t.Errorf("a batch was answered %d after %d attempts: %s", res.StatusCode, attempt+1, text)
						break
					}
					time.Sleep(time.Second)
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("burst: %d batches acknowledged, %d not, in %s", sent.Load(), failed.Load(), time.Since(start).Round(time.Second))
	if failed.Load() > 0 {
		t.FailNow()
	}
}

// issue1870SendQuiet posts twenty events to the source nobody reads.
func issue1870SendQuiet(t *testing.T, source string) {
	t.Helper()
	events := make([]map[string]any, 20)
	for i := range events {
		events[i] = map[string]any{"id": fmt.Sprintf("quiet-%d", i)}
	}
	raw, _ := json.Marshal(events)
	if res, text := issue1870Signed(t, source, "whsec_quiet", "application/json", raw); res.StatusCode != http.StatusAccepted {
		t.Fatalf("the quiet source answered %d: %s", res.StatusCode, text)
	}
}

// issue1870Segment encodes events as the segment a receiver writes.
func issue1870Segment(t *testing.T, ids []string, received time.Time, replica string) []byte {
	t.Helper()
	events := make([]whevent.Event, 0, len(ids))
	for i, id := range ids {
		payload := []byte(fmt.Sprintf(`{"id":%q,"event":"late"}`, id))
		events = append(events, whevent.Event{
			ReceivedAt: received.Add(time.Duration(i) * time.Millisecond), EventID: id, EventType: "late",
			Replica: replica, Payload: payload, ContentHash: id,
		})
	}
	data, err := whevent.EncodeSegment(events, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// issue1870Put writes an object to the managed-resources store.
func issue1870Put(t *testing.T, store *s3client.Client, key string, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := store.PutObject(ctx, &s3client.PutObjectInput{
		Bucket: issue1870Bucket, Key: key, Body: data, ContentType: "application/gzip",
	}); err != nil {
		t.Fatalf("writing %s: %v", key, err)
	}
}

// issue1870Keys lists the objects under a prefix.
func issue1870Keys(t *testing.T, store *s3client.Client, prefix string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var keys []string
	token := ""
	for {
		out, err := store.ListObjects(ctx, issue1870Bucket, prefix, "", 1000, token)
		if err != nil {
			t.Fatalf("listing %s: %v", prefix, err)
		}
		for _, o := range out.Objects {
			keys = append(keys, o.Key)
		}
		if !out.IsTruncated {
			return keys
		}
		token = out.NextContinueToken
	}
}

// issue1870MarkSegment records a segment against its window, as a receiver
// does after it writes one.
func issue1870MarkSegment(t *testing.T, db *sql.DB, source string, start time.Time, length time.Duration) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO webhook_windows (source, window_start, window_seconds, generation, last_segment_at)
		VALUES ($1, $2, $3, 1, NOW())
		ON CONFLICT (source, window_start) DO UPDATE
		   SET generation = webhook_windows.generation + 1, last_segment_at = NOW()`,
		source, start.UTC(), int(length.Seconds())); err != nil {
		t.Fatalf("recording the segment: %v", err)
	}
}

// issue1870WriteLateSegment writes, for a one-minute window already compacted,
// the segment a replica that stalled past the window writes: ten new events
// and five the burst already holds.
func issue1870WriteLateSegment(t *testing.T, db *sql.DB, store *s3client.Client, source string, start time.Time) {
	t.Helper()
	ids := make([]string, 0, issue1870LateNew+issue1870LateDupes)
	for i := range issue1870LateNew {
		ids = append(ids, fmt.Sprintf("late-%d", i))
	}
	for i := range issue1870LateDupes {
		ids = append(ids, fmt.Sprintf("evt-%06d", i*997))
	}
	key := whlayout.SegmentKey(source, start, "acceptance-late-replica", 1)
	issue1870Put(t, store, key, issue1870Segment(t, ids, start.Add(50*time.Second), "acceptance-late-replica"))
	issue1870MarkSegment(t, db, source, start, time.Minute)
}

// issue1870WriteOldHour writes an hour three days old of a source compacting
// by the default hour: its segment, and its window row, as the receiver left
// them then.
func issue1870WriteOldHour(t *testing.T, db *sql.DB, store *s3client.Client, source string) time.Time {
	t.Helper()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-72 * time.Hour)
	ids := []string{"old-1", "old-2", "old-3"}
	issue1870Put(t, store, whlayout.SegmentKey(source, hour, "acceptance-old", 1), issue1870Segment(t, ids, hour, "acceptance-old"))
	issue1870MarkSegment(t, db, source, hour, time.Hour)
	issue1870WaitCompacted(t, db, source, hour, 2*time.Minute)
	return hour
}

// issue1870WaitCompacted waits until a window's latest segment is in its
// Parquet file.
func issue1870WaitCompacted(t *testing.T, db *sql.DB, source string, start time.Time, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var gen, compacted int64
		var lastErr string
		err := db.QueryRow(`SELECT generation, compacted_generation, last_error FROM webhook_windows
			WHERE source = $1 AND window_start = $2`, source, start.UTC()).Scan(&gen, &compacted, &lastErr)
		if err == nil && gen == compacted && gen > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %s of %s was not compacted within %s (generation %d, compacted %d, last error %q, read error %v)",
				start.Format(time.RFC3339), source, within.Round(time.Second), gen, compacted, lastErr, err)
		}
		time.Sleep(5 * time.Second)
	}
}

// issue1870ResourceOf is the resource a window was compacted into.
func issue1870ResourceOf(t *testing.T, db *sql.DB, source string, start time.Time) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`SELECT resource_id FROM webhook_windows WHERE source = $1 AND window_start = $2`,
		source, start.UTC()).Scan(&id); err != nil || id == "" {
		t.Fatalf("the compacted window records no resource: %q %v", id, err)
	}
	return id
}

// issue1870SearchFindsTable is criterion 14: search for the source's name
// returns one of its windows, carrying the table, and fetch lists the eight columns
// with their types.
func issue1870SearchFindsTable(t *testing.T, c *client, source, resourceID string) {
	t.Helper()
	reference := "mcp:resource:" + resourceID
	var table map[string]any
	for attempt := 0; attempt < 30 && table == nil; attempt++ {
		out := c.call("search", map[string]any{
			"intent": source, "sources": []any{"resources"}, "limit": 25,
			"purpose": issue1870Purpose,
		})
		if hit := hitFor1627(out, reference); hit != nil {
			table, _ = hit["table"].(map[string]any)
		}
		if table == nil {
			time.Sleep(4 * time.Second)
		}
	}
	if table == nil {
		t.Fatalf("search for %q does not return the window %s with a table", source, reference)
	}
	if got, want := table["query_table"], issue1870Table(source); got != want {
		t.Fatalf("the hit's table is %v, want %s", got, want)
	}

	tables := fetchTables1627(t, c, reference)
	if len(tables) == 0 {
		t.Fatalf("fetch %s carries no table", reference)
	}
	types := map[string]string{}
	for _, col := range tables[0]["column_types"].([]any) {
		m := col.(map[string]any)
		types[m["name"].(string)] = m["type"].(string)
	}
	want := map[string]string{
		"received_at": "timestamp(6)", "landed_at": "timestamp(6)", "event_id": "varchar", "event_type": "varchar",
		"key": "varchar", "content_hash": "varchar", "replica": "varchar", "payload": "varchar",
	}
	for name, typ := range want {
		if types[name] != typ {
			t.Errorf("fetch lists %s as %q, want %q (all: %v)", name, types[name], typ, types)
		}
	}
}

// issue1870ParquetRows downloads a window's resource through the resources
// route and reads its row count from the Parquet footer.
func issue1870ParquetRows(t *testing.T, c *client, resourceID string) int64 {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+"/api/v1/resources/"+resourceID+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("downloading the window's resource: %d %s", res.StatusCode, data)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "parquet") {
		t.Errorf("the window's resource is served as %q", ct)
	}
	f, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("the window's resource is not a Parquet file: %v", err)
	}
	return f.NumRows()
}

// issue1870RawRetention is criterion 18. Retention counts days; the windows
// are made eight days old by moving their last segment's time back, which is
// the one input the rule reads. The quiet source's window is made dirty, and
// held as a compaction in progress holds it, so it stays uncompacted while
// retention runs.
func issue1870RawRetention(t *testing.T, c *client, db *sql.DB, store *s3client.Client, burst, quiet string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE webhook_windows SET last_segment_at = NOW() - INTERVAL '8 days' WHERE source = $1`,
		burst); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE webhook_windows
		   SET generation = generation + 1, last_segment_at = NOW() - INTERVAL '8 days',
		       claimed_until = NOW() + INTERVAL '1 hour'
		 WHERE source = $1`, quiet); err != nil {
		t.Fatal(err)
	}
	burstPrefix := whlayout.RawPrefix(burst)
	quietPrefix := whlayout.RawPrefix(quiet)
	deadline := time.Now().Add(2 * time.Minute)
	for len(issue1870Keys(t, store, burstPrefix)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the compacted windows' raw segments were not deleted within two minutes")
		}
		time.Sleep(5 * time.Second)
	}
	if n := len(issue1870Keys(t, store, quietPrefix)); n == 0 {
		t.Fatalf("the raw segments of a window owed a compaction were deleted")
	}
	want := issue1870BurstEvents + 1 + issue1870LateNew
	if n := issue1870Rows(t, c, burst, "true"); n != want {
		t.Fatalf("with its raw segments gone the source returns %d events, want %d from its Parquet files", n, want)
	}
	// Released, the dirty window compacts, and then its segments may go.
	if _, err := db.Exec(`UPDATE webhook_windows SET claimed_until = NULL WHERE source = $1`, quiet); err != nil {
		t.Fatal(err)
	}
	issue1870WaitAllCompacted(t, db, quiet, 2*time.Minute)
	deadline = time.Now().Add(2 * time.Minute)
	for len(issue1870Keys(t, store, quietPrefix)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("once compacted, the window's raw segments were not deleted")
		}
		time.Sleep(5 * time.Second)
	}
}

// issue1870Expiry is criterion 19: the old hour, a window of the default
// length, is compacted, retention is
// interrupted after its partition was unregistered, and the next pass, once
// the source's retention covers the hour, deletes the resource and the raw
// segment and records the hour expired.
func issue1870Expiry(t *testing.T, c *client, db *sql.DB, store *s3client.Client, source string, hour time.Time) {
	t.Helper()
	dt, hh, mm := whlayout.PartitionValues(hour)
	inHour := issue1870InWindow(hour)
	if n := issue1870Rows(t, c, source, inHour); n != 3 {
		t.Fatalf("the old hour returns %d events before expiry, want 3", n)
	}
	resourceID := issue1870ResourceOf(t, db, source, hour)

	// The pass that stopped: the partition is unregistered and the step is
	// recorded; the resource and the segment are still there.
	c.call("trino_execute", map[string]any{
		"connection": issue1870Conn, "purpose": issue1870Purpose,
		"sql": fmt.Sprintf("CALL scratch_resources.system.unregister_partition('uploads', '%s', ARRAY['dt', 'hour', 'minute'], ARRAY['%s', '%s', '%s'])",
			"webhook_"+strings.ReplaceAll(source, "-", "_")+"_compacted", dt, hh, mm),
	})
	if _, err := db.Exec(`UPDATE webhook_windows SET unregistered_at = NOW() WHERE source = $1 AND window_start = $2`,
		source, hour.UTC()); err != nil {
		t.Fatal(err)
	}
	status, out := c.rest(http.MethodPut, "/api/v1/admin/webhooks/sources/"+source, jsonBody(t, map[string]any{
		"auth":   map[string]any{"mode": "hmac", "signature_header": "X-Signature", "prefix": "sha256="},
		"config": map[string]any{"compacted_retention_days": 1},
	}))
	if status != http.StatusOK {
		t.Fatalf("shortening retention: %d %v", status, out)
	}

	deadline := time.Now().Add(2 * time.Minute)
	for {
		var expired sql.NullTime
		if err := db.QueryRow(`SELECT expired_at FROM webhook_windows WHERE source = $1 AND window_start = $2`,
			source, hour.UTC()).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired.Valid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the interrupted expiry was not finished within two minutes")
		}
		time.Sleep(5 * time.Second)
	}
	if st, _ := c.rest(http.MethodGet, "/api/v1/resources/"+resourceID, http.NoBody); st != http.StatusNotFound {
		t.Errorf("the expired hour's resource answers %d, want 404", st)
	}
	if keys := issue1870Keys(t, store, whlayout.WindowPrefix(source, hour)); len(keys) != 0 {
		t.Errorf("the expired hour's raw segments remain: %v", keys)
	}
	if n := issue1870Rows(t, c, source, inHour); n != 0 {
		t.Errorf("the expired hour still returns %d events", n)
	}
}

// issue1870Reader is criterion 10's reader: a script scheduled every minute
// that keeps a landed_at watermark in its state, reads from a little before
// it, and inserts each event it has not seen into a table keyed by event id.
// Its first run after it has saved state fails after inserting and before
// saving, which is a run that fails in the middle. A marker row in the sink
// records that the failure happened, so it happens once, whatever the clock.
type issue1870Reader struct {
	script, scriptID, sink string
}

const issue1870ReaderSource = `
view = %q
sink = %q
conn = %q
marker = %q
since = run.state.get("landed_through", "1970-01-01 00:00:00.000000")
rows = platform.query(connection=conn, sql="SELECT CAST(max(landed_at) AS varchar) AS through FROM " + view)["rows"]
through = rows[0]["through"]
if through != None:
    platform.call("trino_execute", {
        "connection": conn,
        "purpose": "Acceptance #1870: a reader processes new events once.",
        "sql": "INSERT INTO " + sink + " SELECT event_id, min(landed_at) FROM " + view + " w" +
               " WHERE w.landed_at > TIMESTAMP '" + since + "' - INTERVAL '30' SECOND" +
               " AND w.landed_at <= TIMESTAMP '" + through + "'" +
               " AND NOT EXISTS (SELECT 1 FROM " + sink + " s WHERE s.event_id = w.event_id)" +
               " GROUP BY event_id",
    })
    failed = platform.query(connection=conn,
        sql="SELECT count(*) AS n FROM " + sink + " WHERE event_id = '" + marker + "'")["rows"][0]["n"]
    if int(failed) == 0 and run.state.get("landed_through") != None:
        platform.call("trino_execute", {
            "connection": conn,
            "purpose": "Acceptance #1870: record the one failed run.",
            "sql": "INSERT INTO " + sink + " VALUES ('" + marker + "', NULL)",
        })
        fail("acceptance #1870: stopping after processing, before the watermark is saved")
    platform.save_state({"landed_through": through})
`

// issue1870FailedMarker is the sink row the reader writes when it fails its
// one run, and issue1870NotMarker leaves it out of the counts.
const (
	issue1870FailedMarker = "acceptance-failed-once"
	issue1870NotMarker    = " WHERE event_id <> '" + issue1870FailedMarker + "'"
)

func issue1870StartReader(t *testing.T, c *client, source string) *issue1870Reader {
	t.Helper()
	r := &issue1870Reader{
		script: issue1870Name("acc1870-reader"),
		sink:   issue1870Schema + ".acc1870_sink_" + strings.ReplaceAll(strings.TrimPrefix(source, "acc1870-burst-"), "-", "_"),
	}
	c.call("trino_execute", map[string]any{
		"connection": issue1870Conn, "purpose": issue1870Purpose,
		"sql": "CREATE TABLE " + r.sink + " (event_id varchar, landed_at timestamp(6))",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("trino_execute", map[string]any{
			"connection": issue1870Conn, "purpose": issue1870Purpose, "sql": "DROP TABLE IF EXISTS " + r.sink,
		})
	})
	out := c.call("manage_script", map[string]any{
		"command": "create", "name": r.script,
		"description": "Acceptance #1870: reads a webhook source every minute, each event once.",
		"source":      fmt.Sprintf(issue1870ReaderSource, issue1870Table(source), r.sink, issue1870Conn, issue1870FailedMarker),
	})
	r.scriptID, _ = out["id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": r.script}) })
	c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": r.script, "cron": "* * * * *", "timezone": "UTC",
	})
	return r
}

// waitFor waits until the sink holds want events, then checks none twice.
func (r *issue1870Reader) waitFor(t *testing.T, c *client, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	got := 0
	for time.Now().Before(deadline) {
		got = issue1870Count(t, c, "SELECT count(*) FROM "+r.sink+issue1870NotMarker)
		if got >= want {
			break
		}
		time.Sleep(15 * time.Second)
	}
	distinct := issue1870Count(t, c, "SELECT count(DISTINCT event_id) FROM "+r.sink+issue1870NotMarker)
	if got != want || distinct != want {
		t.Fatalf("the reader processed %d events (%d distinct); want each of %d exactly once", got, distinct, want)
	}
}

// assertFailedRunRecovered checks a run failed in the middle and the reader
// still processed every event once, which waitFor has just established.
func (r *issue1870Reader) assertFailedRunRecovered(t *testing.T, c *client) {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+r.scriptID+"/runs", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the reader's runs: %d %v", status, body)
	}
	var failed, succeeded int
	for _, raw := range body["data"].([]any) {
		switch raw.(map[string]any)["status"] {
		case "failed":
			failed++
		case "succeeded":
			succeeded++
		}
	}
	if failed == 0 {
		t.Fatalf("no run failed in the middle; the criterion needs one (%d succeeded)", succeeded)
	}
	t.Logf("reader: %d runs succeeded, %d failed after processing and before saving", succeeded, failed)
}
