package sitetypecheck

import (
	"bytes"
	"context"
	"encoding/json"
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
		State: filepath.Join(dir, "state"), CompileCache: filepath.Join(dir, "cache"),
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
