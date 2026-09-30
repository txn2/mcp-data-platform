//go:build integration

package acceptance

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Issue #1988: a script archiving a backfill of CSV reports queued one
// resource index job per file, every one embedding a dense prefix of raw
// values, and a CPU-only embedder spent minutes on them while every search and
// capture waited behind the backlog. A table of values (CSV, TSV, NDJSON) is
// now embedded on its metadata alone while its contents stay lexically
// searchable, and on each replica the index worker yields to an interactive
// embed in flight.
//
// The criteria write files through manage_resource, the tool a script's writes
// and an agent's reach the resource store through, against the dev stack's own
// CPU-only Ollama. What the index embedded is read from the row the index job
// wrote: the text hash stored beside the vector is the SHA-256 of exactly the
// text the worker sent the embedder.
//
// Wire forms: manage_resource's action, filename, display_name, path,
// description, content and content_type are typed strings and tags an array
// of strings; search's intent and purpose are strings and limit a number, each
// admitting one JSON form, and sources an array of strings, sent as literal
// tools/call parameters.

// issue1988Indexed is how long the index job of one file may take.
const issue1988Indexed = 3 * time.Minute

// issue1988Create files one resource through manage_resource and returns its
// id.
func issue1988Create(t *testing.T, c *client, filename, contentType, content string) string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     filename,
		"display_name": strings.TrimSuffix(filename, "."+strings.SplitN(filename, ".", 2)[1]),
		"path":         "acceptance-1988",
		"description":  "Acceptance #1988: a daily report a pipeline archives.",
		"content":      content,
		"content_type": contentType,
		"tags":         []any{"acceptance-1988"},
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id
}

// issue1988Row is what the index job left on a resource's row.
type issue1988Row struct {
	meta        resource.Resource
	contentText string
	textHash    []byte
}

// issue1988AwaitIndexed waits until the resource's index job has written its
// vector and settled its content, and returns the row.
func issue1988AwaitIndexed(t *testing.T, db *sql.DB, id string) issue1988Row {
	t.Helper()
	deadline := time.Now().Add(issue1988Indexed)
	for {
		var row issue1988Row
		var tags pq.StringArray
		err := db.QueryRow(`
			SELECT display_name, description, path, filename, tags, content_text, embedding_text_hash
			FROM resources
			WHERE id = $1 AND embedding IS NOT NULL AND content_indexed_at IS NOT NULL`, id).
			Scan(&row.meta.DisplayName, &row.meta.Description, &row.meta.Path, &row.meta.Filename, &tags,
				&row.contentText, &row.textHash)
		if err == nil {
			row.meta.Tags = tags
			return row
		}
		if err != sql.ErrNoRows {
			t.Fatalf("reading resource %s: %v", id, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("resource %s was not indexed within %s", id, issue1988Indexed)
		}
		time.Sleep(time.Second)
	}
}

func issue1988DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestIssue1988_ATableOfValuesIsEmbeddedOnItsMetadata is the ticket's second
// criterion: a script writes a bulk data file and no content embed is made for
// it. The CSV's vector is built from its metadata alone, its values are still
// extracted for the lexical arm and found by search, and a markdown document
// with the same text is still embedded on its contents.
func TestIssue1988_ATableOfValuesIsEmbeddedOnItsMetadata(t *testing.T) {
	c := connectFor(t, issue1988Indexed+2*time.Minute)
	db := issue1988DB(t)
	token := fmt.Sprintf("sku1988x%d", time.Now().UnixNano()%1_000_000_000)
	body := "store,sku,units\nnorth," + token + ",3\nsouth," + token + ",5\n"

	// The token is in the files' contents only, so a search that finds the CSV
	// found it by what is inside it.
	stamp := time.Now().UnixNano() % 1_000_000_000
	csv := issue1988Create(t, c, fmt.Sprintf("daily-%d.csv", stamp), "text/csv", body)
	md := issue1988Create(t, c, fmt.Sprintf("notes-%d.md", stamp), "text/markdown", body)

	csvRow := issue1988AwaitIndexed(t, db, csv)
	if !strings.Contains(csvRow.contentText, token) {
		t.Errorf("the CSV's values were not extracted for lexical search: %q", csvRow.contentText)
	}
	if want := indexjobs.TextHash(resource.IndexText(csvRow.meta, "")); !bytes.Equal(csvRow.textHash, want) {
		t.Errorf("the CSV's vector was not built from its metadata alone: text hash %x, metadata-only %x, with contents %x",
			csvRow.textHash, want, indexjobs.TextHash(resource.IndexText(csvRow.meta, csvRow.contentText)))
	}

	mdRow := issue1988AwaitIndexed(t, db, md)
	if want := indexjobs.TextHash(resource.IndexText(mdRow.meta, mdRow.contentText)); !bytes.Equal(mdRow.textHash, want) {
		t.Errorf("a document's vector must still carry its contents: text hash %x, want %x", mdRow.textHash, want)
	}

	out := c.call("search", map[string]any{
		"intent":  token,
		"sources": []any{"resources"},
		"limit":   10,
		"purpose": "Acceptance #1988: a value inside a CSV is found by search.",
	})
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), csv) {
		t.Errorf("search for a value inside the CSV did not return it: %s", raw)
	}
}

// issue1988TimeSearch times one search, which embeds its intent on the request
// path.
func issue1988TimeSearch(c *client, intent string) time.Duration {
	start := time.Now()
	c.call("search", map[string]any{
		"intent":  intent,
		"limit":   5,
		"purpose": "Acceptance #1988: time an interactive embed.",
	})
	return time.Since(start)
}

// issue1988Renderer is the dev stack's tile renderer.
const issue1988Renderer = "acme-dev-renderer"

// issue1988PauseRenderer stops the tile renderer for the rest of the test.
// Every file the backfill writes is owed a tile, and on the dev stack the
// renderer drawing a hundred of them shares one machine's CPU with the
// embedding server, which slowed an interactive embed that had nothing queued
// ahead of it (build/1988/acceptance.md records that run). In a deployment the
// renderer runs beside the platform, not on the embedding server's CPU, and
// the criterion is about the embedder's queue, so the renderer is paused while
// it is measured.
func issue1988PauseRenderer(t *testing.T) {
	t.Helper()
	container := os.Getenv("ISSUE_1988_RENDERER_CONTAINER")
	if container == "" {
		container = issue1988Renderer
	}
	docker := func(action string) {
		if out, err := exec.Command("docker", action, container).CombinedOutput(); err != nil { // #nosec G204 -- fixed verbs on the dev stack's own container
			t.Fatalf("docker %s %s: %v\n%s", action, container, err, out)
		}
	}
	docker("pause")
	t.Cleanup(func() { docker("unpause") })
}

// TestIssue1988_ASearchIsNotQueuedBehindABacklog is the ticket's first
// criterion: with about a hundred resource index jobs queued against the
// CPU-only embedder, a search embeds its intent in roughly the time it takes
// on an idle one, not behind the backlog.
func TestIssue1988_ASearchIsNotQueuedBehindABacklog(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	db := issue1988DB(t)
	const backlog = 100
	issue1988PauseRenderer(t)

	idle := make([]time.Duration, 0, 3)
	for i := range 3 {
		idle = append(idle, issue1988TimeSearch(c, fmt.Sprintf("quarterly revenue by region %d", i)))
	}
	slices.Sort(idle)
	baseline := idle[1]

	stamp := time.Now().UnixNano() % 1_000_000_000
	for i := range backlog {
		var b strings.Builder
		b.WriteString("date,store,sku,units,revenue\n")
		for r := range 400 {
			fmt.Fprintf(&b, "2026-09-%02d,store-%d,sku-%d-%d,%d,%d.%02d\n", i%28+1, r%40, stamp, r, r%17, r*13, r%100)
		}
		issue1988Create(t, c, fmt.Sprintf("backfill-%d-%03d.csv", stamp, i), "text/csv", b.String())
	}
	var queued int
	if err := db.QueryRow(`SELECT count(*) FROM index_jobs WHERE source_kind = 'resources' AND status IN ('pending', 'running')`).
		Scan(&queued); err != nil {
		t.Fatalf("counting the backlog: %v", err)
	}
	t.Logf("resource index jobs queued or running after the backfill: %d", queued)

	busy := make([]time.Duration, 0, 3)
	for i := range 3 {
		busy = append(busy, issue1988TimeSearch(c, fmt.Sprintf("customer churn by segment %d", i)))
	}
	slices.Sort(busy)
	t.Logf("search on an idle embedder %v (median %s); behind the backfill %v", idle, baseline, busy)
	if limit := baseline + 5*time.Second; busy[2] > limit {
		t.Errorf("a search behind the backfill took %s; an idle one takes %s, want under %s", busy[2], baseline, limit)
	}
}
