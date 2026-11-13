package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// devEmbed runs scripts/dev-embed.sh in a root holding the embed files named,
// with a make that records its arguments and fails when told to. It returns
// the output, whether it passed, and what make was asked to do.
func devEmbed(t *testing.T, present []string, makeFails bool) (out string, ok bool, made string) {
	t.Helper()
	root := t.TempDir()
	script, err := os.ReadFile("../../scripts/dev-embed.sh")
	require(t, err)
	writeFile(t, root, "scripts/dev-embed.sh", string(script))
	for _, rel := range present {
		writeFile(t, root, rel, "built\n")
	}
	calls := filepath.Join(t.TempDir(), "make-calls")
	fake := "#!/bin/sh\necho \"$@\" >> " + calls + "\necho npm output\n"
	if makeFails {
		fake += "echo 'vite: build failed'\nexit 2\n"
	}
	writeFile(t, root, "fake-make", fake)
	require(t, os.Chmod(filepath.Join(root, "fake-make"), 0o700)) // #nosec G302 -- a test fixture the test runs
	cmd := exec.CommandContext(t.Context(), "bash", "scripts/dev-embed.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MAKE="+filepath.Join(root, "fake-make"))
	b, err := cmd.CombinedOutput()
	data, _ := os.ReadFile(calls) // #nosec G304 -- a file under t.TempDir()
	return string(b), err == nil, strings.TrimSpace(string(data))
}

const (
	embedUI     = "internal/ui/dist/index.html"
	embedViewer = "internal/contentviewer/dist/.vite/manifest.json"
)

// TestDevEmbedBuildsAnEmptyEmbed pins #2548's third ask: the stack is never
// compiled from the empty embed make verify's embed-clean leaves. Either half
// missing builds the portal; both present builds nothing; a failed build fails
// and shows why.
func TestDevEmbedBuildsAnEmptyEmbed(t *testing.T) {
	t.Parallel()
	out, ok, made := devEmbed(t, []string{embedUI, embedViewer}, false)
	if !ok || made != "" || out != "" {
		t.Errorf("with both builds in place: ok=%v, make %q, output %q; want nothing done", ok, made, out)
	}
	for _, present := range [][]string{nil, {embedUI}, {embedViewer}} {
		out, ok, made = devEmbed(t, present, false)
		if !ok || made != "--no-print-directory frontend-build" {
			t.Errorf("with only %v: ok=%v, make %q\n%s", present, ok, made, out)
		}
		if !strings.Contains(out, "missing") || !strings.Contains(out, "dev-embed: built") {
			t.Errorf("with only %v the script did not say what it built:\n%s", present, out)
		}
	}
	out, ok, _ = devEmbed(t, nil, true)
	assertOutput(t, out, ok, expect{pass: false, want: []string{"make frontend-build failed", "vite: build failed"}})
}

// TestDevStackBuildsTheEmbedBeforeEachCompile pins where it runs: make dev
// before it compiles the server, and air (both replicas) before each rebuild.
func TestDevStackBuildsTheEmbedBeforeEachCompile(t *testing.T) {
	t.Parallel()
	start := readRepoFile(t, "dev/start.sh")
	embed := strings.Index(start, "bash scripts/dev-embed.sh ||")
	compile := strings.Index(start, `if ! go build -o ./build/air/mcp-data-platform`)
	if embed < 0 || compile < 0 || embed > compile {
		t.Errorf("dev/start.sh must run scripts/dev-embed.sh before it compiles the server (embed at %d, compile at %d)", embed, compile)
	}
	if !strings.Contains(start, `-build.cmd "bash scripts/dev-embed.sh && go build -o ./build/air-b/`) {
		t.Error("the second replica's air rebuilds without scripts/dev-embed.sh")
	}
	if !strings.Contains(readRepoFile(t, "dev/.air.toml"), `cmd = "bash scripts/dev-embed.sh && go build `) {
		t.Error("dev/.air.toml rebuilds without scripts/dev-embed.sh")
	}
}
