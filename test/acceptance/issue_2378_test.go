//go:build integration

package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// issue2378Linear signs body as Linear does: the hex HMAC-SHA256 of the body in
// Linear-Signature, with the timestamp carried in the body.
func issue2378Linear(body []byte) issue2223Request {
	sig := hex.EncodeToString(issue2223MAC(sha256.New, []byte(issue2223Secret), body))
	return issue2223Request{contentType: "application/json", body: body, header: map[string]string{"Linear-Signature": sig}}
}

// TestIssue2378_BodyTimestampRefusalNamesTheBodyField is the ticket's first
// criterion: a Linear source sent a correctly signed body with no
// webhookTimestamp, and one sent a body that is not JSON, are each refused
// with a reason naming the body (the field, or the unreadable body), never a
// header.
func TestIssue2378_BodyTimestampRefusalNamesTheBodyField(t *testing.T) {
	c := connect(t)
	source := issue2223Source(t, c, "linear", issue2223Secret, nil, nil)

	status, body := issue2223Post(t, source, issue2378Linear([]byte(`{"action":"create","webhookId":"m-`+source+`"}`)))
	if status != http.StatusUnauthorized || !strings.Contains(body, "timestamp field is missing from the body") ||
		!strings.Contains(body, "webhookTimestamp") || strings.Contains(body, "header") {
		t.Fatalf("a body with no webhookTimestamp was answered %d %s, want 401 naming the body field webhookTimestamp and no header", status, body)
	}

	status, body = issue2223Post(t, source, issue2378Linear([]byte(`{"action":"create","webhookTimestamp":1:}`)))
	if status != http.StatusUnauthorized || !strings.Contains(body, "the body is not JSON") || strings.Contains(body, "header") {
		t.Fatalf("a body that is not JSON was answered %d %s, want 401 saying the body is not JSON and no header", status, body)
	}
}

// TestIssue2378_HeaderTimestampRefusalStillNamesTheHeader is the second
// criterion: a Slack source whose request has no X-Slack-Request-Timestamp is
// still told the timestamp header is missing.
func TestIssue2378_HeaderTimestampRefusalStillNamesTheHeader(t *testing.T) {
	c := connect(t)
	source := issue2223Source(t, c, "slack", issue2223Secret, nil, nil)
	r := issue2223SlackStyle("X-Slack-Signature", "X-Slack-Request-Timestamp")(time.Now(), "m-"+source, nil)
	delete(r.header, "X-Slack-Request-Timestamp")
	status, body := issue2223Post(t, source, r)
	if status != http.StatusUnauthorized || !strings.Contains(body, "the timestamp header is missing") {
		t.Fatalf("a Slack request with no timestamp header was answered %d %s, want 401 naming the timestamp header", status, body)
	}
}

// TestIssue2378_LinearTamperRefusedForTheSignatureAtEveryTimestamp is the
// third criterion: the #2223 Linear tamper is refused for its signature
// whatever digit the timestamp ends in. Each request is signed at a time
// within Linear's one-minute window whose milliseconds end in 0 through 9,
// which is the case that failed about one nightly in ten.
func TestIssue2378_LinearTamperRefusedForTheSignatureAtEveryTimestamp(t *testing.T) {
	c := connect(t)
	source := issue2223Source(t, c, "linear", issue2223Secret, nil, nil)
	now := time.Now().Truncate(10 * time.Millisecond)
	for digit := range 10 {
		at := now.Add(-time.Duration(10-digit) * time.Millisecond)
		ms := strconv.FormatInt(at.UnixMilli(), 10)
		if ms[len(ms)-1] != byte('0'+digit) {
			t.Fatalf("timestamp %s does not end in %d", ms, digit)
		}
		good := issue2378Linear([]byte(`{"action":"create","webhookId":"m-` + source + `","webhookTimestamp":` + ms + `}`))
		status, body := issue2223Post(t, source, issue2223FlipBody(good))
		if status != http.StatusUnauthorized || !strings.Contains(body, "the signature does not match") {
			t.Fatalf("timestamp ending %d: the tampered request was answered %d %s, want 401 naming the signature", digit, status, body)
		}
	}
}
