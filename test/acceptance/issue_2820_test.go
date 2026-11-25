//go:build integration

package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// Issue #2820: an agent reports a problem with the platform itself to its
// operator. manage_feedback action=create topic=platform posts what it was
// trying to do, what blocked it, the tool, a suggestion and the calls it made;
// an administrator turns it on and names the channels a post is sent to in
// Admin > Settings > Platform feedback. A post naming a credential (a known
// stored secret in any encoding, or a credential's shape) is refused naming
// the field, and the value lands nowhere. A repeat is counted on the open post
// it repeats. A session and a persona have limits.
//
// Wire forms: manage_feedback's action, topic, target_type, trying_to,
// blocked_by, tool and suggestion are strings and calls a list of strings,
// each sent in that one form; the settings route takes a JSON object whose
// enabled is a boolean, channels and personas lists of strings and the limits
// numbers. No parameter touched admits a second form.

const issue2820Settings = "/api/v1/admin/settings/platform-feedback"

// issue2820Configure saves the settings for the test and restores what was
// there when it ends.
func issue2820Configure(t *testing.T, c *client, settings map[string]any) {
	t.Helper()
	_, before := c.rest(http.MethodGet, issue2820Settings, nil)
	if status := c.restJSON(http.MethodPut, issue2820Settings, settings); status != http.StatusOK {
		t.Fatalf("saving the platform feedback settings answered %d", status)
	}
	t.Cleanup(func() {
		prior := map[string]any{"enabled": false}
		for k, v := range before {
			if k != "updated_by" && k != "updated_at" {
				prior[k] = v
			}
		}
		c.restJSON(http.MethodPut, issue2820Settings, prior)
	})
}

// issue2820On turns posting on with no channel and generous persona limits.
func issue2820On(t *testing.T, c *client, channels ...string) {
	t.Helper()
	if channels == nil {
		channels = []string{}
	}
	issue2820Configure(t, c, map[string]any{"enabled": true, "channels": channels, "persona_daily_limit": 10000})
}

// issue2820Post posts and returns the raw result.
func issue2820Post(t *testing.T, c *client, tryingTo, blockedBy string) (bool, string, map[string]any) {
	t.Helper()
	res, text, err := c.callRaw("manage_feedback", map[string]any{
		"action": "create", "topic": "platform", "target_type": "standalone",
		"trying_to": tryingTo, "blocked_by": blockedBy, "tool": "trino_query",
		"suggestion": "Name the catalog the connection reads.", "calls": []string{"mcp:call:acc2820"},
	})
	if err != nil {
		t.Fatalf("manage_feedback: transport error: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return res.IsError, text, out
}

// issue2820Forget deletes posts when the test ends.
func issue2820Forget(t *testing.T, ids *[]string) {
	t.Helper()
	db := devDB(t)
	t.Cleanup(func() {
		for _, id := range *ids {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM platform_feedback WHERE id = $1`, id)
		}
	})
}

// TestIssue2820_APostReachesTheChannelAsAnEnvelope: with a json channel into
// an hmac source named in the settings, a post lands as a platform.feedback
// envelope carrying the post's fields and nothing else.
func TestIssue2820_APostReachesTheChannelAsAnEnvelope(t *testing.T) {
	c := connect(t)
	source := issue1996Source(t, c, 300)
	hook := issue1996Connection(t, c, issue1996SelfURL()+"/hooks/"+source, map[string]any{"hmac_preset": "platform"})
	ch := issue1997Channel(t, c, hook, "json")
	issue2820On(t, c, ch)
	var ids []string
	issue2820Forget(t, &ids)

	trying := fmt.Sprintf("acc-2820 envelope %d: read the orders table", time.Now().UnixNano())
	refused, text, out := issue2820Post(t, c, trying, "trino_query said the catalog does not exist")
	if refused {
		t.Fatalf("the post was refused: %s", text)
	}
	id, _ := out["id"].(string)
	ids = append(ids, id)
	if n, _ := out["channels"].(float64); n != 1 {
		t.Errorf("the post was queued for %v channels, want 1: %s", out["channels"], text)
	}
	ev := issue1997Landed(t, c, source, "Platform feedback: "+trying)
	if ev.typ != "platform.feedback" {
		t.Errorf("event_type = %q, want platform.feedback", ev.typ)
	}
	data, _ := ev.payload["data"].(map[string]any)
	if data["id"] != id || data["trying_to"] != trying || data["tool"] != "trino_query" {
		t.Errorf("the envelope's data = %v", data)
	}
	allowed := []string{"id", "trying_to", "blocked_by", "tool", "suggestion", "calls", "persona"}
	for k := range data {
		if !slices.Contains(allowed, k) {
			t.Errorf("the envelope carries %q, which a post never sends", k)
		}
	}
}

// issue2820Encodings is a value in every form a sink could hold it.
func issue2820Encodings(v string) []string {
	j, _ := json.Marshal(v)
	return []string{
		v, base64.StdEncoding.EncodeToString([]byte(v)), base64.RawURLEncoding.EncodeToString([]byte(v)),
		hex.EncodeToString([]byte(v)), url.QueryEscape(v), strings.Trim(string(j), `"`),
	}
}

// TestIssue2820_ACredentialIsRefusedAndLandsNowhere: a stored secret's value,
// sent raw and base64-encoded, and a token's shape are each refused naming the
// field; no table the post could reach holds the value in any form.
func TestIssue2820_ACredentialIsRefusedAndLandsNowhere(t *testing.T) {
	c := connect(t)
	issue2820On(t, c)
	_, value := secret2051(t, c, nil, "acceptance-2820")
	token := "ghp_" + strings.Repeat("A1b2C3d4E5", 3) + "f6G7h8"
	for field, blocked := range map[string]string{
		"raw":    "the upstream refused password " + value,
		"base64": "the header was Basic " + base64.StdEncoding.EncodeToString([]byte(value)),
		"shape":  "I sent the token " + token,
	} {
		refused, text, _ := issue2820Post(t, c, "acc-2820 credential "+field, blocked)
		if !refused || !strings.Contains(text, "platform_feedback_secret") || !strings.Contains(text, "blocked_by holds") {
			t.Errorf("%s: a post holding a credential was not refused naming the field: %s", field, text)
		}
		if strings.Contains(text, value) || strings.Contains(text, token) {
			t.Errorf("%s: the refusal repeats the credential", field)
		}
	}
	db := devDB(t)
	for _, sink := range []string{
		"SELECT row_to_json(f)::text FROM platform_feedback f",
		"SELECT row_to_json(r)::text FROM platform_feedback_reports r",
		"SELECT row_to_json(r)::text FROM platform_feedback_refusals r",
		"SELECT row_to_json(n)::text FROM notifications n WHERE n.created_at > NOW() - interval '1 hour'",
		"SELECT coalesce(parameters::text, '') || coalesce(error_message, '') FROM audit_logs WHERE tool_name = 'manage_feedback' AND timestamp > NOW() - interval '1 hour'",
	} {
		rows, err := db.QueryContext(context.Background(), sink)
		if err != nil {
			t.Fatalf("%s: %v", sink, err)
		}
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			for _, enc := range append(issue2820Encodings(value), issue2820Encodings(token)...) {
				if strings.Contains(row, enc) {
					t.Errorf("%s holds the credential (%q)", sink, enc)
				}
			}
		}
		_ = rows.Close()
	}
}

// TestIssue2820_RepeatsFoldOntoOnePost: three posts of one problem are one
// post with a report count of three.
func TestIssue2820_RepeatsFoldOntoOnePost(t *testing.T) {
	c := connect(t)
	issue2820On(t, c)
	var ids []string
	issue2820Forget(t, &ids)
	stamp := time.Now().UnixNano()
	trying := fmt.Sprintf("acc-2820 fold %d: export the weekly orders report to the resources bucket", stamp)
	blocked := "trino_export refused the bucket the destination names"
	var first string
	for i, variant := range []string{trying, trying + ".", trying} {
		refused, text, out := issue2820Post(t, connect(t), variant, blocked)
		if refused {
			t.Fatalf("post %d was refused: %s", i+1, text)
		}
		id, _ := out["id"].(string)
		if i == 0 {
			first = id
			ids = append(ids, id)
			continue
		}
		if id != first || out["folded"] != true {
			t.Errorf("post %d was not counted on the first: %s", i+1, text)
		}
		if n, _ := out["report_count"].(float64); int(n) != i+1 {
			t.Errorf("post %d reads report_count %v, want %d", i+1, out["report_count"], i+1)
		}
	}
	status, got := c.rest(http.MethodGet, "/api/v1/admin/platform-feedback/"+first, nil)
	if status != http.StatusOK || !strings.Contains(asJSON(got), `"report_count":3`) {
		t.Errorf("the post as an administrator reads it: %d %v", status, got)
	}
}

// TestIssue2820_TheSixthPostInASessionIsRefused names the session limit.
func TestIssue2820_TheSixthPostInASessionIsRefused(t *testing.T) {
	c := connect(t)
	issue2820On(t, c)
	var ids []string
	issue2820Forget(t, &ids)
	stamp := time.Now().UnixNano()
	topics := []string{"read a parquet file", "register a csv table", "publish a site", "schedule a script", "send a notification", "share an asset"}
	for i, topic := range topics {
		refused, text, out := issue2820Post(t, c, fmt.Sprintf("acc-2820 limit %d %d: %s", stamp, i, topic), "the tool answered an error")
		if i < 5 {
			if refused {
				t.Fatalf("post %d was refused: %s", i+1, text)
			}
			if id, _ := out["id"].(string); id != "" {
				ids = append(ids, id)
			}
			continue
		}
		if !refused || !strings.Contains(text, "platform_feedback_limit") || !strings.Contains(text, "session_limit 5") {
			t.Errorf("the sixth post was not refused naming the session limit: %s", text)
		}
	}
}

// TestIssue2820_OffRefusesTheTopic: with the feature off, a post is refused
// saying where an administrator turns it on.
func TestIssue2820_OffRefusesTheTopic(t *testing.T) {
	c := connect(t)
	issue2820Configure(t, c, map[string]any{"enabled": false, "channels": []string{}})
	refused, text, _ := issue2820Post(t, c, "acc-2820 off", "nothing")
	if !refused || !strings.Contains(text, "platform_feedback_off") || !strings.Contains(text, "Admin > Settings > Platform feedback") {
		t.Errorf("a post with the feature off: %s", text)
	}
}
