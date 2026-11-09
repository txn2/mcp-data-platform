//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Issue #2580: a moderator's thread header was wider than the 448px drawer, so
// the whole thread scrolled sideways to reach the choice of who sees it and the
// delete button. A layout is only measured in a real browser, so the criteria
// run in Chromium (ui/e2e/feedback-drawer) against the dev stack.
//
// Wire forms: the browser's own clicks and navigations. The thread is opened and
// its status set through the portal API, whose bodies are JSON objects of
// strings; no parameter admits a second form.

const threads2580 = "/api/v1/portal/threads"

// browser2580 runs the feedback drawer's browser cases whose names match grep.
func browser2580(t *testing.T, grep string, extra ...string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/feedback-drawer/playwright.config.ts", "--grep", grep)
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "FEEDBACK_BASE_URL="+baseURL())
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright against %s:\n%s", baseURL(), out)
	if err != nil {
		t.Fatalf("the browser case %q failed: %v", grep, err)
	}
}

// marker2580 is text that names one thread in a list.
func marker2580(label string) string {
	return fmt.Sprintf("acc-%s-%d", label, time.Now().UnixNano())
}

// openThread2580 opens a thread through the portal API and returns its id.
func openThread2580(t *testing.T, c *client, body map[string]any) string {
	t.Helper()
	status, thread := c.rest(http.MethodPost, threads2580, jsonBody(t, body))
	id, _ := thread["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("opening a thread: %d %v", status, thread)
	}
	return id
}

// longestLabels2580 is a thread on a new asset carrying the longest kind and
// status labels a header can hold, and the text that names it.
func longestLabels2580(t *testing.T, admin *client) (asset, marker string) {
	t.Helper()
	asset = issue2290Asset(t, admin, "2580 header")
	marker = marker2580("2580")
	id := openThread2580(t, admin, map[string]any{"kind": "suggestion", "target_type": "asset", "asset_id": asset, "body": marker})
	if status, out := admin.rest(http.MethodPatch, threads2580+"/"+id, jsonBody(t, map[string]any{"status": "acknowledged"})); status != http.StatusOK {
		t.Fatalf("marking the thread Acknowledged: %d %v", status, out)
	}
	return asset, marker
}

func TestIssue2580_AModeratorsHeaderFitsTheDrawerAtBothWidths(t *testing.T) {
	asset, marker := longestLabels2580(t, connect(t))
	browser2580(t, "^.*drawer (448|375)px", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_MARKER="+marker, "FEEDBACK_ASSET="+asset)
}

func TestIssue2580_TheHeaderFitsTheItemsOwnFeedbackPanel(t *testing.T) {
	asset, marker := longestLabels2580(t, connect(t))
	browser2580(t, "^.*panel (448|375)px", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_MARKER="+marker, "FEEDBACK_ASSET="+asset)
}

func TestIssue2580_APersonWhoCannotChangeItSeesTheBadge(t *testing.T) {
	admin := connect(t)
	asset, marker := longestLabels2580(t, admin)
	issue2290Share(t, admin, asset, map[string]any{"shared_with_email": emailKCAnalyst, "permission": "viewer"})
	browser2580(t, "^.*badge (448|375)px",
		"FEEDBACK_EMAIL="+emailKCAnalyst, "FEEDBACK_PASSWORD="+passKCAnalyst, "FEEDBACK_MARKER="+marker, "FEEDBACK_VISIBILITY=Internal")
}

func TestIssue2580_AnExternalPersonSeesTheBadgeOnTheirOwnThread(t *testing.T) {
	admin := connect(t)
	_, email, session := issue2257ExternalAccount(t, admin, "2580", map[string]any{"portal": map[string]any{"feedback": true}})
	asset := issue2290Asset(t, admin, "2580 external")
	issue2290Share(t, admin, asset, map[string]any{"shared_with_email": email, "permission": "editor"})
	marker := marker2580("2580")
	if status, out := session.do(t, http.MethodPost, threads2580, map[string]any{
		"kind": "suggestion", "target_type": "asset", "asset_id": asset, "body": marker,
	}); status != http.StatusCreated {
		t.Fatalf("the external person's thread: %d %v", status, out)
	}
	browser2580(t, "^.*badge (448|375)px",
		"FEEDBACK_EMAIL="+email, "FEEDBACK_PASSWORD="+issue2290Password, "FEEDBACK_MARKER="+marker, "FEEDBACK_VISIBILITY=Shared")
}
