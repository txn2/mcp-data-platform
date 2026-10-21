//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2214: feedback thread bodies are Markdown, and a notification that
// carries the body renders it for its medium. A person the asset is shared
// with comments in Markdown through the portal's thread route; the owner's
// notification email, read from mailpit, carries it as HTML and the text part
// keeps it as written. The timeline's rendering is checked in the browser by
// the CommentBody tests; this is the half a server produces.
//
// Wire forms: the thread body's kind, target_type, asset_id, title and body
// are strings.

// TestIssue2214_ACommentEmailRendersItsMarkdown: emphasis, code and a list
// arrive as markup in the HTML part, a mention as the address it names, and a
// script tag as nothing that runs.
func TestIssue2214_ACommentEmailRendersItsMarkdown(t *testing.T) {
	owner := connect(t)
	commenter := connectAs(t, "acme-analyst-key")
	_, me := commenter.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	commenterEmail, _ := me["email"].(string)
	if commenterEmail == "" {
		t.Fatalf("the analyst key carries no email: %v", me)
	}
	assetID, marker := saveReport(t, owner, "2214 markdown")
	if status, out := owner.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares", jsonBody(t, map[string]any{
		"shared_with_email": commenterEmail, "permission": "viewer", "notify": false,
	})); status != http.StatusCreated {
		t.Fatalf("sharing with the commenter: %d %v", status, out)
	}

	body := "**Fixed** the `join` in v2.\n\n- west\n- east\n\nThanks @admin(example.com) <script>alert(1)</script>"
	status, out := commenter.rest(http.MethodPost, "/api/v1/portal/threads", jsonBody(t, map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": assetID,
		"title": fmt.Sprintf("acc-2214 %d", time.Now().UnixNano()), "body": body,
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating the thread: %d %v", status, out)
	}

	var html, text string
	waitFor(t, "the owner's notification email", func() (bool, string) {
		res := getJSON(t, mailpitBase()+"/api/v1/messages?limit=100", "")
		items, _ := res["messages"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if s, _ := m["Subject"].(string); !strings.Contains(s, marker) {
				continue
			}
			id, _ := m["ID"].(string)
			msg := getJSON(t, mailpitBase()+"/api/v1/message/"+id, "")
			html, _ = msg["HTML"].(string)
			text, _ = msg["Text"].(string)
			return true, ""
		}
		return false, "no email naming the asset yet"
	})
	for _, want := range []string{"<strong>Fixed</strong>", "<code>join</code>", "<li>west</li>", "@admin@example.com"} {
		if !strings.Contains(html, want) {
			t.Errorf("the HTML part lacks %q", want)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Error("a script tag reached the HTML part as markup")
	}
	if !strings.Contains(text, "**Fixed**") {
		t.Errorf("the text part does not keep the Markdown as written: %.300s", text)
	}
}
