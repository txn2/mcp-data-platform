//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2743: email channels for outside recipients. A script's
// platform.notify sends no link with link=None or link="", and the run's page
// otherwise; an email channel marked delivery_list sends every address every
// message, an address that turned email off included, with no preferences or
// unsubscribe link and no List-Unsubscribe header; an ordinary email channel
// says who opted out: the notify result, the run's posts and the admin
// channel view; and delivery_list is refused on another kind.
//
// Wire forms: the admin channel PUT takes kind as a string, recipients as a
// list of strings and delivery_list as a JSON boolean (sent true, and left
// out); notify takes action, channel and title as strings, and link left out
// and as the empty string; platform.notify takes link=None, link="" and no
// link; the run posts route takes the script and run in its path.

const (
	chanFeed2743  = "acc-2743-feed"
	chanSales2743 = "acc-2743-sales"
	optedOut2743  = "acc-2743-muted@example.com"
)

// optOut2743 records email turned off for an address, as its unsubscribe
// does, and restores the default when the test ends.
func optOut2743(t *testing.T, email string) {
	t.Helper()
	db := devDB(t)
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO user_notification_prefs (email, mode) VALUES ($1, 'off') ON CONFLICT (email) DO UPDATE SET mode = 'off'`, email); err != nil {
		t.Fatalf("turning email off for %s: %v", email, err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(t.Context(), `DELETE FROM user_notification_prefs WHERE email = $1`, email)
	})
}

// mail2743 is the HTML, text and headers of the message to recipient whose
// subject holds marker.
type mail2743 struct {
	html, text string
	headers    map[string]any
}

func mailTo2743(t *testing.T, marker, recipient string) mail2743 {
	t.Helper()
	var id string
	waitFor(t, "the email to "+recipient, func() (bool, string) {
		out := getJSON(t, mailpitBase()+"/api/v1/search?query="+url.QueryEscape(fmt.Sprintf("to:%s subject:%q", recipient, marker)), "")
		items, _ := out["messages"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if s, _ := m["Subject"].(string); strings.Contains(s, marker) {
				id, _ = m["ID"].(string)
				return id != "", ""
			}
		}
		return false, fmt.Sprint(len(items), " matches")
	})
	msg := getJSON(t, mailpitBase()+"/api/v1/message/"+id, "")
	html, _ := msg["HTML"].(string)
	text, _ := msg["Text"].(string)
	return mail2743{html: html, text: text, headers: getJSON(t, mailpitBase()+"/api/v1/message/"+id+"/headers", "")}
}

func (m mail2743) hasHeader(name string) bool {
	for k := range m.headers {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// footer reports whether the message carries the preferences or the
// unsubscribe line.
func (m mail2743) footer() bool {
	body := strings.ToLower(m.html + m.text)
	return strings.Contains(body, "unsubscribe") || strings.Contains(body, "manage preferences") || strings.Contains(body, "notification preferences")
}

// TestIssue2743_ADeliveryListReachesEveryAddressWithNoUnsubscribe: every
// listed address receives the message, one that turned email off included;
// neither part carries the preferences or unsubscribe line and no
// List-Unsubscribe header is sent; the channel reads back as a delivery list
// naming nobody opted out.
func TestIssue2743_ADeliveryListReachesEveryAddressWithNoUnsubscribe(t *testing.T) {
	c := connect(t)
	optOut2743(t, optedOut2743)
	view := putChannel(t, c, chanFeed2743, map[string]any{
		"kind": "email", "recipients": []string{"acc-2743-data@client.example", optedOut2743}, "delivery_list": true,
	})
	t.Cleanup(func() { deleteChannel(t, c, chanFeed2743) })
	if view["delivery_list"] != true {
		t.Fatalf("the channel did not save as a delivery list: %v", view)
	}

	marker := fmt.Sprintf("acc-2743 weekly export %d", time.Now().UnixNano())
	res := c.call("notify", map[string]any{"action": "send", "channel": chanFeed2743, "title": marker, "body": "This week's export."})
	if number(t, res, "queued") != 2 {
		t.Fatalf("a delivery list queued %v, want both addresses: %v", res["queued"], res)
	}
	if _, has := res["opted_out"]; has {
		t.Errorf("a delivery list reported opted-out recipients: %v", res)
	}
	for _, to := range []string{"acc-2743-data@client.example", optedOut2743} {
		m := mailTo2743(t, marker, to)
		if m.footer() {
			t.Errorf("the message to %s carries a preferences or unsubscribe line", to)
		}
		for _, h := range []string{"List-Unsubscribe", "List-Unsubscribe-Post"} {
			if m.hasHeader(h) {
				t.Errorf("the message to %s carries %s", to, h)
			}
		}
	}

	status, got := c.rest(http.MethodGet, "/api/v1/admin/notification-channels/"+chanFeed2743, nil)
	if opted, _ := got["opted_out"].([]any); status != http.StatusOK || got["delivery_list"] != true || opted == nil || len(opted) != 0 {
		t.Errorf("the delivery list reads back as %d %v, want delivery_list true and opted_out []", status, got)
	}
}

// TestIssue2743_AnOrdinaryListSaysWhoItDidNotReach: the opted-out address is
// not sent the message; the notify result counts it and says so; its
// recipients' messages keep the unsubscribe footer and header; and the
// administrator's channel view names the address.
func TestIssue2743_AnOrdinaryListSaysWhoItDidNotReach(t *testing.T) {
	c := connect(t)
	optOut2743(t, optedOut2743)
	putChannel(t, c, chanSales2743, map[string]any{
		"kind": "email", "recipients": []string{"acc-2743-a@example.com", "acc-2743-b@example.com", optedOut2743},
	})
	t.Cleanup(func() { deleteChannel(t, c, chanSales2743) })

	marker := fmt.Sprintf("acc-2743 sales digest %d", time.Now().UnixNano())
	res := c.call("notify", map[string]any{"action": "send", "channel": chanSales2743, "title": marker, "link": ""})
	if number(t, res, "queued") != 2 || number(t, res, "opted_out") != 1 {
		t.Fatalf("result = %v, want 2 queued and 1 opted out", res)
	}
	want := `accepted for 2 of 3 recipients of "` + chanSales2743 + `"; 1 has opted out of email and was not sent it`
	if d, _ := res["detail"].(string); d != want {
		t.Errorf("detail = %q, want %q", d, want)
	}
	m := mailTo2743(t, marker, "acc-2743-a@example.com")
	if !m.footer() || !m.hasHeader("List-Unsubscribe") {
		t.Error("an ordinary list's message lost its unsubscribe footer or header")
	}
	if strings.Contains(m.html, ">Open<") {
		t.Error(`a notify send with link "" carries an Open button`)
	}

	status, got := c.rest(http.MethodGet, "/api/v1/admin/notification-channels/"+chanSales2743, nil)
	opted, _ := got["opted_out"].([]any)
	if status != http.StatusOK || len(opted) != 1 || opted[0] != optedOut2743 {
		t.Errorf("the channel view names %v opted out, want %s", got["opted_out"], optedOut2743)
	}
}

// TestIssue2743_AScriptSendsNoLinkAndItsRunSaysWhoOptedOut: platform.notify
// with link=None and link="" sends an email with no Open button and no run
// address; with no link, the run's page; and the run's posts count the
// recipient who opted out.
func TestIssue2743_AScriptSendsNoLinkAndItsRunSaysWhoOptedOut(t *testing.T) {
	c := connect(t)
	optOut2743(t, optedOut2743)
	putChannel(t, c, chanSales2743, map[string]any{
		"kind": "email", "recipients": []string{"acc-2743-c@example.com", optedOut2743},
	})
	t.Cleanup(func() { deleteChannel(t, c, chanSales2743) })

	stamp := time.Now().UnixNano()
	marker := func(form string) string { return fmt.Sprintf("acc-2743 %s %d", form, stamp) }
	name := fmt.Sprintf("acc-2743-feed-%d", stamp)
	c.saveScript(map[string]any{
		"command": "create", "name": name, "display_name": "Acceptance 2743 feed",
		"source": fmt.Sprintf(`
def main():
    """Sends the weekly file three ways."""
    platform.notify(channel = %q, title = %q, body = "Attached.", link = None)
    platform.notify(channel = %q, title = %q, body = "Attached.", link = "")
    platform.notify(channel = %q, title = %q, body = "Attached.")
`, chanSales2743, marker("none"), chanSales2743, marker("empty"), chanSales2743, marker("default")),
	}, nil)
	t.Cleanup(func() { c.call("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	scriptID, _ := got["id"].(string)
	run := c.call("run_script", map[string]any{"name": name})
	runID, _ := run["run_id"].(string)
	if s, _ := run["status"].(string); s != "succeeded" || runID == "" {
		t.Fatalf("run: %v", run)
	}

	for _, form := range []string{"none", "empty"} {
		m := mailTo2743(t, marker(form), "acc-2743-c@example.com")
		if strings.Contains(m.html, "/runs/") || strings.Contains(m.text, "/runs/") || strings.Contains(m.html, ">Open<") {
			t.Errorf("link=%s: the email links to the run or carries an Open button", form)
		}
	}
	if m := mailTo2743(t, marker("default"), "acc-2743-c@example.com"); !strings.Contains(m.html, "/runs/"+runID) {
		t.Error("with no link, the email does not lead back to the run")
	}

	path := "/api/v1/portal/scripts/" + scriptID + "/runs/" + runID + "/posts"
	waitFor(t, "the run's posts", func() (bool, string) {
		code, out := c.rest(http.MethodGet, path, nil)
		posts, _ := out["posts"].([]any)
		if code != http.StatusOK || len(posts) != 3 {
			return false, fmt.Sprint(code, " ", len(posts), " posts")
		}
		for _, p := range posts {
			post, _ := p.(map[string]any)
			if post["opted_out"] != float64(1) || post["deliveries"] != float64(1) {
				return false, fmt.Sprint(post)
			}
		}
		return true, ""
	})
}

// TestIssue2743_OnlyAnEmailChannelIsADeliveryList: the admin API refuses
// delivery_list on a webhook channel, naming the setting.
func TestIssue2743_OnlyAnEmailChannelIsADeliveryList(t *testing.T) {
	c := connect(t)
	status, out := c.rest(http.MethodPut, "/api/v1/admin/notification-channels/acc-2743-hook", jsonBody(t, map[string]any{
		"kind": "webhook", "connection": "api-test", "delivery_list": true,
	}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), "delivery_list is for an email channel") {
		t.Errorf("a webhook delivery list answered %d %v, want 400 naming the setting", status, out)
	}
}
