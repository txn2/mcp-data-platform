package pdfhttp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mapTracker is the text between the map-tracker markers in s, empty when
// they are absent.
func mapTracker(s string) string {
	const start, end = "/*map-tracker:start*/", "/*map-tracker:end*/"
	i, j := strings.Index(s, start), strings.Index(s, end)
	if i < 0 || j < i {
		return ""
	}
	return s[i : j+len(end)]
}

// TestMapTrackerIsTheThumbnailsToo holds #2087: the PDF print step and the
// thumbnail capture wait for a map with one tracker, so the thumbnail cannot
// again be taken while the PDF waits.
func TestMapTrackerIsTheThumbnailsToo(t *testing.T) {
	ts, err := os.ReadFile(filepath.Join("..", "..", "..", "ui", "src", "lib", "thumbnail.ts"))
	if err != nil {
		t.Fatalf("reading the thumbnail capture: %v", err)
	}
	ours, theirs := mapTracker(PrintStep), mapTracker(string(ts))
	if ours == "" || theirs == "" {
		t.Fatalf("a map tracker is missing its markers (print step %d bytes, thumbnail %d bytes)", len(ours), len(theirs))
	}
	if ours != theirs {
		t.Errorf("the print step's map tracker and the thumbnail's differ:\n--- print step\n%s\n--- thumbnail\n%s", ours, theirs)
	}
}
