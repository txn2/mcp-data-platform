package thumbtypes_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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
// CAPTURABLE_TYPES in ui/src/lib/thumbnailSupport.ts -- and this reads the
// TypeScript one and fails when the two disagree, on the families, on their
// order, or on which of them are themeable.

// familyRe matches one entry of the TypeScript table, which is written one
// entry per line as an object literal with the two fields in a fixed order.
// A rewrite that breaks that shape matches nothing, which is reported rather
// than passing vacuously.
var familyRe = regexp.MustCompile(`\{\s*type:\s*"([^"]+)",\s*family:\s*"([^"]+)"\s*\}`)

// themeableRe matches the set of families captured twice, which the browser
// states once as a property of the family rather than once per content type.
// The patterns this side calls themeable are DERIVED from it and the table
// above, so neither language restates the other's answer (#1754).
var themeableRe = regexp.MustCompile(
	`THEMEABLE_FAMILIES:\s*ReadonlySet<CaptureFamily>\s*=\s*new Set<CaptureFamily>\(\[([^\]]*)\]`)

// largeRe matches the set of families held to the raised source bound, which
// the browser states once as a property of the family as it does the themeable
// set. The patterns this side raises the bound for are DERIVED from it and
// the table above, so neither language restates the other's answer.
var largeRe = regexp.MustCompile(
	`LARGE_SOURCE_FAMILIES:\s*ReadonlySet<CaptureFamily>\s*=\s*new Set<CaptureFamily>\(\[([^\]]*)\]`)

// limitRe matches one of the browser's source bounds, whose value is written
// as a product of decimal factors ("32 * 1024 * 1024").
func limitRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`export const ` + name + `\s*=\s*([0-9 *]+);`)
}

// quotedRe pulls the quoted members out of a family set's body.
var quotedRe = regexp.MustCompile(`"([^"]+)"`)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	// file = <root>/internal/thumbtypes/parity_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// browserRel is the TypeScript definition, relative to the repository root.
const browserRel = "ui/src/lib/thumbnailSupport.ts"

// browserSource is the TypeScript definition's text.
func browserSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(browserRel))) //nolint:gosec // test reads project sources
	if err != nil {
		t.Fatalf("reading %s: %v", browserRel, err)
	}
	return string(body)
}

// browserFamilies is the TypeScript table, read as the patterns it names and
// the subsets of them whose family the browser captures twice and holds to the
// raised source bound.
func browserFamilies(t *testing.T) (capturable, themeable, large []string) {
	t.Helper()
	body := browserSource(t)
	themeableFamilies := browserFamilySet(t, "themeable", themeableRe, body)
	largeFamilies := browserFamilySet(t, "large-source", largeRe, body)
	for _, m := range familyRe.FindAllStringSubmatch(body, -1) {
		capturable = append(capturable, m[1])
		if themeableFamilies[m[2]] {
			themeable = append(themeable, m[1])
		}
		if largeFamilies[m[2]] {
			large = append(large, m[1])
		}
	}
	if len(capturable) == 0 {
		t.Fatalf("%s: no capturable families found; the table's shape has changed and this "+
			"test can no longer read it", browserRel)
	}
	return capturable, themeable, large
}

// browserFamilySet reads one of the browser's family sets, each of which
// states a property of the family rather than of each content type.
func browserFamilySet(t *testing.T, what string, re *regexp.Regexp, body string) map[string]bool {
	t.Helper()
	set := re.FindStringSubmatch(body)
	if set == nil {
		t.Fatalf("%s: the %s family set cannot be read; its shape has changed", browserRel, what)
	}
	families := map[string]bool{}
	for _, m := range quotedRe.FindAllStringSubmatch(set[1], -1) {
		families[m[1]] = true
	}
	if len(families) == 0 {
		t.Fatalf("%s: the %s family set is empty; its shape has changed", browserRel, what)
	}
	return families
}

func TestGoAndBrowserAgreeOnWhatGetsAThumbnail(t *testing.T) {
	capturable, themeable, large := browserFamilies(t)

	assertSame(t, "capturable", thumbtypes.Capturable, capturable)
	assertSame(t, "themeable", thumbtypes.Themeable, themeable)
	assertSame(t, "large-source", thumbtypes.LargeSourceFamilies, large)
}

// TestGoAndBrowserAgreeOnHowLargeADocumentMayBe holds the two bounds to each
// other. A family raised on one side alone is a card that offers a redraw the
// server will never take, or a card that says a file is too large while the
// server is drawing it; the values were written out twice from the day the
// second bound existed (#1794).
func TestGoAndBrowserAgreeOnHowLargeADocumentMayBe(t *testing.T) {
	body := browserSource(t)
	for _, tc := range []struct {
		name string
		want int64
	}{
		{"THUMBNAIL_SOURCE_LIMIT", thumbtypes.DefaultSourceLimit},
		{"LARGE_THUMBNAIL_SOURCE_LIMIT", thumbtypes.LargeSourceLimit},
	} {
		m := limitRe(tc.name).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("%s: %s cannot be read; its shape has changed", browserRel, tc.name)
		}
		if got := product(t, m[1]); got != tc.want {
			t.Errorf("%s = %d in %s, want %d", tc.name, got, browserRel, tc.want)
		}
	}
}

// product evaluates the browser's way of writing a size: decimal factors
// multiplied together.
func product(t *testing.T, expr string) int64 {
	t.Helper()
	out := int64(1)
	for factor := range strings.SplitSeq(expr, "*") {
		n, err := strconv.ParseInt(strings.TrimSpace(factor), 10, 64)
		if err != nil {
			t.Fatalf("%s: %q is not a product of decimal factors: %v", browserRel, expr, err)
		}
		out *= n
	}
	return out
}

// assertSame compares the two languages' lists element by element, in order:
// order is part of the definition on the browser side, where the first pattern
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
