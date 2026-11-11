package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// aptBrowserInstall is a workflow line that installs the test browser's system
// packages with apt directly.
var aptBrowserInstall = regexp.MustCompile(`playwright\s+install(\s+[^\n]*)?\s--with-deps|playwright\s+install-deps`)

// TestWorkflowsInstallTheTestBrowserThroughTheScript pins #2609: every
// workflow gets Playwright's Chromium through scripts/install-test-browser.sh,
// which bounds and retries apt and goes on without it when the browser starts
// on the runner's own libraries. A bare `playwright install --with-deps`
// failed every acceptance job of the E2E Nightly on 2026-11-11 when the runner
// could not resolve the Ubuntu archives.
func TestWorkflowsInstallTheTestBrowserThroughTheScript(t *testing.T) {
	workflows, err := filepath.Glob("../../.github/workflows/*.yml")
	require(t, err)
	if len(workflows) == 0 {
		t.Fatal("no .github/workflows/*.yml found")
	}
	callers := 0
	for _, path := range workflows {
		raw, err := os.ReadFile(path) //nolint:gosec // a path globbed from this repository
		require(t, err)
		text := string(raw)
		for i, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if aptBrowserInstall.MatchString(line) {
				t.Errorf("%s:%d installs the test browser's packages with apt directly; run scripts/install-test-browser.sh instead: %s",
					filepath.Base(path), i+1, strings.TrimSpace(line))
			}
		}
		if strings.Contains(text, "scripts/install-test-browser.sh") {
			callers++
		}
	}
	if callers < 2 {
		t.Errorf("scripts/install-test-browser.sh is run by %d workflows; ci.yml and acceptance-run.yml both install the browser through it", callers)
	}
	script := readRepoFile(t, "scripts/install-test-browser.sh")
	for _, want := range []string{"install-deps chromium", "timeout ", "launches"} {
		if !strings.Contains(script, want) {
			t.Errorf("scripts/install-test-browser.sh no longer holds %q: the bounded apt attempt and the fallback launch are what #2609 relies on", want)
		}
	}
}
