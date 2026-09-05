// Package structure_test holds the project's structural gates: the invariants
// that are about the shape of the tree rather than the behavior of one
// package. Package budgets, the import ratchet, the public-surface policy, the
// god-object budget, dead-package and noop-interface checks, the integration
// guard, persona coherence and the pinned-figure checks all live here.
//
// They walk the repository, so each one needs the module root rather than its
// own directory. moduleRoot finds it; nothing here reads a path relative to
// the working directory.
package structure_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// moduleRoot returns the absolute path of the module root: the nearest
// ancestor of this package holding go.mod. The gates walk the tree from there,
// which is what lets them live in a suite of their own rather than in the
// root directory just to make filepath.Abs(".") mean the right thing.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := rootOnce()
	if err != nil {
		t.Fatalf("locating the module root: %v", err)
	}
	return root
}

// rootOnce caches the walk: every gate in this package asks for the same
// answer, and the filesystem does not move underneath a test run.
var rootOnce = sync.OnceValues(findModuleRoot)

func findModuleRoot() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", fmt.Errorf("resolving the working directory: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above this package: %w", os.ErrNotExist)
		}
		dir = parent
	}
}

// rootPath joins one or more repository-relative segments onto the module
// root, so a gate names the path it reads the way the tree spells it.
func rootPath(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join(append([]string{moduleRoot(t)}, parts...)...)
}
