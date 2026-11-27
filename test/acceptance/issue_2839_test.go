//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2839: a person's search comes first while the index
// worker's embed is already at the embedder, not only between its requests.
//
// Wire forms: apply_knowledge is called through the MCP client with `page` a
// JSON object (its schema admits no other form); search with `intent` a
// string and `sources` an array of one string.
//
// It runs in the embeddings environment (test/acceptance/environments.txt):
// the dev stack's CPU-only Ollama, which is the case: a knowledge page's
// chunks near the model's context take seconds each to embed, and before #2839
// they went to the embedder in one request that a search could only queue
// behind.

// issue2839Body is a page of about 140,000 characters of distinct prose, which
// the index worker embeds as about two dozen chunks near the input budget: sent
// as one request, as before #2839, they hold even a fast CPU embedder past a
// search's 5-second embedding timeout.
func issue2839Body(run string) string {
	var b strings.Builder
	for s := range 24 {
		fmt.Fprintf(&b, "## Section %d of the warehouse handbook %s\n\n", s, run)
		for p := range 30 {
			fmt.Fprintf(&b, "Paragraph %d.%d: pallets received at dock %d are counted, labelled and put away by zone, "+
				"and a shortage is reported to the carrier within %d hours of the delivery note. ", s, p, s*30+p, p%12+1)
			b.WriteString("Cycle counts reconcile the ledger each night; a variance above tolerance opens a review.\n\n")
		}
	}
	return b.String()
}

// Criterion: with a long page's chunks at the embedder, a search is answered
// with semantic ranking in about the time a search takes, and the page is
// still embedded in full afterwards.
func TestIssue2839_ASearchIsAnsweredWhileAPagesChunksAreAtTheEmbedder(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	db := issue1988DB(t)
	issue1988PauseRenderer(t)
	run := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	slug := "acceptance-2839-" + run
	out := c.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug": slug, "title": "Warehouse handbook " + run,
			"summary": "Written by the #2839 acceptance run.", "body": issue2839Body(run), "force_new": true,
		},
	})
	cs, _ := out["changeset_id"].(string)
	if cs == "" {
		t.Fatalf("the page was not written: %v", out)
	}
	rollback1696(t, c, cs)

	waitFor(t, "the page's index job at the embedder", func() (bool, string) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM index_jobs WHERE source_kind = 'portal-knowledge-pages' AND status = 'running'`).Scan(&n); err != nil {
			return false, err.Error()
		}
		return n > 0, fmt.Sprintf("%d running", n)
	})

	start := time.Now()
	got := c.call("search", map[string]any{
		"intent": "how are shortages reported to the carrier", "sources": []string{"knowledge"}, "limit": 5,
		"purpose": "Acceptance #2839: a search while a page is at the embedder.",
	})
	took := time.Since(start)
	t.Logf("search while the page's chunks were at the embedder: %s, ranking %v", took, got["ranking"])
	if got["ranking"] != "hybrid" {
		t.Fatalf("the search fell back to %v ranking (%v) while the page was at the embedder", got["ranking"], got["ranking_note"])
	}
	if took > 10*time.Second {
		t.Fatalf("the search took %s behind the page's embed", took)
	}

	// The page is not starved by the searches that preempted it: it finishes.
	// On a CPU-only embedder its chunks take 80 to 95 seconds by themselves,
	// so the bound is the embed's time with room to spare, not the 90-second
	// wait other criteria use (#2843).
	deadline := time.Now().Add(embedded2839)
	var status string
	for {
		err := db.QueryRow(`SELECT status FROM index_jobs WHERE source_kind = 'portal-knowledge-pages'
		                     ORDER BY created_at DESC LIMIT 1`).Scan(&status)
		if err == nil && status == "succeeded" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page embedded in full did not arrive within %s; last look: %s (%v)", embedded2839, status, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// embedded2839 is how long the page may take to embed in full.
const embedded2839 = 5 * time.Minute
