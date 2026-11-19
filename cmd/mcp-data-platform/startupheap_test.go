package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWatch is a heapWatch over a heap the test sets.
func fakeWatch(dir string, heap *uint64) *heapWatch {
	h, err := openHeapWatch(dir)
	if err != nil {
		h = newHeapWatch(dir)
	}
	h.read = func() uint64 { return *heap }
	h.now = func() time.Time { return time.Unix(1700000000, 0) }
	return h
}

// #2721: a profile is written each time the live heap passes the next
// threshold, which doubles; a jump past several writes one.
func TestHeapWatch_WritesAtEachDoubling(t *testing.T) {
	dir := t.TempDir()
	heap := uint64(100 << 20)
	h := fakeWatch(dir, &heap)

	path, err := h.observe()
	require.NoError(t, err)
	assert.Empty(t, path, "under the first threshold nothing is written")

	heap = 300 << 20
	path, err = h.observe()
	require.NoError(t, err)
	require.NotEmpty(t, path)
	assert.True(t, strings.HasSuffix(path, "-300MiB.pb.gz"), path)
	assert.Equal(t, uint64(512<<20), h.next)
	path, err = h.observe()
	require.NoError(t, err)
	assert.Empty(t, path, "the same heap writes once")

	heap = 3 << 30
	path, err = h.observe()
	require.NoError(t, err)
	require.NotEmpty(t, path)
	assert.Equal(t, uint64(4<<30), h.next, "a jump past several thresholds writes one profile")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	b, err := os.ReadFile(path) //nolint:gosec // a path the test made
	require.NoError(t, err)
	require.Greater(t, len(b), 2)
	assert.Equal(t, []byte{0x1f, 0x8b}, b[:2], "a gzipped pprof heap profile")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// A failed write is reported and moves the threshold on, so it is not retried
// on every read; a directory that cannot be written is reported.
func TestHeapWatch_Failures(t *testing.T) {
	heap := uint64(300 << 20)
	h := fakeWatch(t.TempDir(), &heap)
	h.write = func(io.Writer) error { return errors.New("disk full") }
	_, err := h.observe()
	require.ErrorContains(t, err, "disk full")
	assert.Equal(t, uint64(512<<20), h.next)

	unopened := newHeapWatch(filepath.Join(t.TempDir(), "absent"))
	unopened.read = func() uint64 { return heap }
	_, err = unopened.observe()
	require.ErrorContains(t, err, "is not open")

	gone := fakeWatch(t.TempDir(), &heap)
	gone.next = startupHeapFirst
	require.NoError(t, gone.root.Close())
	_, err = gone.observe()
	require.Error(t, err, "a closed root writes nothing")
}

// The watch writes into its directory and stops when its window ends.
func TestRunHeapWatch_StopsAtTheWindow(t *testing.T) {
	dir := t.TempDir()
	heap := uint64(300 << 20)
	h := fakeWatch(dir, &heap)
	done := make(chan struct{})
	go func() {
		runHeapWatch(context.Background(), h, 100*time.Millisecond, 5*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not stop at the end of its window")
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	failing := fakeWatch(dir, &heap)
	failing.write = func(io.Writer) error { return errors.New("no") }
	runHeapWatch(context.Background(), failing, 20*time.Millisecond, 5*time.Millisecond)
}

// No directory, no watch; a directory that cannot be made is reported and no
// watch starts; the live heap reads as the runtime reports it.
func TestStartStartupHeapWatch(t *testing.T) {
	ctx := t.Context()
	startStartupHeapWatch(ctx, "")

	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	startStartupHeapWatch(ctx, filepath.Join(file, "sub"))

	startStartupHeapWatch(ctx, filepath.Join(t.TempDir(), "made"))
	assert.Positive(t, liveHeap())

	var buf bytes.Buffer
	require.NoError(t, newHeapWatch(t.TempDir()).write(&buf))
	assert.NotZero(t, buf.Len())
}
