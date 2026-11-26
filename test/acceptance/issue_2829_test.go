//go:build integration

package acceptance

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2829: a platform feedback post waited unbounded on its embedding, so
// with the embedding service busy (as right after an upgrade) the caller gave
// up and nothing was stored. A post now waits at most 5 seconds for it, then
// is stored and sent folded only by its words. Run in the embeddings
// environment, with the dev stack's embedder paused: pausing freezes the
// process with its socket open, so a request waits rather than failing fast.
//
// Wire forms: manage_feedback takes action, topic, target_type, trying_to,
// blocked_by, tool and suggestion as strings and calls as a list of strings;
// the settings route takes a JSON object. No parameter touched admits a
// second form.

// issue2829Answer is how long a post may take with the embedder stalled: the
// 5-second bound and room for the post's own writes, far under the 60 seconds
// a draft run or a client waits.
const issue2829Answer = 20 * time.Second

// TestIssue2829_APostIsStoredWhileTheEmbedderIsStalled: with the embedder
// paused, a post answers within the bound, is stored, and the administrator's
// list shows it.
func TestIssue2829_APostIsStoredWhileTheEmbedderIsStalled(t *testing.T) {
	c := connect(t)
	issue2820On(t, c)
	var ids []string
	issue2820Forget(t, &ids)
	marker := "acc2829x" + unique1579()
	trying := "Acceptance 2829 " + marker + " read the release notes table."
	resume := issue1987Pause(t)
	start := time.Now()
	isErr, text, out := issue2820Post(t, c, trying, "The embedder is stalled while the post is made.")
	took := time.Since(start)
	resume()
	if isErr {
		t.Fatalf("the post was refused: %s", text)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("the post answered no id: %s", text)
	}
	ids = append(ids, id)
	if took > issue2829Answer {
		t.Errorf("the post took %s with the embedder stalled, want under %s", took, issue2829Answer)
	}
	var stored int
	if err := devDB(t).QueryRowContext(context.Background(), `SELECT count(*) FROM platform_feedback WHERE id = $1`, id).Scan(&stored); err != nil || stored != 1 {
		t.Errorf("the post is not stored: %d rows, %v", stored, err)
	}
	status, list := c.rest(http.MethodGet, "/api/v1/admin/platform-feedback?q="+marker, nil)
	if status != http.StatusOK || !strings.Contains(asJSON(list), id) {
		t.Errorf("the administrator's list does not show the post: %d %v", status, list)
	}
}
