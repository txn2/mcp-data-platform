package sitetypecheck

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPruneCompileCache: only the cache this build reads is left (#2833), and
// none at all when the deployment keeps none.
func TestPruneCompileCache(t *testing.T) {
	current := wazeroCacheName()
	if current == "" {
		t.Skip("this test binary records no wazero version")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, compileCacheDir)
	keep := filepath.Join(root, CompilerVersion, current, "entry")
	for _, p := range []string{
		keep,
		filepath.Join(root, CompilerVersion, current, "entry.123.tmp"),
		filepath.Join(root, CompilerVersion, "wazero-v1.0.0-amd64-linux", "entry"),
		filepath.Join(root, "7.0.0-dev.1", current, "entry"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	}
	pruneCompileCache(dir, true)
	var left []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			left = append(left, p)
		}
		return nil
	})
	assert.Equal(t, []string{keep}, left)

	pruneCompileCache(dir, false)
	assert.NoDirExists(t, root, "turning the cache off frees its disk")
	pruneCompileCache(t.TempDir(), true) // nothing there yet
}

func TestWazeroCacheNameIn(t *testing.T) {
	suffix := "-" + runtime.GOARCH + "-" + runtime.GOOS
	for want, deps := range map[string][]*debug.Module{
		"wazero-v1.12.0" + suffix: {{Path: "golang.org/x/sync", Version: "v0.1.0"}, {Path: wazeroModule, Version: "v1.12.0"}},
		"":                        {{Path: wazeroModule, Version: "(devel)"}},
	} {
		assert.Equal(t, want, wazeroCacheNameIn(&debug.BuildInfo{Deps: deps}))
	}
	assert.Empty(t, wazeroCacheNameIn(&debug.BuildInfo{}))
}

// TestCheckerPassesTheCacheDirectory: a checker keeping the cache names its
// directory in every job; one keeping none names none.
func TestCheckerPassesTheCacheDirectory(t *testing.T) {
	dir := t.TempDir()
	on := NewChecker(Config{Dir: dir, CompileCache: true}, nil, Cache{})
	assert.Equal(t, filepath.Join(dir, compileCacheDir, CompilerVersion), on.cacheDir())
	assert.Empty(t, NewChecker(Config{Dir: dir}, nil, Cache{}).cacheDir())
}
