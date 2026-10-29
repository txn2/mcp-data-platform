//go:build integration

package acceptance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"
)

// Issue #2367: a resource deleted while its tile was being drawn logged
// ERROR "thumbnails: recording a resource's tile failed ... resource not
// found", which operators and rollout soaks alert on, for a file that was
// meant to be deleted and owed no tile. The draw now ends quietly and removes
// the tiles it stored for the file.
//
// The delete is made once the tile worker has claimed the file, read from the
// row's lease in the database, so it lands while the tile is being drawn, as
// in the ticket (write, then delete 0.6 s later, ERROR 1.2 s after that).
//
// Wire forms: manage_resource's action, filename, display_name, path,
// description, content and content_type as JSON strings, and its delete's
// action and reference as JSON strings.

// issue2367Settle is how long the criterion watches the replicas' logs after
// the delete: well past the dev stack's draw of a small markdown file (about a
// second), which is the window the ticket's ERROR was written in. A discarded
// draw records nothing, by design, so there is no row to wait on instead.
const issue2367Settle = 15 * time.Second

// issue2367ErrorLines returns the ERROR lines in the replicas' stderr that
// name the resource.
func issue2367ErrorLines(t *testing.T, id string) []string {
	t.Helper()
	var found []string
	for _, p := range stderrLogFiles(t) {
		f, err := os.Open(p) // #nosec G304 -- the dev stack's own log files, from dev/.dev-ports.env
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.Contains(line, id) || !strings.HasPrefix(line, "{") {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) == nil && rec["level"] == "ERROR" {
				found = append(found, line)
			}
		}
		_ = f.Close()
	}
	return found
}

func TestIssue2367_AResourceDeletedWhileItsTileIsDrawnEndsQuietly(t *testing.T) {
	c := connect(t)
	db := issue2257DB(t)
	stamp := unique1787()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     "acc-2367-" + stamp + ".md",
		"display_name": "acc-2367-" + stamp,
		"path":         "acceptance-2367",
		"description":  "Acceptance #2367: a file deleted while its tile is drawn.",
		"content":      "# acc-2367\n\nStaged, registered and deleted within a second.\n",
		"content_type": "text/markdown",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	dir := path.Dir(resourceS3Key1775(t, c, id)) + "/"

	claimed := false
	for deadline := time.Now().Add(30 * time.Second); !claimed && time.Now().Before(deadline); {
		if err := db.QueryRowContext(t.Context(),
			`SELECT thumbnail_claimed_until IS NOT NULL FROM resources WHERE id = $1`, id).Scan(&claimed); err != nil {
			t.Fatalf("reading the resource's lease: %v", err)
		}
		if !claimed {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !claimed {
		t.Fatal("the tile worker did not claim the resource within 30s, so the delete would not land mid-draw")
	}
	c.call("manage_resource", map[string]any{"action": "delete", "reference": "mcp:resource:" + id})

	for deadline := time.Now().Add(issue2367Settle); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if lines := issue2367ErrorLines(t, id); len(lines) > 0 {
			t.Fatalf("a resource deleted while its tile was drawn logged ERROR:\n%s", strings.Join(lines, "\n"))
		}
	}
	listed := c.call("s3_list", map[string]any{
		"connection": "dev-resources", "bucket": "managed-resources", "prefix": dir,
		"purpose": "Acceptance #2367: a deleted resource's directory holds no tile.",
	})
	if strings.Contains(fmt.Sprint(listed), "thumbnail") {
		t.Errorf("the deleted resource's directory %s still holds a tile: %v", dir, listed)
	}
}
