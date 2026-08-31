package thumbtypes_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
)

// The rule about which content families get a thumbnail is asked in two
// languages and cannot be stated in one. Go asks it in SQL, to decide what work
// to offer; the browser asks it to decide whether to draw a tile at all, and
// again to decide how to draw one. Before this ticket it was written out four
// times and the four had stopped agreeing: a JSX resource was never offered
// although the capturer renders JSX, and an image asset was never offered
// although the capturer downscales images (#1568).
//
// Each language now keeps one definition -- thumbtypes here,
// CAPTURABLE_FAMILIES in ui/src/lib/thumbnailSupport.ts -- and this reads the
// TypeScript one and fails when the two disagree, on the families, on their
// order, or on which of them are themeable.

// familyRe matches one entry of the TypeScript table, which is written one
// entry per line as an object literal with the three fields in a fixed order.
// A rewrite that breaks that shape matches nothing, which is reported rather
// than passing vacuously.
var familyRe = regexp.MustCompile(
	`\{\s*fragment:\s*"([^"]+)",\s*family:\s*"[^"]+",\s*themeable:\s*(true|false)\s*\}`)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	// file = <root>/internal/thumbtypes/parity_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// browserFamilies is the TypeScript table, read as the fragments it names and
// the subset of them it marks themeable.
func browserFamilies(t *testing.T) (capturable, themeable []string) {
	t.Helper()
	rel := filepath.Join("ui", "src", "lib", "thumbnailSupport.ts")
	body, err := os.ReadFile(filepath.Join(repoRoot(t), rel)) //nolint:gosec // test reads project sources
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	for _, m := range familyRe.FindAllStringSubmatch(string(body), -1) {
		capturable = append(capturable, m[1])
		if m[2] == "true" {
			themeable = append(themeable, m[1])
		}
	}
	if len(capturable) == 0 {
		t.Fatalf("%s: no capturable families found; the table's shape has changed and this "+
			"test can no longer read it", rel)
	}
	return capturable, themeable
}

func TestGoAndBrowserAgreeOnWhatGetsAThumbnail(t *testing.T) {
	capturable, themeable := browserFamilies(t)

	assertSame(t, "capturable", thumbtypes.Capturable, capturable)
	assertSame(t, "themeable", thumbtypes.Themeable, themeable)
}

// assertSame compares the two languages' lists element by element, in order:
// order is part of the definition on the browser side, where the first fragment
// a content type contains decides how it is drawn.
func assertSame(t *testing.T, what string, goList, tsList []string) {
	t.Helper()
	if len(goList) != len(tsList) {
		t.Fatalf("%s families disagree: Go has %v, ui/src/lib/thumbnailSupport.ts has %v",
			what, goList, tsList)
	}
	for i := range goList {
		if goList[i] != tsList[i] {
			t.Errorf("%s family %d disagrees: Go says %q, ui/src/lib/thumbnailSupport.ts says %q",
				what, i, goList[i], tsList[i])
		}
	}
}

// ILikePatterns is what turns a fragment into the substring test the browser
// applies, asked of a column.
func TestILikePatternsWrapEveryFragment(t *testing.T) {
	got := thumbtypes.ILikePatterns([]string{"csv", "image/"})
	want := []string{"%csv%", "%image/%"}
	if len(got) != len(want) {
		t.Fatalf("ILikePatterns returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pattern %d = %q, want %q", i, got[i], want[i])
		}
	}
}
