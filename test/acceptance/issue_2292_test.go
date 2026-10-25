//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2292: ranked search for a word that is in half of a 30,000-file
// library returns ranked hits within the search provider's bound, alone and
// beside the other sources.
//
// Wire forms: search's sources as a JSON array of one string, and omitted;
// intent and purpose as strings; limit as a JSON number.

// issue2292Files is the library's size, and issue2292Matching how many of its
// files carry the word.
const (
	issue2292Files    = 30000
	issue2292Matching = 15000
	// issue2292Bound is the search provider's bound (knowledge's
	// defaultProviderSearchTimeout): a slower answer is a failed search.
	issue2292Bound = 5 * time.Second
)

// issue2292Seed writes the library straight into the dev database: a word in
// the display name, folder and tags of every other file, 4 KiB of extracted
// text in each, each already embedded with the deployment's model so the
// indexer has nothing to do, and a type no tile is drawn for. Its files are
// removed when the test ends.
func issue2292Seed(t *testing.T, word string) {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var model string
	if err := db.QueryRowContext(ctx,
		`SELECT embedding_model FROM resources WHERE embedding IS NOT NULL AND embedding_model <> '' LIMIT 1`).Scan(&model); err != nil {
		t.Fatalf("reading the deployment's embedding model from an indexed resource: %v", err)
	}
	prefix := "acc2292-" + word + "-"
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer ccancel()
		if _, err := db.ExecContext(cctx, `DELETE FROM resources WHERE id LIKE $1`, prefix+"%"); err != nil {
			t.Logf("removing the seeded library: %v", err)
		}
	})
	_, err = db.ExecContext(ctx, `
		INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description, mime_type, size_bytes,
		                       s3_key, uri, tags, uploader_sub, content_text, content_indexed_at,
		                       embedding, embedding_model)
		SELECT $1 || g, 'global', NULL,
		       CASE WHEN g % 2 = 0 THEN 'acceptance-2292/' || $2 || '/' || (g % 50) ELSE 'acceptance-2292/ledger/' || (g % 50) END,
		       'file-' || g || '.bin',
		       CASE WHEN g % 2 = 0 THEN $2 || ' ratings ' || g ELSE 'quarterly ledger ' || g END,
		       'Acceptance #2292: file ' || g,
		       'application/octet-stream', 4096, 'acc2292/' || $1 || g, 'mcp://global/acceptance-2292/' || $1 || g,
		       CASE WHEN g % 2 = 0 THEN ARRAY[$2, 'acceptance-2292'] ELSE ARRAY['acceptance-2292'] END,
		       'acceptance', left(repeat('station household viewers market share weekday daypart ' || g || ' ', 80), 4096), now(),
		       (SELECT array_agg(((g * 7 + i) % 97)::float4 / 97) FROM generate_series(1, 768) i)::vector, $3
		  FROM generate_series(1, $4::int) g`, prefix, word, model, issue2292Files)
	if err != nil {
		t.Fatalf("seeding %d files: %v", issue2292Files, err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE resources`); err != nil {
		t.Fatalf("analyzing: %v", err)
	}
}

// issue2292Hits is the hits search returned in its resources group.
func issue2292Hits(out map[string]any) []map[string]any {
	groups, _ := out["groups"].([]any)
	var hits []map[string]any
	for _, g := range groups {
		group, _ := g.(map[string]any)
		if group["source"] != "resources" {
			continue
		}
		items, _ := group["hits"].([]any)
		for _, item := range items {
			if hit, ok := item.(map[string]any); ok {
				hits = append(hits, hit)
			}
		}
	}
	return hits
}

// A one-word intent matching 15,000 of 30,000 files returns ranked hits
// within the provider's bound, through search with sources ["resources"].
func TestIssue2292_AWordInHalfTheLibraryIsRankedWithinTheBound(t *testing.T) {
	word := fmt.Sprintf("pipeline%d", time.Now().UnixNano()%1_000_000_000)
	issue2292Seed(t, word)
	c := connectFor(t, 5*time.Minute)

	start := time.Now()
	out := c.call("search", map[string]any{
		"intent": word, "sources": []any{"resources"}, "limit": 10,
		"purpose": "Acceptance #2292: rank a word half the library carries.",
	})
	took := time.Since(start)
	hits := issue2292Hits(out)
	if len(hits) == 0 {
		t.Fatalf("search returned no resource hits for %q (%d of %d files carry it): %v", word, issue2292Matching, issue2292Files, out)
	}
	if took > issue2292Bound {
		t.Fatalf("search took %s, past the provider's %s bound", took, issue2292Bound)
	}
	for _, h := range hits {
		if text, _ := h["text"].(string); !strings.Contains(text, word) {
			t.Fatalf("hit %v does not name the word %q", h, word)
		}
	}
	t.Logf("search with sources [resources] answered %d hits in %s", len(hits), took)
}

// The same search with no sources returns the resources group with those hits.
func TestIssue2292_TheSameSearchWithEverySourceKeepsTheResourcesGroup(t *testing.T) {
	word := fmt.Sprintf("pipeline%d", time.Now().UnixNano()%1_000_000_000)
	issue2292Seed(t, word)
	c := connectFor(t, 5*time.Minute)

	start := time.Now()
	out := c.call("search", map[string]any{
		"intent": word, "limit": 10,
		"purpose": "Acceptance #2292: every source, with a word half the library carries.",
	})
	took := time.Since(start)
	if hits := issue2292Hits(out); len(hits) == 0 {
		t.Fatalf("search with every source returned no resources group for %q: %v", word, out)
	}
	if took > 2*issue2292Bound {
		t.Fatalf("search took %s", took)
	}
	t.Logf("search with every source answered in %s", took)
}
