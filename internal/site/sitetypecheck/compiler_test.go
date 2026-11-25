package sitetypecheck

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompilerKept: with nothing in the bucket the compiler is fetched from
// the registry and held to its pinned hash; a registry that does not answer,
// or answers with another tarball, is refused naming which.
func TestCompilerKept(t *testing.T) {
	url := registryTarball(defaultRegistry, CompilerPackage, CompilerVersion)

	s := &compilerStore{getter: &fakeGetter{}, registry: defaultRegistry}
	_, err := s.compilerKept(context.Background())
	require.ErrorContains(t, err, "fetching the TypeScript compiler")

	other := tgz(t, map[string]string{"lib/tsc.wasm": "\x00asm"})
	s = &compilerStore{getter: &fakeGetter{bodies: map[string][]byte{url: other}}, registry: defaultRegistry}
	_, err = s.compilerKept(context.Background())
	require.ErrorIs(t, err, ErrIntegrity, "a tarball that is not the pinned one is never run")
	assert.Contains(t, err.Error(), "fetched from the registry")
}

func TestCompilerFromRefusesAPackageWithoutTheCompiler(t *testing.T) {
	_, err := compilerFrom([]byte("not gzip"))
	require.Error(t, err)
	_, err = compilerFrom(tgz(t, map[string]string{"lib/lib.d.ts": ""}))
	require.ErrorContains(t, err, "no lib/tsc.wasm")
}

func TestDigest(t *testing.T) {
	assert.Len(t, digest("a", "b"), 16)
	assert.Equal(t, digest("a", "b"), digest("a", "b"))
	assert.NotEqual(t, digest("ab"), digest("a", "b"), "parts are kept apart")
}
