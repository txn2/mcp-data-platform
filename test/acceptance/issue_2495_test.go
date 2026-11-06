//go:build integration

package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Issue #2495: during a rolling update a page load could take index.html
// from a replica on one build and get 404 for that build's entry script or a
// preloaded module from a replica on the other, leaving a blank page. The
// index.html the platform serves now carries an inline script that reloads
// once and then says the portal is being updated.
//
// The page is the one the platform's binary embeds and serves at /portal/.
// The browser cases in ui/e2e/boot-recovery make the entry or a preloaded
// module answer 404 in Chromium and assert the reload and the message.
//
// Wire forms: the browser's own navigations; no tool is called.

// TestIssue2495_AMissingBuildFileDoesNotLeaveABlankPage runs the boot
// recovery cases against the platform's own /portal/.
func TestIssue2495_AMissingBuildFileDoesNotLeaveABlankPage(t *testing.T) {
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/boot-recovery/playwright.config.ts")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "BOOT_RECOVERY_BASE_URL="+baseURL())
	out, err := cmd.CombinedOutput()
	t.Logf("playwright against %s:\n%s", baseURL(), out)
	if err != nil {
		t.Fatalf("the boot recovery cases failed: %v", err)
	}
}
