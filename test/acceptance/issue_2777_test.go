//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2777: a publish with base that adds a file at the path a
// kept compiled entry produces (an app.js beside an unchanged app.tsx) is
// refused with the collision refusal naming both, not a database error, and
// nothing is written.
//
// Wire forms: manage_site takes command, site and note as strings, base as a
// number, and files as a list of {path, content} objects whose content is
// utf-8 text. No tool parameter changed its schema.

// Criterion 1: the overlay is refused naming app.tsx and app.js, the refusal
// is not a database error, and dev keeps serving release 1.
func TestIssue2777_AnOverlayAtAKeptOutputPathIsRefusedNamingBoth(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, compiled2774(buildFile2774))
	res, text, err := c.callRaw("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "overlay", "base": 1,
		"files": []map[string]any{{"path": "app.js", "content": "console.log('a plain file');"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "app.tsx") || !strings.Contains(text, "compiles to app.js, which the release also holds as a file") {
		t.Fatalf("the overlay was not refused with the collision: error=%v %s", res.IsError, text)
	}
	for _, leak := range []string{"pq:", "duplicate key", "23505", "site_release_files_pkey"} {
		if strings.Contains(text, leak) {
			t.Errorf("the refusal is a database error (%s): %s", leak, text)
		}
	}
	if rels := releases2588(t, c, s); len(rels) != 1 {
		t.Errorf("a refused overlay wrote a release: %v", rels)
	}
	if v := devRelease2774(t, c, s); v != 1 {
		t.Errorf("a refused overlay moved dev to release %d", v)
	}
}
