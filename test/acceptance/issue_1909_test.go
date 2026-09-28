//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1909: each script's tile is its flow diagram, drawn by the tile
// worker in the headless renderer beside the platform and served at
// GET /api/v1/portal/scripts/{id}/thumbnail, which the scripts listing's grid
// view shows.
//
// What these hold, against the running platform and its renderer: a saved
// script gains a stored tile, light and dark; saving a version that adds an
// export draws a new tile; a latest version that does not parse is answered
// with no tile (the placeholder) and is recorded as not drawable with the
// parse error; and a script with no platform calls is drawn, not refused.
//
// Wire forms: the path parameter and `variant` are each sent once as the URL
// string the route admits; manage_script's arguments once as strings.

// tileWait bounds how long a test waits for the worker to draw a tile: its
// poll, a render in each theme, and a store.
const tileWait = 3 * time.Minute

func tile1909(c *client, id, variant string) (int, string) {
	path := fmt.Sprintf("/api/v1/portal/scripts/%s/thumbnail", id)
	if variant != "" {
		path += "?variant=" + variant
	}
	status, _, body := c.restGet(path, nil)
	return status, body
}

// awaitTile waits until the script's tile is served and differs from not.
func awaitTile1909(t *testing.T, c *client, id, not string) string {
	t.Helper()
	deadline := time.Now().Add(tileWait)
	for time.Now().Before(deadline) {
		if status, body := tile1909(c, id, ""); status == http.StatusOK && body != not {
			return body
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("script %s was not given a tile within %s", id, tileWait)
	return ""
}

func TestIssue1909_ASavedScriptIsDrawnAndRedrawnWhenItChanges(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	v1 := `
def main():
    """Reads one row."""
    platform.query("SELECT 1 AS n", connection = "acme")
`
	id, name := script1906(t, c, "tile", v1)
	first := awaitTile1909(t, c, id, "")
	if len(first) < 8 || first[1:4] != "PNG" {
		t.Fatalf("the tile is not a PNG: %q", first[:min(len(first), 8)])
	}
	if status, dark := tile1909(c, id, "dark"); status != http.StatusOK || dark == first {
		t.Errorf("the dark tile is %d and equal to the light one: %v", status, dark == first)
	}
	peer := connectAs(t, devPeerAPIKey)
	if status, _ := tile1909(peer, id, ""); status != http.StatusOK {
		t.Errorf("a reader who does not own the script is answered %d", status)
	}

	v2 := `
def main():
    """Reads one row and exports it."""
    rows = platform.query("SELECT 1 AS n", connection = "acme")
    platform.export("acc-1909", rows["rows"], format = "csv")
`
	if out := c.saveEdit(map[string]any{"command": "update", "name": name, "source": v2}, nil); out["error"] != nil || out["status"] == "invalid" {
		t.Fatalf("update refused: %v", out)
	}
	awaitTile1909(t, c, id, first)
}

func TestIssue1909_AScriptWithNoPlatformCallsIsDrawn(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "tile-empty", `
def main():
    """Prints a sum and calls nothing on the platform."""
    print(1 + 2)
`)
	awaitTile1909(t, c, id, "")
}

// Saving refuses a source that does not parse, so the latest version is
// rewritten in the database to stand for one saved before a rule of the
// language changed (#1823 made `load` reserved).
func TestIssue1909_AVersionThatDoesNotParseIsThePlaceholder(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "tile-bad", `
def main():
    """Reads one row."""
    platform.query("SELECT 1 AS n", connection = "acme")
`)
	awaitTile1909(t, c, id, "")
	db := issue1904DB(t)
	issue1904Exec(t, db, `UPDATE scripts SET source_code = $1, version = version + 1 WHERE id = $2`,
		"def load():\n    platform.query(\"SELECT 1\")\nload()\n", id)

	deadline := time.Now().Add(tileWait)
	for time.Now().Before(deadline) {
		if n := issue1904Count(t, db, `SELECT count(*) FROM script_tiles
			 WHERE script_id = $1 AND failure LIKE '%does not parse%' AND failed_version = (SELECT version FROM scripts WHERE id = $1)`, id); n == 1 {
			if status, _ := tile1909(c, id, ""); status != http.StatusNotFound {
				t.Errorf("a script whose latest version does not parse is answered %d, want the placeholder's 404", status)
			}
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("the unparseable version was not recorded as not drawable within %s", tileWait)
}
