package sitetypecheck

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

// wazeroModule is the module path wazero is built in from; its version names
// the directory wazero keeps a compilation cache in.
const wazeroModule = "github.com/tetratelabs/wazero"

// cacheDir is the compilation cache a check's worker reads and writes, or ""
// when the deployment keeps none.
func (c *Checker) cacheDir() string {
	if !c.cfg.CompileCache {
		return ""
	}
	return filepath.Join(c.cfg.Dir, compileCacheDir, CompilerVersion)
}

// pruneCompileCache leaves under dir only the compilation cache a check of
// this build reads (#2833): all of it removed when the deployment keeps none,
// so turning the setting off frees the disk; otherwise every other compiler
// version, every other wazero version's directory, and the partial files a
// worker stopped mid-write leaves.
func pruneCompileCache(dir string, keep bool) {
	root := filepath.Join(dir, compileCacheDir)
	if !keep {
		_ = os.RemoveAll(root)
		return
	}
	removeAllBut(root, CompilerVersion)
	versioned := filepath.Join(root, CompilerVersion)
	current := wazeroCacheName()
	if current == "" {
		return
	}
	removeAllBut(versioned, current)
	partial, _ := filepath.Glob(filepath.Join(versioned, current, "*.tmp"))
	for _, p := range partial {
		_ = os.Remove(p)
	}
}

// removeAllBut removes every entry of dir other than keep.
func removeAllBut(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// wazeroCacheName is the directory wazero keeps this build's compiled code in
// under a cache directory: its version, architecture and system, read as
// wazero reads them. "" when the build records no wazero version, where
// nothing is pruned.
func wazeroCacheName() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return wazeroCacheNameIn(info)
}

// wazeroCacheNameIn is wazeroCacheName for a build's recorded information.
func wazeroCacheNameIn(info *debug.BuildInfo) string {
	version := ""
	for _, dep := range info.Deps {
		if strings.Contains(dep.Path, wazeroModule) {
			version = dep.Version
		}
	}
	if version == "" || version == "(devel)" {
		return ""
	}
	return "wazero-" + version + "-" + runtime.GOARCH + "-" + runtime.GOOS
}
