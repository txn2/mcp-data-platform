package sitetypecheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// The TypeScript compiler a check runs: the @typescript/typescript-wasip1-wasm
// build pinned by version and by the integrity hash the registry publishes for
// it. The package is experimental and only dev builds exist; move the pin to a
// stable build once one is published.
const (
	CompilerPackage   = "@typescript/typescript-wasip1-wasm"
	CompilerVersion   = "7.1.0-dev.20261030.1"
	compilerIntegrity = "sha512-m3WKNe9Wa0EsTOJXpf1t55hB7TzArcfuiygFi5MgD6EL5xB3eUTN9xNRklv/m0yyXSoVmpqdYNgujjgroCPlNA=="
)

// Files and modes a check writes on the replica's disk.
const (
	wasmFile  = "tsc.wasm"
	dirMode   = 0o750
	fileMode  = 0o600
	digestLen = 16
)

// compilerFiles is where the compiler is laid out on a replica's disk: the
// WebAssembly module and the standard library beside it.
type compilerFiles struct {
	Wasm string
	Lib  string
}

// compilerStore lays the compiler out once per process.
type compilerStore struct {
	getter   Getter
	cache    Cache
	dir      string
	registry string

	mu  sync.Mutex
	got *compilerFiles
}

// ensure is the compiler on this replica's disk: already there, from the
// bucket, or fetched from the registry and verified against the pinned hash.
func (s *compilerStore) ensure(ctx context.Context) (*compilerFiles, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.got != nil {
		return s.got, nil
	}
	root := filepath.Join(s.dir, "tsc", CompilerVersion)
	files := &compilerFiles{Wasm: filepath.Join(root, wasmFile), Lib: filepath.Join(root, "lib")}
	if _, err := os.Stat(filepath.Join(root, ".complete")); err == nil {
		s.got = files
		return files, nil
	}
	kept, err := s.compilerKept(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeTree(root, kept); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, ".complete"), nil, fileMode); err != nil {
		return nil, fmt.Errorf("laying out the compiler: %w", err)
	}
	s.got = files
	return files, nil
}

// compilerKept is the compiler's files, from the bucket or the registry.
func (s *compilerStore) compilerKept(ctx context.Context) (map[string][]byte, error) {
	if kept, ok := s.cache.get(ctx, "compiler", compilerIntegrity); ok && kept[wasmFile] != nil {
		return kept, nil
	}
	url := registryTarball(s.registry, CompilerPackage, CompilerVersion)
	tgz, err := s.getter.Get(ctx, url, maxCompilerBytes)
	if err != nil {
		return nil, fmt.Errorf("fetching the TypeScript compiler: %w", err)
	}
	if err := verifyIntegrity(tgz, compilerIntegrity); err != nil {
		return nil, fmt.Errorf("the TypeScript compiler fetched from the registry: %w", err)
	}
	kept, err := compilerFrom(tgz)
	if err != nil {
		return nil, err
	}
	s.cache.put(ctx, "compiler", compilerIntegrity, kept)
	return kept, nil
}

// compilerFrom reads the compiler package: lib/tsc.wasm as tsc.wasm, and each
// lib/*.d.ts as lib/<name>.
func compilerFrom(tgz []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("the compiler package is not a gzip tarball: %w", err)
	}
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the compiler package: %w", err)
		}
		name, ok := compilerEntry(hdr)
		if !ok {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxCompilerBytes))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		out[name] = data
	}
	if out[wasmFile] == nil {
		return nil, errors.New("the compiler package holds no lib/tsc.wasm")
	}
	return out, nil
}

// compilerEntry is the name a compiler package entry is kept under: the
// module as tsc.wasm, each lib/*.d.ts as itself; false for anything else.
func compilerEntry(hdr *tar.Header) (string, bool) {
	name, ok := packagePath(hdr.Name)
	if !ok || hdr.Typeflag != tar.TypeReg {
		return "", false
	}
	switch {
	case name == "lib/"+wasmFile:
		return wasmFile, true
	case strings.HasPrefix(name, "lib/") && strings.HasSuffix(name, ".d.ts") && path.Dir(name) == "lib":
		return name, true
	default:
		return "", false
	}
}

// writeTree writes files under root by their slash paths, refusing a path
// that leaves it.
func writeTree(root string, files map[string][]byte) error {
	for name, data := range files {
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("refusing to write %q outside the check", name)
		}
		full := filepath.Join(root, clean)
		if err := os.MkdirAll(filepath.Dir(full), dirMode); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
		if err := os.WriteFile(full, data, fileMode); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// digest is a short content hash, for naming a check's scratch directories.
func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = io.WriteString(h, p)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:digestLen]
}
