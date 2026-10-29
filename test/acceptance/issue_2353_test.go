//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Acceptance for #2353: a body_token source verifies the token its sender
// puts in the body and stores none of it. Each request is posted to
// POST /hooks/{source} on the running platform, as a form body and as a JSON
// body, and the source's table is read back with trino_query.

// issue2353Secret is the token both sources are given.
const issue2353Secret = "acc-2353-c2VjcmV0LXRva2Vu"

// TestIssue2353_MattermostTokenIsNotStored posts Mattermost's outgoing
// webhook as a form body and as JSON; both land, and no row holds the token.
func TestIssue2353_MattermostTokenIsNotStored(t *testing.T) {
	c := connect(t)
	source := issue2223Source(t, c, "mattermost", issue2353Secret, nil, nil)
	form := url.Values{"token": {issue2353Secret}, "text": {"deploy finished"}, "post_id": {"p-form"}}.Encode()
	if status, out := issue2223Post(t, source, issue2223Request{contentType: "application/x-www-form-urlencoded", body: []byte(form)}); status != http.StatusAccepted {
		t.Fatalf("the form body was answered %d %s, want 202", status, out)
	}
	jsonBody := `{"token":"` + issue2353Secret + `","text":"deploy finished","post_id":"p-json"}`
	if status, out := issue2223Post(t, source, issue2223Request{contentType: "application/json", body: []byte(jsonBody)}); status != http.StatusAccepted {
		t.Fatalf("the JSON body was answered %d %s, want 202", status, out)
	}
	issue1870AwaitRows(t, c, source, "", 2)
	if n := issue1870Rows(t, c, source, "strpos(payload, '"+issue2353Secret+"') > 0"); n != 0 {
		t.Fatalf("%d stored events hold the source's token, want none", n)
	}
	if n := issue1870Rows(t, c, source, "json_extract_scalar(payload, '$.text') = 'deploy finished' AND json_extract_scalar(payload, '$.token') IS NULL"); n != 2 {
		t.Fatalf("%d events kept the sender's text without the token, want 2", n)
	}
}

// TestIssue2353_GraphClientStateIsNotStored posts a Microsoft Graph batch
// split into one event per notification; every notification carried the
// clientState and no event holds it.
func TestIssue2353_GraphClientStateIsNotStored(t *testing.T) {
	c := connect(t)
	source := issue2223Source(t, c, "microsoft_graph", issue2353Secret, nil, map[string]any{"split": "$.value", "event_type_path": "$.changeType"})
	item := func(id string) string {
		return `{"subscriptionId":"` + id + `","changeType":"updated","clientState":"` + issue2353Secret + `"}`
	}
	body := `{"value":[` + strings.Join([]string{item("a"), item("b"), item("c")}, ",") + `]}`
	if status, out := issue2223Post(t, source, issue2223Request{contentType: "application/json", body: []byte(body)}); status != http.StatusAccepted {
		t.Fatalf("the batch was answered %d %s, want 202", status, out)
	}
	issue1870AwaitRows(t, c, source, "", 3)
	if n := issue1870Rows(t, c, source, "strpos(payload, '"+issue2353Secret+"') > 0"); n != 0 {
		t.Fatalf("%d stored notifications hold the clientState, want none", n)
	}
	if n := issue1870Rows(t, c, source, "event_type = 'updated' AND json_extract_scalar(payload, '$.subscriptionId') IN ('a','b','c')"); n != 3 {
		t.Fatalf("%d notifications kept their own fields, want 3", n)
	}
}
