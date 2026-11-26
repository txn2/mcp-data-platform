package sitetypecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emptyCommand is the smallest WASI command: a _start that returns at once.
var emptyCommand = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic, version
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: () -> ()
	0x03, 0x02, 0x01, 0x00, // function 0 of type 0
	0x07, 0x0a, 0x01, 0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00, // export _start
	0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b, // code: no locals, end
}

func workerJob(t *testing.T, wasm []byte) job {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tsc.wasm")
	require.NoError(t, os.WriteFile(path, wasm, 0o600))
	for _, d := range []string{"project", "lib", "state", "cache"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, d), 0o700))
	}
	return job{
		Wasm: path, Project: filepath.Join(dir, "project"), Lib: filepath.Join(dir, "lib"),
		State:    filepath.Join(dir, "state"),
		Tsconfig: "tsconfig.json", MemoryLimit: 64 << 20,
	}
}

// TestRunWorker runs one job through the worker's whole protocol: a job read
// from stdin, the module run under its memory limit with the project, the
// standard library and the state mounted, and the outcome written to stdout.
func TestRunWorker(t *testing.T) {
	j := workerJob(t, emptyCommand)
	in, err := json.Marshal(j)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, RunWorker(context.Background(), bytes.NewReader(in), &out))
	var o outcome
	require.NoError(t, json.Unmarshal(out.Bytes(), &o))
	assert.Equal(t, outcome{}, o, "a module that returns at once exits cleanly")

	require.Error(t, RunWorker(context.Background(), strings.NewReader("{"), &out), "a job that is not JSON")
}

func TestRunJobFailures(t *testing.T) {
	j := workerJob(t, emptyCommand)
	j.Wasm = filepath.Join(t.TempDir(), "missing.wasm")
	assert.NotEmpty(t, runJob(context.Background(), j).Error, "a missing compiler")

	bad := workerJob(t, []byte("not wasm"))
	assert.Contains(t, runJob(context.Background(), bad).Error, "compiling the TypeScript compiler")
}

// TestRunJobOverItsMemory: a module declaring more memory than the job's
// limit is refused as over the limit, not as a broken compiler.
func TestRunJobOverItsMemory(t *testing.T) {
	twoPages := append(append([]byte(nil), emptyCommand[:18]...), 0x05, 0x03, 0x01, 0x00, 0x02) // memory: min 2 pages
	twoPages = append(twoPages, emptyCommand[18:]...)
	j := workerJob(t, twoPages)
	j.MemoryLimit = wasmPage // one page
	o := runJob(context.Background(), j)
	assert.True(t, o.OverMemory, "outcome = %+v", o)

	j.MemoryLimit = 4 * wasmPage
	assert.Equal(t, outcome{}, runJob(context.Background(), j), "within the limit it runs")
}

func TestBoundedBuffer(t *testing.T) {
	var b boundedBuffer
	n, err := b.Write(bytes.Repeat([]byte("x"), maxOutputBytes+10))
	require.NoError(t, err)
	assert.Equal(t, maxOutputBytes+10, n, "a write past the bound is accepted and dropped")
	_, _ = b.Write([]byte("more"))
	assert.Len(t, b.String(), maxOutputBytes)
}

// TestOutcomeOfACrashKeepsThePanic: a compiler that trapped is reported with
// the panic it wrote to stderr, bounded, ahead of the runtime's error (#2830).
func TestOutcomeOfACrashKeepsThePanic(t *testing.T) {
	trap := errors.New("module[] function[_start] failed: wasm error: unreachable")
	o := outcomeOf(job{}, trap, "", "panic: path must not be empty\n\ngoroutine 1 [running]:\n")
	assert.True(t, strings.HasPrefix(o.Error, "panic: path must not be empty"), o.Error)
	assert.Contains(t, o.Error, "wasm error: unreachable")

	long := outcomeOf(job{}, trap, "", "panic: x\n"+strings.Repeat("frame\n", 5000))
	assert.Less(t, len(long.Error), maxCrashBytes+200, "the crash text is bounded")

	assert.Equal(t, trap.Error(), outcomeOf(job{}, trap, "", "").Error, "no stderr keeps the runtime's error")
}

// TestDefaultMemoryLimitFor: the compiler's default is a quarter of the
// container's limit, within its floor and cap (#2828).
func TestDefaultMemoryLimitFor(t *testing.T) {
	for container, want := range map[int64]int64{
		0:        DefaultMemoryLimit,
		1 << 30:  MinDefaultMemoryLimit,
		4 << 30:  1 << 30,
		8 << 30:  2 << 30,
		64 << 30: DefaultMemoryLimit,
	} {
		assert.Equal(t, want, DefaultMemoryLimitFor(container), "container %d MiB", container>>20)
	}
}

// TestCeilingWarning: a worker that could take more than half the container
// is warned about at startup, naming the setting (#2828).
func TestCeilingWarning(t *testing.T) {
	assert.Empty(t, CeilingWarning(1<<30, 4<<30), "the default in a 4 GiB container")
	assert.Empty(t, CeilingWarning(3<<30, 0), "no container limit read")
	w := CeilingWarning(3<<30, 4<<30)
	assert.Contains(t, w, "sites.typecheck.memory_limit 3072 MiB")
	assert.Contains(t, w, "4096 MiB limit")
}
