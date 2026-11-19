package gates

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestNoLaneWritesTheUIEmbedDirectory holds #2711: verify runs its lanes
// together, every one of them compiles packages that import internal/ui, and
// internal/ui embeds all:dist. A step in any lane that empties or rewrites
// internal/ui/dist (frontend-build, or a GoReleaser run in the checkout,
// whose before hook calls frontend-build) races the others' compiles: one
// listed dist/mockServiceWorker.js and failed to open it, and every
// first-party package failed to type-check.
func TestNoLaneWritesTheUIEmbedDirectory(t *testing.T) {
	raw, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(raw)
	lanes := prerequisites(makefile, "verify-checks")
	if len(lanes) < 2 {
		t.Fatalf("verify-checks names %v; the lanes it runs together were not found", lanes)
	}
	for _, lane := range lanes {
		for _, target := range reachable(t, makefile, lane) {
			if why := writesEmbed(recipe(t, makefile, target)); why != "" {
				t.Errorf("lane %s runs %s, which %s while the other lanes compile internal/ui; "+
					"build on an export of the tree (scripts/release-check-isolated.sh) instead", lane, target, why)
			}
		}
	}
}

// TestWritesEmbedRecognizesTheRaces is the rule's own check: each way a
// recipe has written the embed directory is caught, and the export is not.
func TestWritesEmbedRecognizesTheRaces(t *testing.T) {
	for _, tc := range []struct {
		line string
		race bool
	}{
		{"env -u GITLAB_TOKEN goreleaser release --snapshot --clean", true},
		{"@rm -rf $(UI_EMBED_DIR)/*", true},
		{"@cp -r $(UI_DIR)/dist/* $(UI_EMBED_DIR)/", true},
		{"@$(MAKE) --no-print-directory frontend-build", true},
		{"bash scripts/release-check-isolated.sh", false},
		{"python3 scripts/schedule-lane.py ui", false},
	} {
		if got := writesEmbed([]string{tc.line}) != ""; got != tc.race {
			t.Errorf("writesEmbed(%q) = %v, want %v", tc.line, got, tc.race)
		}
	}
}

// writesEmbed says how a recipe writes internal/ui/dist, or "" when it does
// not.
func writesEmbed(lines []string) string {
	for _, l := range lines {
		switch {
		case strings.Contains(l, "goreleaser release"):
			return "runs GoReleaser in the checkout (its before hook rewrites internal/ui/dist)"
		case strings.Contains(l, "$(UI_EMBED_DIR)") && (strings.Contains(l, "rm ") || strings.Contains(l, "cp ") || strings.Contains(l, "mv ")):
			return "writes $(UI_EMBED_DIR)"
		case slices.Contains(makeTargets(l), "frontend-build"):
			return "runs frontend-build"
		}
	}
	return ""
}

// reachable returns target and every target its recipe runs through $(MAKE)
// or names as a prerequisite, transitively.
func reachable(t *testing.T, makefile, target string) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	var walk func(string)
	walk = func(name string) {
		if seen[name] || !hasRule(makefile, name) {
			return
		}
		seen[name] = true
		out = append(out, name)
		for _, p := range prerequisites(makefile, name) {
			walk(p)
		}
		for _, l := range recipe(t, makefile, name) {
			for _, n := range makeTargets(l) {
				walk(n)
			}
		}
	}
	walk(target)
	return out
}

// makeTargets returns the targets a recipe line runs with $(MAKE), skipping
// flags and variable assignments.
func makeTargets(line string) []string {
	_, after, ok := strings.Cut(line, "$(MAKE)")
	if !ok {
		return nil
	}
	var out []string
	for f := range strings.FieldsSeq(after) {
		if strings.HasPrefix(f, "-") || strings.Contains(f, "=") {
			continue
		}
		if strings.ContainsAny(f, ";|&") {
			break
		}
		out = append(out, f)
	}
	return out
}

// hasRule reports whether the Makefile defines target.
func hasRule(makefile, target string) bool {
	for line := range strings.SplitSeq(makefile, "\n") {
		if rest, ok := strings.CutPrefix(line, target+":"); ok && !strings.HasPrefix(rest, "=") {
			return true
		}
	}
	return false
}
