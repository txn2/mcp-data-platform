//go:build integration

package acceptance

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Issue #2111: a resource is indexed for search under a policy -- full,
// metadata or none -- that its writer chooses, or that the operator's
// resources.index rules give it. A pipeline writing thousands of data files
// says none once, or writes under a folder a rule maps to none, and nothing is
// embedded, read or kept for them; the folder holding them is found by search
// as one entry instead.
//
// The dev stack's rules (dev/platform.yaml) map pipelines/** to none and
// images under posters/** to metadata. The criteria write through the
// surfaces a writer uses: a managed script's platform.call to manage_resource,
// manage_resource itself, and the portal's upload route. What the index did is
// read from the rows: index_jobs holds every job the write path enqueued, and
// the resource row holds the vector and the extracted text.
//
// Wire forms: manage_resource's index is a typed string (full, metadata,
// none), sent as each value and omitted; an unknown value is refused. The
// upload form's index is a multipart text field, sent omitted, as "metadata",
// and as an unknown value. PATCH /api/v1/resources/{id} takes index as a JSON
// string. manage_resource update's path and description are strings, sent
// without a reference to describe a folder. search's intent, purpose and limit
// and sources are sent as the literal parameters an agent sends.

const (
	// issue2111Backfill is the ticket's number of files one script writes.
	issue2111Backfill = 1500
	// issue2111Indexed is how long one index job may take on the dev stack's
	// CPU-only embedder.
	issue2111Indexed = 3 * time.Minute
	// issue2111Run is how long a backfill run may take: its run_timeout.
	issue2111Run = 15 * time.Minute
	// issue2111PNG is a one-pixel image, the bytes of a poster.
	issue2111PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
)

// issue2111Stamp is a token no other run of the suite writes.
func issue2111Stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000) }

// issue2111BackfillSource is a script writing count daily files into folder
// through manage_resource, saying index when it is non-empty.
func issue2111BackfillSource(index string) string {
	say := ""
	if index != "" {
		say = fmt.Sprintf("            \"index\": %q,\n", index)
	}
	return `def main():
    """Writes a backfill of daily files into one folder."""
    folder = run.params["folder"]
    count = run.params["count"]
    for i in range(count):
        platform.call("manage_resource", {
            "action": "create",
            "if_exists": "replace",
            "path": folder,
            "filename": "day-%04d.csv" % i,
            "display_name": "Backfill day %d" % i,
            "description": "One day of the acceptance backfill.",
            "content": "day,units\n%d,%d\n" % (i, i * 3),
            "content_type": "text/csv",
` + say + `        })
    platform.result({"written": count})
`
}

// issue2111RunBackfill saves the script and runs it for the ticket's count of
// files, returning the folder it wrote.
func issue2111RunBackfill(t *testing.T, c *client, index, folder string) {
	t.Helper()
	name := "acc-2111-backfill-" + issue2111Stamp()
	params := []any{
		map[string]any{"name": "folder", "type": "string", "required": true, "description": "Where the files land."},
		map[string]any{"name": "count", "type": "int", "required": true, "description": "How many files."},
	}
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2111: a backfill.",
		"source": issue2111BackfillSource(index), "params": params,
	}, map[string]any{"folder": folder + "-draft", "count": 2})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	issue2111CleanFolder(t, c, folder+"-draft")
	issue2111CleanFolder(t, c, folder)

	run := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"folder": folder, "count": issue2111Backfill},
		"wait_seconds": int(issue2111Run / time.Second),
	})
	// run_script waits at most five minutes; a longer run goes on in the
	// background and is read with get_run.
	runID, _ := run["run_id"].(string)
	deadline := time.Now().Add(issue2111Run)
	for run["status"] == "pending" || run["status"] == "running" {
		if time.Now().After(deadline) {
			t.Fatalf("the backfill run did not end within %s: %v", issue2111Run, run)
		}
		time.Sleep(5 * time.Second)
		run = c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
	}
	if run["status"] != "succeeded" {
		t.Fatalf("the backfill run did not succeed: %v", run)
	}
	t.Logf("backfill run %s wrote %d files in %vms", runID, issue2111Backfill, run["duration_ms"])
}

// issue2111CleanFolder removes every file the test wrote under folder.
func issue2111CleanFolder(t *testing.T, c *client, folder string) {
	t.Helper()
	db := issue2111DB(t)
	t.Cleanup(func() {
		rows, err := db.Query(`SELECT id FROM resources WHERE path = $1`, folder)
		if err != nil {
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		_ = rows.Close()
		for _, id := range ids {
			_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		}
	})
}

func issue2111DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// issue2111Folder is what the rows of one folder say about its index.
type issue2111Folder struct {
	files, none, explicit, vectors, withText, textBytes, jobs int
	ids                                                       []string
}

func issue2111ReadFolder(t *testing.T, db *sql.DB, folder string) issue2111Folder {
	t.Helper()
	var f issue2111Folder
	var ids pq.StringArray
	err := db.QueryRow(`
		SELECT count(*),
		       count(*) FILTER (WHERE index_policy = 'none'),
		       count(*) FILTER (WHERE index_policy_explicit),
		       count(*) FILTER (WHERE embedding IS NOT NULL),
		       count(*) FILTER (WHERE content_text <> ''),
		       COALESCE(sum(length(content_text)), 0),
		       COALESCE(array_agg(id), '{}')
		FROM resources WHERE path = $1`, folder).
		Scan(&f.files, &f.none, &f.explicit, &f.vectors, &f.withText, &f.textBytes, &ids)
	if err != nil {
		t.Fatalf("reading folder %s: %v", folder, err)
	}
	f.ids = ids
	if err := db.QueryRow(`SELECT count(*) FROM index_jobs WHERE source_kind = 'resources' AND source_id = ANY($1)`,
		pq.Array(f.ids)).Scan(&f.jobs); err != nil {
		t.Fatalf("counting the folder's index jobs: %v", err)
	}
	return f
}

// issue2111AssertNothingIndexed is the ticket's first criterion over one
// folder: every file is there, under none, and no index job, vector or
// extracted byte exists for any of them.
func issue2111AssertNothingIndexed(t *testing.T, db *sql.DB, folder string, explicit bool) issue2111Folder {
	t.Helper()
	f := issue2111ReadFolder(t, db, folder)
	if f.files != issue2111Backfill {
		t.Fatalf("folder %s holds %d files; the run wrote %d", folder, f.files, issue2111Backfill)
	}
	if f.none != issue2111Backfill {
		t.Errorf("%d of %d files are under none", f.none, f.files)
	}
	if want := map[bool]int{true: issue2111Backfill, false: 0}[explicit]; f.explicit != want {
		t.Errorf("%d files record their policy as the writer's choice; want %d", f.explicit, want)
	}
	if f.jobs != 0 {
		t.Errorf("the write path enqueued %d resource index jobs; want none", f.jobs)
	}
	if f.vectors != 0 || f.withText != 0 || f.textBytes != 0 {
		t.Errorf("files under none were indexed: %d vectors, %d with extracted text, %d bytes of text",
			f.vectors, f.withText, f.textBytes)
	}
	return f
}

// TestIssue2111_AScriptSayingNoneIndexesNothing: a script writing 1,500
// resources with index "none" enqueues no resource index job, reads and keeps
// no content, and embeds nothing.
func TestIssue2111_AScriptSayingNoneIndexesNothing(t *testing.T) {
	c := connectFor(t, issue2111Run+5*time.Minute)
	db := issue2111DB(t)
	folder := "acc-2111-backfill-" + issue2111Stamp()
	issue2111RunBackfill(t, c, "none", folder)
	issue2111AssertNothingIndexed(t, db, folder, true)
}

// TestIssue2111_ARuleMappingAFolderToNoneDoesTheSame: the same script with no
// index argument, writing under pipelines/** which the rules map to none,
// leaves the same rows.
func TestIssue2111_ARuleMappingAFolderToNoneDoesTheSame(t *testing.T) {
	c := connectFor(t, issue2111Run+5*time.Minute)
	db := issue2111DB(t)
	folder := "pipelines/acc-2111-" + issue2111Stamp()
	issue2111RunBackfill(t, c, "", folder)
	issue2111AssertNothingIndexed(t, db, folder, false)
}

// issue2111Search runs one search an agent would and returns its answer as
// text.
func issue2111Search(c *client, intent string) string {
	out := c.call("search", map[string]any{
		"intent":  intent,
		"sources": []any{"resources"},
		"limit":   20,
		"purpose": "Acceptance #2111: what search returns for a file's policy.",
	})
	raw, _ := json.Marshal(out)
	return string(raw)
}

// issue2111AwaitSearch searches until match holds for the answer, since an
// index job runs off the write path.
func issue2111AwaitSearch(t *testing.T, c *client, intent string, match func(string) bool) string {
	t.Helper()
	return issue2111AwaitSearchWithin(t, c, intent, issue2111Indexed, match)
}

// issue2111AwaitSearchWithin is issue2111AwaitSearch with its own wait.
func issue2111AwaitSearchWithin(t *testing.T, c *client, intent string, wait time.Duration, match func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		got := issue2111Search(c, intent)
		if match(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("search for %q did not return what was expected within %s: %s", intent, wait, got)
		}
		time.Sleep(2 * time.Second)
	}
}

// TestIssue2111_TheFolderOfUnindexedFilesIsOneSearchResult: a folder holding
// files that are not indexed one by one is found by search as one entry, by
// the description written for it, and none of its files is.
func TestIssue2111_TheFolderOfUnindexedFilesIsOneSearchResult(t *testing.T) {
	c := connectFor(t, issue2111Run+5*time.Minute)
	db := issue2111DB(t)
	stamp := issue2111Stamp()
	folder := "pipelines/acc-2111-folder-" + stamp
	issue2111RunBackfill(t, c, "", folder)
	f := issue2111AssertNothingIndexed(t, db, folder, false)

	token := "nltvbackfill" + stamp
	described := c.call("manage_resource", map[string]any{
		"action": "update", "path": folder,
		"description": "Daily schedule exports of the " + token + " station backfill.",
	})
	if raw, _ := json.Marshal(described); !strings.Contains(string(raw), folder) {
		t.Fatalf("describing the folder did not name it: %s", raw)
	}

	// A folder job may be running from the backfill's last write when the
	// description lands; the queue keeps no second job while one runs, and the
	// reconciler re-embeds the entry on its next sweep.
	got := issue2111AwaitSearchWithin(t, c, token, indexjobs.ReconcilerInterval+issue2111Indexed,
		func(s string) bool { return strings.Contains(s, folder) })
	if n := strings.Count(got, `:`+folder+`"`); n != 1 {
		t.Errorf("search returned the folder %d times; want one entry: %s", n, got)
	}
	if !strings.Contains(got, "Its files are not in search one by one") {
		t.Errorf("the folder hit does not say how its files are listed: %s", got)
	}
	for _, id := range f.ids {
		if strings.Contains(got, id) {
			t.Fatalf("search returned file %s of a folder that is not indexed file by file: %s", id, got)
		}
	}
	var jobs int
	if err := db.QueryRow(`SELECT count(*) FROM index_jobs WHERE source_kind = 'resource_folders' AND source_id LIKE $1`,
		"%:"+folder).Scan(&jobs); err != nil {
		t.Fatalf("counting the folder's index jobs: %v", err)
	}
	t.Logf("folder %s: %d files, %d folder index jobs", folder, f.files, jobs)
	if jobs == 0 {
		t.Error("the folder was found with no folder index job recorded for it")
	}
}

// issue2111AwaitVector waits for a resource's index job to write its vector
// and settle its content, and returns the row's policy and extracted text.
func issue2111AwaitVector(t *testing.T, db *sql.DB, id string) (policy string, explicit bool, text string) {
	t.Helper()
	// A write that lands while the file's last index job runs is picked up by
	// the reconciler's next sweep, so the wait is one sweep and one job.
	deadline := time.Now().Add(indexjobs.ReconcilerInterval + issue2111Indexed)
	for {
		err := db.QueryRow(`SELECT index_policy, index_policy_explicit, content_text FROM resources
			WHERE id = $1 AND embedding IS NOT NULL AND content_indexed_at IS NOT NULL`, id).Scan(&policy, &explicit, &text)
		if err == nil {
			return policy, explicit, text
		}
		if err != sql.ErrNoRows {
			t.Fatalf("reading resource %s: %v", id, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("resource %s was not indexed within %s", id, indexjobs.ReconcilerInterval+issue2111Indexed)
		}
		time.Sleep(time.Second)
	}
}

// TestIssue2111_PostersAScriptWritesAreSearchableByTitleAndTags: a pipeline
// writing posters under a folder the rules map to metadata gets each one
// searchable by its title and tags, though a script wrote them.
func TestIssue2111_PostersAScriptWritesAreSearchableByTitleAndTags(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	db := issue2111DB(t)
	stamp := issue2111Stamp()
	folder := "posters/acc-2111-" + stamp
	title := "Harborlights" + stamp
	name := "acc-2111-posters-" + stamp
	source := `def poster(show):
    """Files one show's key art."""
    platform.call("manage_resource", {
        "action": "create",
        "if_exists": "replace",
        "path": "` + folder + `",
        "filename": show.lower() + ".png",
        "display_name": show + " poster",
        "description": "Key art for " + show + ".",
        "tags": ["poster", "season-` + stamp + `"],
        "content_base64": "` + issue2111PNG + `",
        "content_type": "image/png",
    })

def main():
    """Writes the season's posters."""
    poster("` + title + `")
    poster("Quietcoast` + stamp + `")
    platform.result({"posters": 2})
`
	c.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2111: posters.", "source": source,
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	issue2111CleanFolder(t, c, folder)
	if run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120}); run["status"] != "succeeded" {
		t.Fatalf("the poster run did not succeed: %v", run)
	}

	var id string
	if err := db.QueryRow(`SELECT id FROM resources WHERE path = $1 AND filename = $2`, folder,
		strings.ToLower(title)+".png").Scan(&id); err != nil {
		t.Fatalf("finding the poster the run wrote: %v", err)
	}
	policy, explicit, text := issue2111AwaitVector(t, db, id)
	if policy != "metadata" || explicit {
		t.Errorf("the poster is under %q (writer's choice %v); want metadata from the rule", policy, explicit)
	}
	if text != "" {
		t.Errorf("a file under metadata kept extracted text: %q", text)
	}
	issue2111AwaitSearch(t, c, title+" poster", func(s string) bool { return strings.Contains(s, id) })
	issue2111AwaitSearch(t, c, "season-"+stamp, func(s string) bool { return strings.Contains(s, id) })
}

// issue2111StyleGuide is a PDF whose text is token: the dev fixture with its
// one line of text replaced by a token of the same length, so its byte
// offsets still hold.
func issue2111StyleGuide(t *testing.T, token string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "issue_1794_orange.pdf"))
	if err != nil {
		t.Fatalf("reading the PDF fixture: %v", err)
	}
	const line = "First page, orange"
	if len(token) != len(line) {
		t.Fatalf("the token %q must be %d characters", token, len(line))
	}
	return bytes.Replace(raw, []byte(line), []byte(token), 1)
}

// issue2111Upload uploads a file through the portal's upload route with the
// given form fields.
func issue2111Upload(t *testing.T, c *client, filename, contentType string, content []byte, fields map[string]string) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := createPart1628(w, filename, contentType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, baseURL()+"/api/v1/resources", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("uploading %s: %v", filename, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if id, _ := out["id"].(string); id != "" {
		t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	}
	return res.StatusCode, out
}

// TestIssue2111_AnUploadedPDFIsSearchableByItsContents: an uploaded PDF style
// guide is indexed in full by default and found by a phrase inside it; the
// upload form's index field is honored and an unknown value is refused.
func TestIssue2111_AnUploadedPDFIsSearchableByItsContents(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	db := issue2111DB(t)
	stamp := issue2111Stamp()
	token := fmt.Sprintf("kerningrule%07s", stamp[len(stamp)-7:])
	fields := map[string]string{
		"scope": "global", "path": "acc-2111-guides", "display_name": "Style guide " + stamp,
		"description": "Acceptance #2111: the brand style guide.",
	}
	status, out := issue2111Upload(t, c, "style-guide-"+stamp+".pdf", "application/pdf", issue2111StyleGuide(t, token), fields)
	if status != http.StatusCreated {
		t.Fatalf("uploading the style guide answered %d %v", status, out)
	}
	id, _ := out["id"].(string)
	if out["index_policy"] != "full" || out["index_policy_explicit"] != false {
		t.Errorf("the upload reports policy %v (writer's choice %v); want full from the default", out["index_policy"], out["index_policy_explicit"])
	}
	policy, _, text := issue2111AwaitVector(t, db, id)
	if policy != "full" || !strings.Contains(text, token) {
		t.Fatalf("the style guide is under %q with extracted text %q; want full with %s in it", policy, text, token)
	}
	issue2111AwaitSearch(t, c, token, func(s string) bool { return strings.Contains(s, id) })

	fields["display_name"] = "Style guide summary " + stamp
	fields["index"] = "metadata"
	status, out = issue2111Upload(t, c, "style-summary-"+stamp+".pdf", "application/pdf", issue2111StyleGuide(t, token), fields)
	if status != http.StatusCreated || out["index_policy"] != "metadata" || out["index_policy_explicit"] != true {
		t.Errorf("an upload saying index=metadata answered %d with policy %v (writer's choice %v)",
			status, out["index_policy"], out["index_policy_explicit"])
	}
	fields["display_name"] = "Style guide refused " + stamp
	fields["index"] = "everything"
	if status, out = issue2111Upload(t, c, "style-refused-"+stamp+".pdf", "application/pdf", issue2111StyleGuide(t, token), fields); status != http.StatusBadRequest {
		t.Errorf("an upload saying index=everything answered %d %v; want 400", status, out)
	}
}

// issue2111Create files one resource through manage_resource.
func issue2111Create(t *testing.T, c *client, args map[string]any) map[string]any {
	t.Helper()
	sent := map[string]any{"action": "create", "content_type": "text/csv", "description": "Acceptance #2111."}
	for k, v := range args {
		sent[k] = v
	}
	out := c.call("manage_resource", sent)
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return out
}

// TestIssue2111_ANoneResourceIsStillAFile: a resource under none is listed,
// fetched, versioned, held to its library's permissions, and referenced by an
// asset and attached to a prompt.
func TestIssue2111_ANoneResourceIsStillAFile(t *testing.T) {
	c := connect(t)
	stamp := issue2111Stamp()
	folder := "acc-2111-none-" + stamp
	created := issue2111Create(t, c, map[string]any{
		"filename": "rates.csv", "display_name": "Rates " + stamp, "path": folder,
		"content": "region,rate\nnorth,3\n", "index": "none",
	})
	id, _ := created["resource_id"].(string)
	reference, _ := created["reference"].(string)
	uri, _ := created["uri"].(string)

	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK || row["index_policy"] != "none" || row["index_policy_explicit"] != true {
		t.Fatalf("GET the resource answered %d with policy %v (writer's choice %v)", status, row["index_policy"], row["index_policy_explicit"])
	}

	listed := c.call("manage_resource", map[string]any{"action": "list", "path": folder})
	if raw, _ := json.Marshal(listed); !strings.Contains(string(raw), id) {
		t.Errorf("manage_resource list does not list a file under none: %s", raw)
	}
	fetched := c.call("fetch", map[string]any{
		"reference": reference, "purpose": "Acceptance #2111: a file under none is still fetched.",
	})
	if raw, _ := json.Marshal(fetched); !strings.Contains(string(raw), "north,3") {
		t.Errorf("fetch did not return the contents of a file under none: %s", raw)
	}
	replaced := c.call("manage_resource", map[string]any{
		"action": "replace_content", "reference": reference, "content": "region,rate\nnorth,4\n",
	})
	if v, _ := replaced["version"].(float64); v != 2 {
		t.Errorf("replacing a file under none recorded version %v; want 2: %v", replaced["version"], replaced)
	}
	if _, row = c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody); row["index_policy"] != "none" {
		t.Errorf("a replacement changed the file's policy to %v; the writer chose none", row["index_policy"])
	}

	other := connectAs(t, devOwnerAPIKey)
	res, text, err := other.callRaw("manage_resource", map[string]any{"action": "get", "reference": reference})
	if err != nil {
		t.Fatalf("another person's get: %v", err)
	}
	var seen map[string]any
	_ = json.Unmarshal([]byte(text), &seen)
	if !res.IsError && seen["found"] != false {
		t.Errorf("another person read a file in someone else's library: %s", text)
	}

	saved := c.call("save_asset", map[string]any{
		"name": "Acceptance 2111 rates " + stamp, "content_type": "text/html",
		"content":    fmt.Sprintf("<h1>Rates</h1><script>fetch(%q)</script>", uri),
		"references": []any{uri},
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("an asset referencing a file under none was not saved: %v", saved)
	}
	t.Cleanup(func() { c.call("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })

	promptName := "acceptance-2111-" + stamp
	prompt := c.call("manage_prompt", map[string]any{
		"command": "create", "name": promptName, "display_name": "Acceptance 2111",
		"description": "Acceptance 2111 fixture.", "content": "Use the attached rates.", "scope": "personal",
	})
	promptID, _ := prompt["id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_prompt", map[string]any{"command": "delete", "name": promptName}) })
	if status, out := c.rest(http.MethodPost, "/api/v1/portal/prompts/"+promptID+"/attachments",
		jsonBody(t, map[string]any{"resource_id": id})); status != http.StatusCreated && status != http.StatusOK {
		t.Errorf("attaching a file under none to a prompt answered %d %v", status, out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/prompts/"+promptID+"/attachments/"+id, nil) })
}

// TestIssue2111_ThePolicyIsVisibleAndChangeable: each resource reports its
// policy, a writer changes it through manage_resource update and PATCH, an
// unknown value is refused, and the admin indexing counts report the policies.
func TestIssue2111_ThePolicyIsVisibleAndChangeable(t *testing.T) {
	c := connect(t)
	stamp := issue2111Stamp()
	folder := "acc-2111-policy-" + stamp
	ids := map[string]string{}
	for _, index := range []string{"full", "metadata", "none"} {
		out := issue2111Create(t, c, map[string]any{
			"filename": index + ".csv", "display_name": "Policy " + index + " " + stamp, "path": folder,
			"content": "a,b\n1,2\n", "index": index,
		})
		ids[index], _ = out["resource_id"].(string)
		if _, row := c.rest(http.MethodGet, "/api/v1/resources/"+ids[index], http.NoBody); row["index_policy"] != index {
			t.Errorf("a file created with index=%s reports %v", index, row["index_policy"])
		}
	}
	defaulted := issue2111Create(t, c, map[string]any{
		"filename": "defaulted.md", "display_name": "Policy default " + stamp, "path": folder,
		"content": "# Notes\n", "content_type": "text/markdown",
	})
	defaultID, _ := defaulted["resource_id"].(string)
	if _, row := c.rest(http.MethodGet, "/api/v1/resources/"+defaultID, http.NoBody); row["index_policy"] != "full" || row["index_policy_explicit"] != false {
		t.Errorf("a markdown file with no index reports %v (writer's choice %v); want full from the default", row["index_policy"], row["index_policy_explicit"])
	}

	res, text, err := c.callRaw("manage_resource", map[string]any{
		"action": "create", "filename": "bad.csv", "display_name": "Bad " + stamp, "path": folder,
		"content": "a\n1\n", "content_type": "text/csv", "description": "Acceptance #2111.", "index": "everything",
	})
	if err != nil || !res.IsError || !strings.Contains(text, "[full metadata none]") {
		t.Errorf("manage_resource create with index=everything answered %v %s; want a refusal naming the values", err, text)
	}

	updated := c.call("manage_resource", map[string]any{"action": "update", "reference": "mcp:resource:" + ids["none"], "index": "metadata"})
	if raw, _ := json.Marshal(updated); !strings.Contains(string(raw), "metadata") {
		t.Errorf("manage_resource update index=metadata did not report it: %s", raw)
	}
	status, patched := c.rest(http.MethodPatch, "/api/v1/resources/"+ids["full"], jsonBody(t, map[string]any{"index": "none"}))
	if status != http.StatusOK || patched["index_policy"] != "none" {
		t.Errorf("PATCH index=none answered %d with policy %v", status, patched["index_policy"])
	}
	if status, out := c.rest(http.MethodPatch, "/api/v1/resources/"+ids["full"], jsonBody(t, map[string]any{"index": "most"})); status != http.StatusBadRequest {
		t.Errorf("PATCH index=most answered %d %v; want 400", status, out)
	}

	status, state := c.rest(http.MethodGet, "/api/v1/admin/resources/index", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/resources/index answered %d %v", status, state)
	}
	counts, _ := state["counts"].(map[string]any)
	for _, key := range []string{"full", "metadata", "none", "explicit", "content_bytes", "folders"} {
		if _, ok := counts[key].(float64); !ok {
			t.Errorf("the indexing counts carry no %s: %v", key, counts)
		}
	}
	if number(t, counts, "none") < 1 || number(t, counts, "metadata") < 1 || number(t, counts, "full") < 1 {
		t.Errorf("the indexing counts do not include the files this test filed: %v", counts)
	}
}

// TestIssue2111_ReclassifyingAppliesTheRulesToExistingRows: a file written
// under pipelines/** before the rule existed -- full, with a vector and its
// text kept -- drops its vector and its text when an administrator
// reclassifies, and a writer's own choice is left alone.
func TestIssue2111_ReclassifyingAppliesTheRulesToExistingRows(t *testing.T) {
	c := connect(t)
	db := issue2111DB(t)
	stamp := issue2111Stamp()
	folder := "pipelines/acc-2111-old-" + stamp
	ruled := issue2111Create(t, c, map[string]any{
		"filename": "old.csv", "display_name": "Old backfill " + stamp, "path": folder, "content": "a,b\n1,2\n",
	})
	chosen := issue2111Create(t, c, map[string]any{
		"filename": "kept.csv", "display_name": "Kept " + stamp, "path": folder, "content": "a,b\n1,2\n", "index": "full",
	})
	ruledID, _ := ruled["resource_id"].(string)
	chosenID, _ := chosen["resource_id"].(string)

	// The row as a release before #2111 left it: indexed in full, its text
	// kept. Only the database can put a row back in time.
	if _, err := db.Exec(`UPDATE resources SET index_policy = 'full', index_policy_explicit = false,
		content_text = 'a,b 1,2 kept before the rule', embedding = array_fill(0.01, ARRAY[768])::vector
		WHERE id = $1`, ruledID); err != nil {
		t.Fatalf("writing the old row: %v", err)
	}

	status, started := c.rest(http.MethodPost, "/api/v1/admin/resources/index/reclassify", http.NoBody)
	if status != http.StatusAccepted {
		t.Fatalf("POST reclassify answered %d %v", status, started)
	}
	deadline := time.Now().Add(5 * time.Minute)
	var run map[string]any
	for {
		_, state := c.rest(http.MethodGet, "/api/v1/admin/resources/index", http.NoBody)
		run, _ = state["last_run"].(map[string]any)
		if run != nil && run["finished_at"] != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reclassification did not finish: %v", state)
		}
		time.Sleep(time.Second)
	}
	if run["error"] != nil {
		t.Fatalf("the reclassification failed: %v", run)
	}
	t.Logf("reclassification: %v", run["result"])

	var policy, text string
	var vector bool
	if err := db.QueryRow(`SELECT index_policy, content_text, embedding IS NOT NULL FROM resources WHERE id = $1`, ruledID).
		Scan(&policy, &text, &vector); err != nil {
		t.Fatal(err)
	}
	if policy != "none" || text != "" || vector {
		t.Errorf("the old row is under %q with %d bytes of text and vector=%v; want none with neither", policy, len(text), vector)
	}
	if err := db.QueryRow(`SELECT index_policy FROM resources WHERE id = $1`, chosenID).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if policy != "full" {
		t.Errorf("a writer's choice of full was changed to %q by the rules", policy)
	}
}
