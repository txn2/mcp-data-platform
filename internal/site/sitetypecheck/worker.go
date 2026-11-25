package sitetypecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// WorkerCommand is the argument that runs the platform binary as a check's
// worker process: one job on stdin, one outcome on stdout.
const WorkerCommand = "typecheck-worker"

// wasmPage is the WebAssembly page size, the unit wazero's memory limit is in;
// maxWasmPages is the 4 GiB a wasm32 module addresses.
const (
	wasmPage     = 64 << 10
	maxWasmPages = 1 << 16
)

// maxOutputBytes bounds what a check's compiler output is kept to.
const maxOutputBytes = 4 << 20

// job is one check as the worker runs it, all paths on the replica's disk.
type job struct {
	Wasm         string `json:"wasm"`
	CompileCache string `json:"compile_cache"`
	// Project is the release with its node_modules, mounted read-only at /.
	Project string `json:"project"`
	// Lib is the standard library, mounted read-only at /ts/lib.
	Lib string `json:"lib"`
	// State is the environment's incremental state, mounted writable at
	// /state; empty runs without it.
	State string `json:"state"`
	// Tsconfig is the project file, a path in the release.
	Tsconfig string `json:"tsconfig"`
	// MemoryLimit is the most the compiler may hold, in bytes.
	MemoryLimit int64 `json:"memory_limit"`
}

// outcome is what the worker reports.
type outcome struct {
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output"`
	OverMemory bool   `json:"over_memory"`
	Error      string `json:"error,omitempty"`
}

// RunWorker runs one job read from in and writes its outcome to out. It is
// the whole of the worker process: the replica's memory never holds the
// compiler's.
func RunWorker(ctx context.Context, in io.Reader, out io.Writer) error {
	var j job
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&j); err != nil {
		return fmt.Errorf("reading the job: %w", err)
	}
	return json.NewEncoder(out).Encode(runJob(ctx, j)) //nolint:wrapcheck // the parent reads a missing outcome as a failed worker
}

// runJob runs the compiler once.
func runJob(ctx context.Context, j job) outcome {
	wasm, err := readFile(j.Wasm)
	if err != nil {
		return outcome{Error: err.Error()}
	}
	rt := wazero.NewRuntimeWithConfig(ctx, runtimeConfig(j))
	defer func() { _ = rt.Close(ctx) }()
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		// A module whose declared memory is over the limit is refused here,
		// before it runs: that is the check over its limit, not a broken
		// compiler.
		return outcome{Error: "compiling the TypeScript compiler: " + err.Error(), OverMemory: j.MemoryLimit > 0 && strings.Contains(err.Error(), "memory")}
	}
	var stdout, stderr boundedBuffer
	_, err = rt.InstantiateModule(ctx, mod, moduleConfig(j, &stdout, &stderr))
	return outcomeOf(j, err, stdout.String(), stderr.String())
}

// runtimeConfig is the runtime a job's compiler runs in: its memory limit,
// the compilation cache, and closed when the job's context ends.
func runtimeConfig(j job) wazero.RuntimeConfig {
	cfg := wazero.NewRuntimeConfig()
	if j.MemoryLimit > 0 {
		cfg = cfg.WithMemoryLimitPages(uint32(min(j.MemoryLimit/wasmPage, maxWasmPages))) // #nosec G115 -- bounded to the 4 GiB a wasm32 module addresses
	}
	if j.CompileCache != "" {
		if cache, err := wazero.NewCompilationCacheWithDir(j.CompileCache); err == nil {
			cfg = cfg.WithCompilationCache(cache)
		}
	}
	return cfg.WithCloseOnContextDone(true)
}

// moduleConfig is the compiler's command line and file system: the project
// read-only at /, the standard library at /ts/lib, and the state at /state.
func moduleConfig(j job, stdout, stderr *boundedBuffer) wazero.ModuleConfig {
	fsc := wazero.NewFSConfig().WithReadOnlyDirMount(j.Project, "/").WithReadOnlyDirMount(j.Lib, "/ts/lib")
	args := []string{"tsc", "-p", "/" + strings.TrimPrefix(j.Tsconfig, "/"), "--pretty", "false", "--noEmit"}
	if j.State != "" {
		fsc = fsc.WithDirMount(j.State, "/state")
		args = append(args, "--incremental", "--tsBuildInfoFile", "/state/tsbuildinfo")
	}
	return wazero.NewModuleConfig().WithFSConfig(fsc).WithArgs(args...).
		WithStdout(stdout).WithStderr(stderr).WithSysWalltime().WithSysNanotime()
}

// outcomeOf reads how the compiler's run ended.
func outcomeOf(j job, err error, stdout, stderr string) outcome {
	o := outcome{Output: stdout}
	var exit *sys.ExitError
	switch {
	case errors.As(err, &exit):
		o.ExitCode = int(exit.ExitCode())
	case err != nil:
		o.Error = err.Error()
	}
	if strings.Contains(stderr, "out of memory") || strings.Contains(o.Error, "memory") && j.MemoryLimit > 0 {
		o.OverMemory = true
	}
	if o.ExitCode != 0 && o.Output == "" && !o.OverMemory && o.Error == "" {
		o.Error = strings.TrimSpace(stderr)
	}
	return o
}

// boundedBuffer keeps the first maxOutputBytes written to it.
type boundedBuffer struct{ b bytes.Buffer }

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if room := maxOutputBytes - w.b.Len(); room > 0 {
		_, _ = w.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (w *boundedBuffer) String() string { return w.b.String() }
