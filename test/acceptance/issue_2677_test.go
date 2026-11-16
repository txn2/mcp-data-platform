//go:build integration

package acceptance

import (
	"net/http"
	"testing"
)

// Acceptance for #2677: a site release whose index.html has loading="lazy"
// images below the fold, and a content-visibility:auto section there, gets a
// tile on its first attempt, the image inside the frame decoded first; a page
// with no images draws as before.
//
// Wire forms: manage_site takes command, site and note as strings and files
// as a JSON array of objects of strings.

const picture2677 = `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#2a6"/></svg>`

// lazyPage2677 has a picture in the frame and, past a 3000px spacer, lazy
// pictures and a content-visibility:auto section a 960px frame never reaches.
const lazyPage2677 = `<!doctype html><html><body>
<h1>Lazy below the fold</h1>
<img src="pic.svg" width="400" height="300" alt="in the frame">
<div style="height:3000px"></div>
<img loading="lazy" src="pic.svg?1" width="400" height="300" alt="below one">
<img loading="lazy" src="pic.svg?2" width="400" height="300" alt="below two">
<section style="content-visibility:auto;contain-intrinsic-size:600px"><img loading="lazy" src="pic.svg?3" width="400" height="300" alt="hidden"></section>
</body></html>`

// tileAttempts2677 is how many attempts the environment's tile took.
func tileAttempts2677(t *testing.T, s *site2604, env string) int {
	t.Helper()
	var attempts int
	if err := issue1904DB(t).QueryRow(`SELECT t.attempts FROM site_tiles t JOIN sites s ON s.id = t.site_id WHERE s.name = $1 AND t.environment = $2`, s.name, env).Scan(&attempts); err != nil {
		t.Fatalf("reading the tile's attempts: %v", err)
	}
	return attempts
}

// TestIssue2677_LazyImagesBelowTheFoldDoNotHoldTheTile: the lazy page gets a
// tile on its first attempt, with no failure recorded.
func TestIssue2677_LazyImagesBelowTheFoldDoNotHoldTheTile(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2677-lazy-")
	connectAs(t, s.key).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "lazy",
		"files": []map[string]any{{"path": "index.html", "content": lazyPage2677}, {"path": "pic.svg", "content": picture2677}},
	})
	if status := tile2658(t, s, s.key, "dev", http.StatusOK); status != http.StatusOK {
		t.Fatalf("the lazy page got no tile within the bound: %d (failure %v)", status, envOf2664(t, s, "dev")["tile_failure"])
	}
	if n := tileAttempts2677(t, s, "dev"); n != 1 {
		t.Errorf("the lazy page's tile took %d attempts, not one", n)
	}
	if f := envOf2664(t, s, "dev")["tile_failure"]; f != nil {
		t.Errorf("the lazy page's tile records a failure: %v", f)
	}
}

// TestIssue2677_APageWithNoImagesDrawsAsBefore: a page with no images gets a
// tile on its first attempt.
func TestIssue2677_APageWithNoImagesDrawsAsBefore(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2677-plain-")
	s.publish(t, s.key, "plain")
	if status := tile2658(t, s, s.key, "dev", http.StatusOK); status != http.StatusOK {
		t.Fatalf("a page with no images got no tile within the bound: %d", status)
	}
	if n := tileAttempts2677(t, s, "dev"); n != 1 {
		t.Errorf("a page with no images took %d attempts, not one", n)
	}
}
