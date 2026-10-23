package gates

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// targetLine is a rule's first line: `name:` or `name: prerequisites`, not a
// variable assignment and not a target-specific one.
var targetLine = regexp.MustCompile(`^([A-Za-z0-9_.-]+):([^=]|$)`)

// bashShellLine is a target-specific `name: SHELL := /bin/bash`.
var bashShellLine = regexp.MustCompile(`^([A-Za-z0-9_.-]+):\s*SHELL\s*:?=\s*/bin/bash\s*$`)

// Make runs a recipe with /bin/sh, which is bash on macOS and dash on the
// Ubuntu runners, and dash refuses `set -o pipefail`. The acceptance recipe
// passed every local run and failed every nightly group before running a
// criterion (#2266). A recipe that sets pipefail must run its target in bash.
func TestPipefailRecipesDeclareBash(t *testing.T) {
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	bash := map[string]bool{}
	using := map[string]int{}
	target := ""
	for i, line := range strings.Split(string(src), "\n") {
		if m := bashShellLine.FindStringSubmatch(line); m != nil {
			bash[m[1]] = true
			continue
		}
		if m := targetLine.FindStringSubmatch(line); m != nil {
			target = m[1]
			continue
		}
		if strings.HasPrefix(line, "\t") && strings.Contains(line, "pipefail") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			if _, seen := using[target]; !seen {
				using[target] = i + 1
			}
		}
	}
	if len(using) == 0 {
		t.Fatal("no recipe sets pipefail; the acceptance recipe did, so the scan is reading the Makefile wrong")
	}
	for name, line := range using {
		if !bash[name] {
			t.Errorf("Makefile:%d: %s sets pipefail, which dash refuses; add `%s: SHELL := /bin/bash`", line, name, name)
		}
	}
}
