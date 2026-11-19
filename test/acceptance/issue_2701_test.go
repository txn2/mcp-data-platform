//go:build integration

package acceptance

import (
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2701: the storage capacity pass reclaimed every site
// environment's tile as an orphan a day after it was drawn, and the row still
// said drawn, so the tile was never drawn again. A site tile is now a
// reference the pass reads; a tile whose object is gone is drawn again on the
// next request for it; and a row an older release drew (renderer generation 2)
// is drawn again on upgrade whether or not its object survived. The dev stack
// reclaims an orphan three minutes old and lists the buckets every 30 seconds
// (dev/start.sh), so a pass past the age runs within the criterion's bound.
//
// Wire forms: the portal's site routes take JSON objects of strings and the
// tile route takes the site and environment in its path; no tool parameter is
// touched.

// tileKey2701 is the object key and renderer generation site_tiles records
// for env, read from the platform's database.
func tileKey2701(t *testing.T, s *site2604, env string) (key string, renderer int) {
	t.Helper()
	err := devDB(t).QueryRowContext(t.Context(),
		`SELECT t.tile_key, t.renderer FROM site_tiles t JOIN sites s ON s.id = t.site_id WHERE s.name = $1 AND t.environment = $2`,
		s.name, env).Scan(&key, &renderer)
	if err != nil {
		t.Fatalf("reading %s's tile row: %v", env, err)
	}
	return key, renderer
}

// drawn2701 is a site published to dev whose dev tile is drawn, and the key
// it is stored under.
func drawn2701(t *testing.T, prefix string) (*site2604, string) {
	t.Helper()
	s := newSite2604(t, keyAdmin2604, prefix)
	s.publish(t, keyAdmin2604, "one")
	if status := tile2658(t, s, keyAdmin2604, "dev", http.StatusOK); status != http.StatusOK {
		t.Fatalf("dev's tile was not drawn within the bound: %d", status)
	}
	key, _ := tileKey2701(t, s, "dev")
	if key == "" || !assetStore(t).exists(key) {
		t.Fatalf("the drawn tile names %q, which is not in the portal bucket", key)
	}
	return s, key
}

// TestIssue2701_ATileOutlivesTheCapacityPass: a tile older than the reclaim
// age is still in the bucket after a listing that began past that age, and
// the tile route still answers it.
func TestIssue2701_ATileOutlivesTheCapacityPass(t *testing.T) {
	s, key := drawn2701(t, "acc2701-pass-")
	reclaimable := time.Now().Add(3*time.Minute + 5*time.Second)
	for time.Now().Before(reclaimable) {
		time.Sleep(5 * time.Second)
	}
	waitForFreshListing(t, time.Now())
	if !assetStore(t).exists(key) {
		t.Fatalf("the capacity pass removed the tile %s as an orphan", key)
	}
	if status, _ := s.rest(t, keyAdmin2604, http.MethodGet, "/environments/dev/tile", nil); status != http.StatusOK {
		t.Errorf("the tile route answered %d after the capacity pass", status)
	}
}

// TestIssue2701_ATileWhoseObjectIsGoneIsDrawnAgain: with the tile's object
// removed by hand, the next request answers 404 and the tile is drawn again
// for the same release, with no change to the site.
func TestIssue2701_ATileWhoseObjectIsGoneIsDrawnAgain(t *testing.T) {
	s, key := drawn2701(t, "acc2701-gone-")
	assetStore(t).remove(key)
	if status, _ := s.rest(t, keyAdmin2604, http.MethodGet, "/environments/dev/tile", nil); status != http.StatusNotFound {
		t.Fatalf("a tile whose object is gone answered %d", status)
	}
	if status := tile2658(t, s, keyAdmin2604, "dev", http.StatusOK); status != http.StatusOK {
		t.Fatalf("the tile was not drawn again within the bound: %d", status)
	}
	if again, _ := tileKey2701(t, s, "dev"); again == "" || !assetStore(t).exists(again) {
		t.Errorf("the redrawn tile names %q, which is not in the portal bucket", again)
	}
}

// TestIssue2701_AnUpgradeDrawsTilesAnOlderReleaseRecorded: a row recorded by
// the renderer generation before this fix, whose object the pass removed, is
// drawn again by the worker with nobody asking for it, as every deployment's
// tiles are on upgrade.
func TestIssue2701_AnUpgradeDrawsTilesAnOlderReleaseRecorded(t *testing.T) {
	s, key := drawn2701(t, "acc2701-upgrade-")
	if _, err := devDB(t).ExecContext(t.Context(),
		`UPDATE site_tiles t SET renderer = 2 FROM sites s WHERE s.id = t.site_id AND s.name = $1`, s.name); err != nil {
		t.Fatalf("recording the tile under the older generation: %v", err)
	}
	assetStore(t).remove(key)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		again, renderer := tileKey2701(t, s, "dev")
		if renderer == 3 && again != "" && assetStore(t).exists(again) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker did not draw the older generation's tile again: renderer %d, key %q", renderer, again)
		}
		time.Sleep(time.Second)
	}
	if status, _ := s.rest(t, keyAdmin2604, http.MethodGet, "/environments/dev/tile", nil); status != http.StatusOK {
		t.Errorf("the redrawn tile answered %d", status)
	}
}
