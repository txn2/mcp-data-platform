package structure_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2EAdminServerIsTheProductionAssembly holds the E2E suite to the admin
// handler the server mounts (#2037). Its hand-built copy of admin.Deps lacked
// the key store #1715 added, so the nightly exercised a server no deployment
// runs and failed for three weeks. Every E2E admin server comes from
// httpserver.AdminHandler; a test under test/e2e that assembles admin.Deps
// itself has started a second copy.
func TestE2EAdminServerIsTheProductionAssembly(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, "test", "e2e")
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(readRepoFile(t, rel), "\n") {
			if strings.Contains(line, "admin.Deps{") || strings.Contains(line, "admin.NewHandler(") {
				t.Errorf("%s:%d assembles the admin handler itself; build it with httpserver.AdminHandler: %s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
	}
	if !strings.Contains(readRepoFile(t, "test", "e2e", "helpers", "admin.go"), "httpserver.AdminHandler(") {
		t.Error("test/e2e/helpers/admin.go no longer builds its handler with httpserver.AdminHandler")
	}
}
