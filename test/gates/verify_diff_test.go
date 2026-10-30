package gates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readRepoFile reads a file of this repository by its path from the root.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	// #nosec G304 -- rel is a constant path inside this repository.
	data, err := os.ReadFile(filepath.Join("..", "..", rel))
	require(t, err)
	return string(data)
}

// verifyDiffRepo is a repository holding scripts/verify-diff.py and the
// schedule lane whose diff helpers it loads, with one route registered on main.
func verifyDiffRepo(t *testing.T) string {
	t.Helper()
	return newGitRepo(t, "scripts/verify-diff.py", map[string]string{
		"go.mod":                       "module example.com/m\n\ngo 1.25\n",
		"scripts/schedule-lane.py":     readRepoFile(t, "scripts/schedule-lane.py"),
		"internal/web/routes.go":       "package web\n\nimport \"net/http\"\n\nfunc Register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"GET /a\", nil)\n}\n",
		"internal/web/routes_test.go":  "package web\n",
		"internal/other/other.go":      "package other\n",
		"internal/other/other_test.go": "package other\n",
	})
}

// TestVerifyDiffNamesTheChangedPackagesForGosec pins #2186's gosec step: a
// package with a changed production file is scanned, a package whose only
// change is a test file is not (gosec does not read tests), and an untracked
// file counts.
func TestVerifyDiffNamesTheChangedPackagesForGosec(t *testing.T) {
	root := verifyDiffRepo(t)
	writeFile(t, root, "internal/other/other_test.go", "package other\n\n// changed\n")
	out, ok := runScript(t, root, []string{"scripts/verify-diff.py", "gosec-packages"}, nil)
	if !ok || strings.TrimSpace(out) != "" {
		t.Fatalf("a test-only change named packages for gosec: ok=%v %q", ok, out)
	}

	writeFile(t, root, "internal/web/new.go", "package web\n")
	out, ok = runScript(t, root, []string{"scripts/verify-diff.py", "gosec-packages"}, nil)
	if !ok || strings.TrimSpace(out) != "./internal/web" {
		t.Fatalf("an untracked production file: ok=%v %q, want ./internal/web", ok, out)
	}
}

// TestVerifyDiffRunsTheRouteGateOnlyOnARegistration pins #2186's route
// step: an edit to a file that registers routes but leaves the registrations
// alone skips the gate, and an added or removed registration runs it.
func TestVerifyDiffRunsTheRouteGateOnlyOnARegistration(t *testing.T) {
	root := verifyDiffRepo(t)
	routes := []string{"scripts/verify-diff.py", "routes-changed"}

	writeFile(t, root, "internal/web/routes.go", "package web\n\nimport \"net/http\"\n\n// Register mounts the routes.\nfunc Register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"GET /a\", nil)\n}\n")
	if out, ok := runScript(t, root, routes, nil); ok {
		t.Fatalf("a comment beside a registration ran the route gate:\n%s", out)
	}

	writeFile(t, root, "internal/web/routes_test.go", "package web\n\nfunc x() { mux.HandleFunc(\"GET /t\", nil) }\n")
	if out, ok := runScript(t, root, routes, nil); ok {
		t.Fatalf("a registration in a test file ran the route gate:\n%s", out)
	}

	writeFile(t, root, "internal/web/routes.go", "package web\n\nimport \"net/http\"\n\nfunc Register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"GET /a\", nil)\n\tmux.Handle(\"POST /b\", nil)\n}\n")
	out, ok := runScript(t, root, routes, nil)
	assertOutput(t, out, ok, expect{pass: true, want: []string{"internal/web/routes.go", `mux.Handle("POST /b", nil)`}})

	runIn(t, root, "git", "checkout", "-q", "--", "internal/web/routes.go")
	writeFile(t, root, "internal/web/routes.go", "package web\n\nimport \"net/http\"\n\nfunc Register(mux *http.ServeMux) {}\n")
	if out, ok := runScript(t, root, routes, nil); !ok {
		t.Fatalf("a removed registration did not run the route gate:\n%s", out)
	}
}
