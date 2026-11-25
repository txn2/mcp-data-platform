package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bareGoInstall is a workflow line that runs `go install` itself rather than
// through scripts/go-install.sh.
var bareGoInstall = regexp.MustCompile(`(^|[\s;&|(])go\s+install\s`)

// TestWorkflowsInstallGoToolsThroughTheScript pins #2789: every workflow
// installs a Go tool through scripts/go-install.sh, which tries the install
// again when the module proxy drops a download. A bare `go install` of air
// failed an acceptance job of the E2E Nightly on 2026-11-24 when
// proxy.golang.org reset one stream, before any criterion ran.
func TestWorkflowsInstallGoToolsThroughTheScript(t *testing.T) {
	workflows, err := filepath.Glob("../../.github/workflows/*.yml")
	require(t, err)
	if len(workflows) == 0 {
		t.Fatal("no .github/workflows/*.yml found")
	}
	callers := 0
	for _, path := range workflows {
		raw, err := os.ReadFile(path) //nolint:gosec // a path globbed from this repository
		require(t, err)
		for i, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if bareGoInstall.MatchString(trimmed) {
				t.Errorf("%s:%d runs go install directly; run scripts/go-install.sh instead: %s",
					filepath.Base(path), i+1, trimmed)
			}
			if strings.Contains(trimmed, "scripts/go-install.sh ") {
				callers++
			}
		}
	}
	if callers == 0 {
		t.Error("no workflow installs a tool through scripts/go-install.sh; the acceptance jobs install air through it")
	}
	script := readRepoFile(t, "scripts/go-install.sh")
	for _, want := range []string{"GO_INSTALL_ATTEMPTS", `go install "$@"`, "sleep "} {
		if !strings.Contains(script, want) {
			t.Errorf("scripts/go-install.sh no longer holds %q: the retried install is what #2789 relies on", want)
		}
	}
}
