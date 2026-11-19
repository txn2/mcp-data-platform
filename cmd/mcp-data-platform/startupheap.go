package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/metrics"
	"runtime/pprof"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
)

// startupHeapEnvDir names a directory a starting process writes heap
// profiles to (#2721). A replica whose startup work outgrows its memory limit
// is killed within seconds, before anyone can pull a profile from PPROF_ADDR,
// so the process writes its own: one each time the live heap doubles from
// startupHeapFirst, during its first startupHeapWindow. Empty (the default)
// writes nothing. A heap profile holds stack traces and allocation sizes, never
// the contents of memory. On Kubernetes an emptyDir keeps the files across the
// restart that follows an OOM kill.
const startupHeapEnvDir = "MCP_PLATFORM_STARTUP_HEAP_PROFILE_DIR"

const (
	// startupHeapWindow is how long after start the heap is watched.
	startupHeapWindow = 10 * time.Minute
	// startupHeapEvery is how often it is read. Startup work that is killed
	// climbed about 100 MB a second, so a quarter second misses little.
	startupHeapEvery = 250 * time.Millisecond
	// startupHeapFirst is the first live heap a profile is written at.
	startupHeapFirst = 256 << 20
	// startupHeapMetric is the live heap: the bytes of heap objects, read
	// without a stop-the-world.
	startupHeapMetric = "/memory/classes/heap/objects:bytes"
	// startupHeapDirMode and startupHeapFileMode keep the directory and the
	// profiles to the process's own user.
	startupHeapDirMode  = 0o750
	startupHeapFileMode = 0o600
)

// heapWatch writes a heap profile each time the live heap passes its next
// threshold, which doubles after each one.
type heapWatch struct {
	dir   string
	root  *os.Root
	pid   int
	next  uint64
	read  func() uint64
	write func(io.Writer) error
	now   func() time.Time
}

// newHeapWatch watches the process's own heap into dir.
func newHeapWatch(dir string) *heapWatch {
	return &heapWatch{
		dir: dir, pid: os.Getpid(), next: startupHeapFirst,
		read:  liveHeap,
		write: func(w io.Writer) error { return pprof.Lookup("heap").WriteTo(w, 0) }, //nolint:wrapcheck // observe wraps it
		now:   time.Now,
	}
}

// liveHeap reads the live heap from the runtime's metrics.
func liveHeap() uint64 {
	sample := []metrics.Sample{{Name: startupHeapMetric}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

// observe reads the heap once and, past the next threshold, writes a profile
// and returns its path. A failed write moves the threshold on too, so a full
// disk is not retried every read.
func (h *heapWatch) observe() (string, error) {
	live := h.read()
	if live < h.next {
		return "", nil
	}
	for h.next <= live {
		h.next *= 2
	}
	name := fmt.Sprintf("heap-%d-%d-%dMiB.pb.gz", h.now().Unix(), h.pid, live>>20)
	path := filepath.Join(h.dir, name)
	if h.root == nil {
		return "", fmt.Errorf("startup heap profile: %s is not open", h.dir)
	}
	// Written through a root opened on the directory, so the name cannot
	// leave it.
	f, err := h.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, startupHeapFileMode)
	if err != nil {
		return "", fmt.Errorf("startup heap profile: %w", err)
	}
	werr := h.write(f)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", fmt.Errorf("startup heap profile: %w", werr)
	}
	return path, nil
}

// startStartupHeapWatch watches the heap for startupHeapWindow when dir is
// set, writing the profiles there.
func startStartupHeapWatch(ctx context.Context, dir string) {
	if dir == "" {
		return
	}
	h, err := openHeapWatch(dir)
	if err != nil {
		slog.Warn("startup heap profiles: the directory cannot be opened; none are written", logKeyError, err)
		return
	}
	slog.Info("startup heap profiles: writing one each time the live heap doubles", "directory", dir,
		"from_mib", startupHeapFirst>>20, "for", startupHeapWindow.String())
	go func() {
		defer func() { _ = h.root.Close() }()
		runHeapWatch(ctx, h, startupHeapWindow, startupHeapEvery)
	}()
}

// openHeapWatch makes dir if it is missing and opens it as the root the
// profiles are written under. dir is the operator's own setting, read from the
// environment at start, never a request's.
func openHeapWatch(dir string) (*heapWatch, error) {
	if err := os.MkdirAll(dir, startupHeapDirMode); err != nil { // #nosec G703 -- the operator's own environment setting, read once at start
		return nil, fmt.Errorf("making %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dir, err)
	}
	h := newHeapWatch(dir)
	h.root = root
	return h, nil
}

// runHeapWatch observes h every interval until window has passed or ctx ends.
func runHeapWatch(ctx context.Context, h *heapWatch, window, every time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	bgloop.Run(ctx, bgloop.Loop{
		Name: bgloop.NameStartupHeap, Every: every, Immediate: true, SpanPerUnit: true,
		Body: func(context.Context) error {
			path, err := h.observe()
			if err != nil {
				slog.Warn("startup heap profiles: writing one failed", logKeyError, err)
				return err
			}
			if path != "" {
				slog.Warn("startup heap profile written", "path", path)
			}
			return nil
		},
	})
}
