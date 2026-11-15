//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2638: a resource reclassification on a deployment with no embedding
// provider queues no index jobs, since no worker exists to run them, and its
// answers say why. With an embedder it queues the jobs it owes, as before.
//
// Wire forms: POST /api/v1/admin/resources/index/reclassify takes no body;
// GET /api/v1/admin/resources/index takes no parameters. manage_resource's
// action, filename, display_name, path, content, content_type and description
// are typed strings, sent as one literal tools/call form each.
//
// Stack: `make dev` for the no-embedder criterion (the plain stack runs the
// noop provider); the embeddings environment (DEV_EMBEDDING_PROVIDER=ollama)
// for the criterion with an embedder.

// issue2638Reclassify makes the resource id unindexed as a release before the
// rules would have left it, reclassifies, waits for the run to finish, and
// returns the POST answer, the final index state, and how many index jobs
// were queued for the resource or its folder since the run started.
func issue2638Reclassify(t *testing.T, c *client) (posted, state map[string]any, jobs int) {
	t.Helper()
	db := issue2111DB(t)
	stamp := issue2111Stamp()
	folder := "acc-2638/" + stamp
	made := issue2111Create(t, c, map[string]any{
		"filename": "rows.csv", "display_name": "Rows " + stamp, "path": folder, "content": "a,b\n1,2\n",
	})
	id, _ := made["resource_id"].(string)
	// Unindexed and not a writer's choice: the rules index it again, which
	// owes the resource's job and its folder's.
	if _, err := db.Exec(`UPDATE resources SET index_policy = 'none', index_policy_explicit = false WHERE id = $1`, id); err != nil {
		t.Fatalf("writing the row: %v", err)
	}
	var before int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM index_jobs`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Minute)
	for {
		var status int
		status, posted = c.rest(http.MethodPost, "/api/v1/admin/resources/index/reclassify", http.NoBody)
		if status == http.StatusAccepted {
			break
		}
		// Another criterion's reclassification is walking the table.
		if status != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("POST reclassify answered %d %v", status, posted)
		}
		time.Sleep(time.Second)
	}
	for {
		_, state = c.rest(http.MethodGet, "/api/v1/admin/resources/index", http.NoBody)
		if run, _ := state["last_run"].(map[string]any); run != nil && run["finished_at"] != nil {
			if run["error"] != nil {
				t.Fatalf("the reclassification failed: %v", run)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reclassification did not finish: %v", state)
		}
		time.Sleep(time.Second)
	}
	var policy string
	if err := db.QueryRow(`SELECT index_policy FROM resources WHERE id = $1`, id).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if policy == "none" {
		t.Fatalf("the reclassification left the resource unindexed; the criterion needs a rule that indexes it")
	}
	if err := db.QueryRow(`SELECT count(*) FROM index_jobs WHERE id > $1 AND
		((source_kind = 'resources' AND source_id = $2) OR (source_kind = 'resource_folders' AND source_id LIKE $3))`,
		before, id, "%"+stamp+"%").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	return posted, state, jobs
}

// TestIssue2638_NoEmbedderQueuesNoIndexWorkAndSaysWhy: on the plain stack,
// which has no embedder, a reclassification that indexes a resource again
// queues no index job, and both the POST answer and the index state say no
// index work is owed because the deployment has no embedder.
func TestIssue2638_NoEmbedderQueuesNoIndexWorkAndSaysWhy(t *testing.T) {
	c := connect(t)
	posted, state, jobs := issue2638Reclassify(t, c)
	if jobs != 0 {
		t.Errorf("the reclassification queued %d index jobs on a deployment with no embedder", jobs)
	}
	for name, body := range map[string]map[string]any{"the POST answer": posted, "the index state": state} {
		reason, _ := body["no_index_reason"].(string)
		if !strings.Contains(reason, "no embedding provider") {
			t.Errorf("%s says %q; want why no index work is owed", name, reason)
		}
	}
	run, _ := state["last_run"].(map[string]any)
	if reason, _ := run["no_index_reason"].(string); reason == "" {
		t.Errorf("the last run carries no no_index_reason: %v", run)
	}
}

// TestIssue2638_AnEmbedderQueuesTheJobsItOwes: with an embedder, the same
// reclassification queues the resource's job, and nothing says no index work
// is owed.
func TestIssue2638_AnEmbedderQueuesTheJobsItOwes(t *testing.T) {
	c := connect(t)
	posted, state, jobs := issue2638Reclassify(t, c)
	if jobs == 0 {
		t.Errorf("the reclassification queued no index job for a resource it indexed again")
	}
	if posted["no_index_reason"] != nil || state["no_index_reason"] != nil {
		t.Errorf("a deployment with an embedder says no index work is owed: %v / %v", posted["no_index_reason"], state["no_index_reason"])
	}
}
