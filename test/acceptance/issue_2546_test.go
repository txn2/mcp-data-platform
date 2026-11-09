//go:build integration

package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #2546: Capture as insight failed on a thread whose text was over the
// 4,000-byte limit and the portal did not say why. The panel now shows the
// server's message, and a reviewer shortens over-limit text in an editable
// field. Nothing is cut silently. The refusal is logged, without the text.
//
// Wire forms: the capture route takes a JSON object whose optional content is a
// string, and is sent with content and with no body at all; the panel sends the
// same object from a real Chromium (ui/e2e/feedback-drawer).

const (
	refusedLog2546 = "capture as insight refused"
	limit2546      = 4000
)

// world2546 is a correction thread with one message on a new asset.
func world2546(t *testing.T, admin *client, body string) (id string) {
	t.Helper()
	asset := issue2290Asset(t, admin, "2546 capture")
	return openThread2580(t, admin, map[string]any{"kind": "correction", "target_type": "asset", "asset_id": asset, "body": body})
}

// exactly2546 is text of exactly n bytes starting with marker.
func exactly2546(marker string, n int) string {
	return marker + " " + strings.Repeat("x", n-len(marker)-1)
}

func thread2546(t *testing.T, admin *client, id string) map[string]any {
	t.Helper()
	status, thread := admin.rest(http.MethodGet, threads2580+"/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the thread: %d %v", status, thread)
	}
	return thread
}

func assertNotCaptured2546(t *testing.T, admin *client, id string) {
	t.Helper()
	thread := thread2546(t, admin, id)
	if linked, _ := thread["insight_id"].(string); linked != "" {
		t.Errorf("the thread was linked to %s", linked)
	}
	if thread["status"] != "open" {
		t.Errorf("the thread's status is %v, want open", thread["status"])
	}
}

func TestIssue2546_ALongThreadIsCapturedAfterTheReviewerShortensIt(t *testing.T) {
	admin := connect(t)
	marker := marker2580("2546")
	id := world2546(t, admin, exactly2546(marker, 4357))
	shortened := "acc-2546 the reviewer kept the part worth keeping."

	browser2580(t, "capture shortened", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_MARKER="+marker, "FEEDBACK_SHORTENED="+shortened)

	thread := thread2546(t, admin, id)
	insight, _ := thread["insight_id"].(string)
	if insight == "" || thread["status"] != "resolved" {
		t.Fatalf("the thread did not resolve against an insight: %v", thread)
	}
	if got := platformQuery2532(t, fmt.Sprintf("SELECT content FROM memory_records WHERE id = '%s'", insight)); got != shortened {
		t.Errorf("the insight holds %q, want %q", got, shortened)
	}
}

func TestIssue2546_OverTheLimitCannotBeSubmittedFromThePanel(t *testing.T) {
	admin := connect(t)
	marker := marker2580("2546")
	id := world2546(t, admin, exactly2546(marker, limit2546+1))
	browser2580(t, "capture blocked", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_MARKER="+marker)
	assertNotCaptured2546(t, admin, id)
}

func TestIssue2546_ARefusedCaptureShowsTheServersMessage(t *testing.T) {
	admin := connect(t)
	// Under the minimum, so the plain button is offered and the server refuses.
	marker := fmt.Sprintf("c%07d", time.Now().UnixNano()%10_000_000)
	id := world2546(t, admin, marker)
	browser2580(t, "capture refused", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_MARKER="+marker,
		"FEEDBACK_REFUSAL=content must be at least 10 bytes (got 8)")
	assertNotCaptured2546(t, admin, id)
}

func TestIssue2546_TheServerRefusesTextOverTheLimitLogsWhyAndNotTheText(t *testing.T) {
	admin := connect(t)
	cases := []struct {
		name, label, want string
		bytes             int
		body              func(text string) io.Reader
	}{
		{
			"the default text, with no body", "2546-default", "content must be at most 4000 bytes (got 4357)", 4357,
			func(string) io.Reader { return http.NoBody },
		},
		{
			"content past the limit", "2546-sent", "content must be at most 4000 bytes (got 4001)", limit2546 + 1,
			func(text string) io.Reader { return jsonBody(t, map[string]any{"content": text}) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marker := marker2580(tc.label)
			text := exactly2546(marker, tc.bytes)
			id := world2546(t, admin, text)
			status, got := admin.rest(http.MethodPost, threads2580+"/"+id+"/insight", tc.body(text))
			assertNotCaptured2546(t, admin, id)
			if status != http.StatusBadRequest || got["detail"] != tc.want {
				t.Fatalf("the refusal: %d %v, want %q", status, got, tc.want)
			}
			logged := readLogs2546(t)
			if !strings.Contains(logged, refusedLog2546) || !strings.Contains(logged, tc.want) {
				t.Errorf("no replica logged the refusal %q", tc.want)
			}
			if strings.Contains(logged, marker) {
				t.Errorf("a replica's log holds the feedback's text (marker %s)", marker)
			}
		})
	}
}

// readLogs2546 is what both dev replicas have written, one of which served the
// request.
func readLogs2546(t *testing.T) string {
	t.Helper()
	var all strings.Builder
	for _, key := range []string{"DEV_AIR_LOG", "DEV_AIR_B_LOG"} {
		path := os.Getenv(key)
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(path) // #nosec G304 G703 -- the dev stack's own log file, named by make dev
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		all.Write(raw)
	}
	if all.Len() == 0 {
		t.Fatal("no replica log was read; `make acceptance` loads DEV_AIR_LOG from dev/.dev-ports.env")
	}
	return all.String()
}
