//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Acceptance for #2837: the Resources page stays responsive, and every folder
// opens, in a library holding 50,000 folders and 100,000 files.
//
// Wire forms: GET /api/v1/resources/facets with query parameters scope, and
// parent as a string (empty for the library's root), the only forms a query
// string admits; the page is driven in a real browser by
// ui/e2e/resources-scale against the dev stack.
//
// The library is a folder of the Global library holding one folder per day and
// one folder per message beneath it, the shape an imap source's attachments
// reach, with one day holding 5,000 message folders. Its rows are written
// straight into the dev stack's database (the page reads metadata; nothing
// reads the files) and removed when the criteria end.

const issue2837Day = "2026-01-01"

// issue2837Seed files the library under a folder of its own and returns that
// folder.
func issue2837Seed(t *testing.T) string {
	t.Helper()
	db := platformDB2539(t)
	top := "acc-2837-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	_, err := db.ExecContext(ctx, `
		WITH msg AS (
			SELECT g, $1 || '/' ||
			       CASE WHEN g <= 5000 THEN $2 ELSE to_char(date '2026-01-02' + (g % 300), 'YYYY-MM-DD') END ||
			       '/' || md5($1 || g::text) AS path
			  FROM generate_series(1, 50000) g
		)
		INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description,
		                       mime_type, size_bytes, s3_key, uri, uploader_sub, index_policy)
		SELECT $1 || '-' || g || '-' || k, 'global', NULL, path, 'file' || k || '.bin', 'file' || k || '.bin', '',
		       'application/octet-stream', 1024, $1 || '/' || g || '/' || k,
		       'mcp://global/' || path || '/file' || k || '.bin', 'acceptance-2837', 'none'
		  FROM msg, generate_series(1, 2) k`, top, issue2837Day)
	if err != nil {
		t.Fatalf("seeding the library: %v", err)
	}
	t.Cleanup(func() { issue2837Remove(top) })
	_, err = db.ExecContext(ctx, `
		INSERT INTO resource_folders (scope, scope_id, path)
		SELECT DISTINCT 'global', NULL::text, array_to_string(parts[1:i], '/')
		  FROM (SELECT DISTINCT string_to_array(path, '/') AS parts FROM resources WHERE path LIKE $1 || '/%') p,
		       generate_subscripts(p.parts, 1) i
		ON CONFLICT DO NOTHING`, top)
	if err != nil {
		t.Fatalf("seeding the folders: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE resources; ANALYZE resource_folders`); err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	return top
}

// issue2837Remove deletes what issue2837Seed wrote.
func issue2837Remove(top string) {
	db, err := sql.Open("postgres", platformDSN2611())
	if err != nil {
		return
	}
	defer db.Close() //nolint:errcheck // cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, _ = db.ExecContext(ctx, `DELETE FROM resources WHERE scope = 'global' AND (path = $1 OR path LIKE $1 || '/%')`, top)
	_, _ = db.ExecContext(ctx, `DELETE FROM resource_folders WHERE scope = 'global' AND (path = $1 OR path LIKE $1 || '/%')`, top)
}

// issue2837Level reads one level of the Global library and how long it took.
func issue2837Level(t *testing.T, c *client, parent string) ([]map[string]any, time.Duration) {
	t.Helper()
	start := time.Now()
	status, out := c.rest(http.MethodGet, "/api/v1/resources/facets?scope=global&parent="+url.QueryEscape(parent), http.NoBody)
	took := time.Since(start)
	if status != http.StatusOK {
		t.Fatalf("reading the level %q: %d %v", parent, status, out)
	}
	raw, _ := out["folders"].([]any)
	folders := make([]map[string]any, 0, len(raw))
	for _, f := range raw {
		if m, ok := f.(map[string]any); ok {
			folders = append(folders, m)
		}
	}
	return folders, took
}

// Criterion 1: each level of the library, the day holding 5,000 folders and
// the level holding every day included, answers within a second, with counts
// at every depth and how many folders each holds.
func TestIssue2837_EachLevelOfALargeLibraryAnswersWithinASecond(t *testing.T) {
	c := connect(t)
	top := issue2837Seed(t)

	attachments, took := issue2837Level(t, c, top)
	t.Logf("level %s: %d folders in %s", top, len(attachments), took)
	if took > time.Second || len(attachments) != 302 {
		t.Fatalf("the level of every day took %s with %d folders, want under a second and the folder plus its 301 days", took, len(attachments))
	}
	if attachments[0]["path"] != top || attachments[0]["count"] != float64(100_000) || attachments[0]["subfolders"] != float64(301) {
		t.Fatalf("the folder itself: %v", attachments[0])
	}

	day, took := issue2837Level(t, c, top+"/"+issue2837Day)
	t.Logf("level %s/%s: %d folders in %s", top, issue2837Day, len(day), took)
	if took > time.Second || len(day) != 5001 {
		t.Fatalf("the day holding 5,000 folders took %s with %d folders, want under a second and 5,001", took, len(day))
	}
	if day[0]["count"] != float64(10_000) || day[1]["count"] != float64(2) || day[1]["subfolders"] != float64(0) {
		t.Fatalf("the day %v and a message folder %v", day[0], day[1])
	}

	root, took := issue2837Level(t, c, "")
	if took > time.Second {
		t.Fatalf("the library's root took %s", took)
	}
	found := false
	for _, f := range root {
		found = found || f["path"] == top
	}
	if !found {
		t.Fatalf("the library's root does not list %s", top)
	}
}

// Criterion 2: in a real browser, the page loads, the tree opens the folder
// holding 5,000 folders, and the listing scrolls through them, each
// interaction answering within a second.
func TestIssue2837_TheResourcesPageStaysResponsiveOnALargeLibrary(t *testing.T) {
	top := issue2837Seed(t)
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/resources-scale/playwright.config.ts")
	cmd.Dir = filepath.Join(root, "ui")
	cases, err := json.Marshal(map[string]string{"top": top, "day": issue2837Day})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "SCALE_BASE_URL="+baseURL(), "SCALE_API_KEY="+devAPIKey(), "SCALE_CASE="+string(cases))
	out, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", out)
	if err != nil {
		t.Fatal(fmt.Errorf("the Resources page did not stay responsive on a large library: %w", err))
	}
}
