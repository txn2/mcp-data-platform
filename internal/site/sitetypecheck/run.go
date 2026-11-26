package sitetypecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Defaults for a check.
const (
	// DefaultMemoryLimit is the compiler's limit where the container's memory
	// limit cannot be read, and the most the default ever is.
	DefaultMemoryLimit = 3 << 30
	// MinDefaultMemoryLimit is the least the default is in a small container.
	MinDefaultMemoryLimit = 512 << 20
	// containerShare is the share of the container's memory limit the
	// compiler gets by default (#2828): the worker runs in the replica's
	// container, so its memory counts against the limit beside the server's.
	containerShare   = 4
	DefaultTimeout   = 5 * time.Minute
	maxPackages      = 3000
	fetchConcurrency = 8
	// workerHeadroom is what the worker process may hold beside the
	// compiler's own memory: its Go runtime and the job.
	workerHeadroom = 512 << 20
)

// Statuses a check ends in.
const (
	StatusClean       = "clean"
	StatusErrors      = "errors"
	StatusOverLimit   = "over_limit"
	StatusUnsupported = "unsupported"
	StatusFailed      = "failed"
)

// Diagnostic is one error or warning the compiler reported.
type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Category string `json:"category"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Result is a finished check.
type Result struct {
	Status      string       `json:"status"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Missing are packages the code imports that the lockfile does not lock,
	// whose types were therefore not read.
	Missing []string `json:"missing"`
	// Unverifiable are packages the lockfile names with no integrity hash,
	// which a check does not fetch.
	Unverifiable []string `json:"unverifiable"`
	Packages     int      `json:"packages"`
	Fetched      int      `json:"fetched"`
	Lockfile     string   `json:"lockfile,omitempty"`
	Message      string   `json:"message,omitempty"`
	DurationMS   int64    `json:"duration_ms"`
	// CompileMS is the part of DurationMS spent compiling the TypeScript
	// compiler before it read a file (#2833), or reading it from the
	// compilation cache; zero for a check that never ran the compiler.
	CompileMS int64 `json:"compile_ms,omitempty"`
	// CacheError is why the compilation cache could not be used, for the
	// log; the check compiled without it.
	CacheError string `json:"-"`
}

// Config bounds a check.
type Config struct {
	// Dir is where the compiler and each check's scratch files are kept on
	// the replica's disk.
	Dir string
	// MemoryLimit is the most the compiler may hold, in bytes; zero is
	// DefaultMemoryLimit. The platform passes DefaultMemoryLimitFor its
	// container's limit when the configuration sets none.
	MemoryLimit int64
	// Timeout is the longest a check may run.
	Timeout time.Duration
	// Registry is where packages and the compiler are fetched; empty is the
	// npm registry.
	Registry string
	// CompileCache keeps the compiled compiler under Dir between checks
	// (#2833): about 177 MiB, read in a fraction of a second where compiling
	// takes seconds of a fast core and minutes at a fractional CPU limit.
	CompileCache bool
}

func (c Config) withDefaults() Config {
	if c.Dir == "" {
		c.Dir = filepath.Join(os.TempDir(), "mcp-data-platform-typecheck")
	}
	if c.MemoryLimit <= 0 {
		c.MemoryLimit = DefaultMemoryLimit
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Registry == "" {
		c.Registry = defaultRegistry
	}
	return c
}

// DefaultMemoryLimitFor is the compiler's default limit in a container of
// this memory limit (#2828): a quarter of it, at least MinDefaultMemoryLimit
// and at most DefaultMemoryLimit; DefaultMemoryLimit when the limit is unknown.
func DefaultMemoryLimitFor(container int64) int64 {
	if container <= 0 {
		return DefaultMemoryLimit
	}
	return min(max(container/containerShare, MinDefaultMemoryLimit), DefaultMemoryLimit)
}

// WorkerCeiling is the most a check's worker process holds with this compiler
// limit: the compiler and the worker's own runtime beside it.
func WorkerCeiling(memoryLimit int64) int64 { return memoryLimit + workerHeadroom }

// CeilingWarning is the startup warning for a compiler limit whose worker
// could take more than half the container's memory limit, which the server's
// heap shares (#2828); "" when it cannot or the limit is unknown.
func CeilingWarning(memoryLimit, container int64) string {
	if container <= 0 || WorkerCeiling(memoryLimit) <= container/2 {
		return ""
	}
	return fmt.Sprintf("a type check's worker may hold %d MiB (sites.typecheck.memory_limit %d MiB plus %d MiB for its runtime), "+
		"more than half the container's %d MiB limit, which the server's own heap (GOMEMLIMIT) shares: a check beside a busy "+
		"server can get the container killed; lower sites.typecheck.memory_limit or raise the container's limit",
		WorkerCeiling(memoryLimit)>>20, memoryLimit>>20, workerHeadroom>>20, container>>20)
}

// spawnFunc runs one job, in production a worker process of the platform's
// own binary.
type spawnFunc func(ctx context.Context, j job, memoryLimit int64) (outcome, error)

// Checker runs checks.
type Checker struct {
	cfg       Config
	getter    Getter
	cache     Cache
	compilers *compilerStore
	spawn     spawnFunc
}

// legacyCompileCache is where v1.169.0 kept the compiler's compilation cache,
// removed when a checker is built (#2828).
const legacyCompileCache = "wasm-cache"

// compileCacheDir is where Config.CompileCache keeps the compiled compiler,
// one directory per compiler version (#2833).
const compileCacheDir = "compile-cache"

// NewChecker builds a checker fetching through getter and caching in cache.
func NewChecker(cfg Config, getter Getter, cache Cache) *Checker {
	cfg = cfg.withDefaults()
	_ = os.RemoveAll(filepath.Join(cfg.Dir, legacyCompileCache))
	pruneCompileCache(cfg.Dir, cfg.CompileCache)
	return &Checker{
		cfg: cfg, getter: getter, cache: cache,
		compilers: &compilerStore{getter: getter, cache: cache, dir: cfg.Dir, registry: cfg.Registry},
		spawn:     spawnWorker,
	}
}

// Check type-checks a release's source with its tsconfig, starting from the
// environment's incremental state (nil for none). It returns the result and
// the state to keep for the next check.
func (c *Checker) Check(ctx context.Context, src fs.FS, tsconfig string, state []byte) (res Result, next []byte) {
	started := time.Now()
	res, next = c.checkTree(ctx, src, tsconfig, state)
	res.DurationMS = time.Since(started).Milliseconds()
	if res.Diagnostics == nil {
		res.Diagnostics = []Diagnostic{}
	}
	if res.Missing == nil {
		res.Missing = []string{}
	}
	if res.Unverifiable == nil {
		res.Unverifiable = []string{}
	}
	return res, next
}

// prepare reads what a check runs on: the compiler and the lockfile's
// packages. A release that cannot be checked is answered with its result.
func (c *Checker) prepare(ctx context.Context, src fs.FS, tsconfig string) (*compilerFiles, *Locked, *Result) {
	if msg := unsupported(src, tsconfig); msg != "" {
		return nil, nil, &Result{Status: StatusUnsupported, Message: msg}
	}
	compiler, err := c.compilers.ensure(ctx)
	if err != nil {
		return nil, nil, &Result{Status: StatusFailed, Message: "the TypeScript compiler could not be read: " + err.Error() + diskHint(err)}
	}
	locked, err := readLocked(src, c.cfg.Registry)
	if err != nil {
		return nil, nil, &Result{Status: StatusFailed, Message: err.Error()}
	}
	if len(locked.Packages) > maxPackages {
		return nil, nil, &Result{Status: StatusFailed, Message: fmt.Sprintf("the lockfile locks %d packages, more than the %d a check reads", len(locked.Packages), maxPackages)}
	}
	return compiler, locked, nil
}

func (c *Checker) checkTree(ctx context.Context, src fs.FS, tsconfig string, state []byte) (res Result, next []byte) {
	compiler, locked, refused := c.prepare(ctx, src, tsconfig)
	if refused != nil {
		return *refused, state
	}
	scratch, err := os.MkdirTemp(c.cfg.Dir, "check-")
	if err != nil {
		return Result{Status: StatusFailed, Message: "preparing the check: " + err.Error() + diskHint(err)}, state
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	res = Result{Lockfile: locked.File, Packages: len(locked.Packages), Unverifiable: locked.Unverifiable}
	project, stateDir := filepath.Join(scratch, "project"), filepath.Join(scratch, "state")
	if err := writeSource(src, project); err != nil {
		return Result{Status: StatusFailed, Message: err.Error()}, state
	}
	fetched, err := c.writePackages(ctx, project, locked.Packages)
	res.Fetched = fetched
	if err != nil {
		res.Status, res.Message = StatusFailed, err.Error()
		return res, state
	}
	if err := writeTree(stateDir, map[string][]byte{"tsbuildinfo": state}); err != nil || len(state) == 0 {
		_ = os.MkdirAll(stateDir, dirMode)
		_ = os.Remove(filepath.Join(stateDir, "tsbuildinfo"))
	}
	o, err := c.run(ctx, compiler, project, stateDir, tsconfig)
	if err != nil {
		res.Status, res.Message = StatusFailed, err.Error()
		return res, state
	}
	v := interpret(o, c.cfg.MemoryLimit)
	res.Status, res.Message, res.Diagnostics, res.Missing = v.Status, v.Message, v.Diagnostics, v.Missing
	res.CompileMS, res.CacheError = o.CompileMS, o.CacheError
	next, _ = os.ReadFile(filepath.Join(stateDir, "tsbuildinfo")) // #nosec G304 -- a path inside this check's scratch directory
	if len(next) == 0 {
		next = state
	}
	return res, next
}

// run runs the worker under the check's timeout.
func (c *Checker) run(ctx context.Context, compiler *compilerFiles, project, state, tsconfig string) (outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	o, err := c.spawn(ctx, job{
		Wasm:    compiler.Wasm,
		Project: project, Lib: compiler.Lib, State: state, Tsconfig: tsconfig, MemoryLimit: c.cfg.MemoryLimit,
		CacheDir: c.cacheDir(),
	}, c.cfg.MemoryLimit)
	if ctx.Err() != nil {
		return outcome{}, fmt.Errorf("the check ran longer than %s and was stopped", c.cfg.Timeout)
	}
	return o, err
}

// interpret reads a worker's outcome into a check's status, message,
// diagnostics and missing packages.
func interpret(o outcome, memoryLimit int64) Result {
	diags := parseDiagnostics(o.Output)
	switch {
	case o.OverMemory:
		return Result{Status: StatusOverLimit, Message: fmt.Sprintf("the check needed more than the %d MiB a check may use and was stopped; the site is not affected, and sites.typecheck.memory_limit raises the limit", memoryLimit>>20)}
	case o.Error != "" && len(diags) == 0:
		return Result{Status: StatusFailed, Message: "the compiler did not finish: " + o.Error}
	case o.ExitCode == 0:
		return Result{Status: StatusClean, Diagnostics: diags}
	case len(diags) == 0:
		return Result{Status: StatusFailed, Message: exitedWithout(o)}
	}
	return Result{Status: StatusErrors, Diagnostics: diags, Missing: missingModules(diags)}
}

// maxExitOutputBytes bounds the compiler output a failure with no diagnostic
// carries in its message (#2834), as the crash text is bounded (#2830).
const maxExitOutputBytes = 4 << 10

// exitedWithout is the message of a run that exited non-zero with no
// diagnostic this check reads: the first of what the compiler wrote, so the
// reason is not lost, or that it wrote nothing.
func exitedWithout(o outcome) string {
	out := unmounted(strings.TrimSpace(o.Output))
	if out == "" {
		return fmt.Sprintf("the compiler exited %d and reported nothing", o.ExitCode)
	}
	if len(out) > maxExitOutputBytes {
		out = strings.ToValidUTF8(out[:maxExitOutputBytes], "") + "\n..."
	}
	return fmt.Sprintf("the compiler exited %d with no diagnostic this check reads; it wrote:\n%s", o.ExitCode, out)
}

// diagnosticLine is one line tsc writes with --pretty false.
var diagnosticLine = regexp.MustCompile(`^(.+)\((\d+),(\d+)\): (error|warning|message) (TS\d+): (.*)$`)

// globalDiagnosticLine is a diagnostic with no file location (#2834): a
// configuration or global error, such as TS18003 for an include that matches
// no file, TS6053 for a missing extends target or TS5110 for an option the
// compiler refuses.
var globalDiagnosticLine = regexp.MustCompile(`^(error|warning|message) (TS\d+): (.*)$`)

// maxDiagnostics bounds the errors a check keeps.
const maxDiagnostics = 1000

// parseDiagnostics reads the compiler's output.
func parseDiagnostics(out string) []Diagnostic {
	var diags []Diagnostic
	for line := range strings.SplitSeq(out, "\n") {
		d, ok := parseDiagnostic(strings.TrimRight(line, "\r"))
		if !ok {
			continue
		}
		diags = append(diags, d)
		if len(diags) == maxDiagnostics {
			break
		}
	}
	return diags
}

// parseDiagnostic reads one line of the compiler's output: a diagnostic at a
// place in a file, or one with no location, whose file, line and column are
// left empty.
func parseDiagnostic(line string) (Diagnostic, bool) {
	if m := diagnosticLine.FindStringSubmatch(line); len(m) > atMessage {
		ln, _ := strconv.Atoi(m[atLine])
		col, _ := strconv.Atoi(m[atColumn])
		return Diagnostic{
			File: strings.TrimPrefix(strings.TrimPrefix(m[atFile], projectMount+"/"), "/"), Line: ln, Column: col,
			Category: m[atCategory], Code: m[atCode], Message: unmounted(m[atMessage]),
		}, true
	}
	if m := globalDiagnosticLine.FindStringSubmatch(line); len(m) > globalMessage {
		return Diagnostic{Category: m[globalCategory], Code: m[globalCode], Message: unmounted(m[globalMessage])}, true
	}
	return Diagnostic{}, false
}

// The submatches of diagnosticLine, and of globalDiagnosticLine.
const (
	atFile = iota + 1
	atLine
	atColumn
	atCategory
	atCode
	atMessage
)

const (
	globalCategory = iota + 1
	globalCode
	globalMessage
)

// unmounted is compiler text with the project's mount point taken off the
// paths it names, so they read as paths in the release.
func unmounted(s string) string { return strings.ReplaceAll(s, projectMount+"/", "") }

// cannotFindModule names the module a TS2307 says is missing.
var cannotFindModule = regexp.MustCompile(`Cannot find module '([^']+)'`)

// missingModules are the bare packages the code imports whose types were not
// found: imported, and not in the lockfile.
func missingModules(diags []Diagnostic) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range diags {
		if d.Code != "TS2307" {
			continue
		}
		m := cannotFindModule.FindStringSubmatch(d.Message)
		if len(m) < 2 || strings.HasPrefix(m[1], ".") || strings.HasPrefix(m[1], slash) || strings.HasPrefix(m[1], "@/") {
			continue
		}
		name := packageOf(m[1])
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// packageOf is the package a bare specifier names: a or @s/a.
func packageOf(spec string) string {
	parts := strings.Split(spec, slash)
	if strings.HasPrefix(spec, "@") && len(parts) > 1 {
		return parts[0] + slash + parts[1]
	}
	return parts[0]
}

// writeSource writes the release's files under dir, leaving out any
// node_modules it carries.
func writeSource(src fs.FS, dir string) error {
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		return writeTree(dir, map[string][]byte{p: data})
	})
	if err != nil {
		return fmt.Errorf("writing the release for the check: %w", err)
	}
	return nil
}

// writePackages writes each package's kept files under its node_modules
// directory, fetching in parallel; it reports how many were fetched rather
// than read from the cache.
func (c *Checker) writePackages(ctx context.Context, project string, pkgs []Tarball) (int, error) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fetchConcurrency)
	var (
		mu      sync.Mutex
		fetched int
	)
	for _, t := range pkgs {
		g.Go(func() error {
			files, wasFetched, err := fetchTypes(gctx, c.getter, c.cache, t)
			if err != nil {
				return fmt.Errorf("the types of %s@%s could not be read: %w", t.Name, t.Version, err)
			}
			placed := make(map[string][]byte, len(files))
			for name, data := range files {
				placed[path.Join(t.Dir, name)] = data
			}
			mu.Lock()
			defer mu.Unlock()
			if wasFetched {
				fetched++
			}
			return writeTree(project, placed)
		})
	}
	err := g.Wait()
	return fetched, err //nolint:wrapcheck // each goroutine names its package
}

// unsupported is why a release cannot be checked as it is: its tsconfig is
// missing, or sets baseUrl, which TypeScript 7 no longer reads.
func unsupported(src fs.FS, tsconfig string) string {
	data, err := fs.ReadFile(src, tsconfig)
	if err != nil {
		return fmt.Sprintf("_build.json names %s, which this release does not have", tsconfig)
	}
	var doc struct {
		CompilerOptions map[string]json.RawMessage `json:"compilerOptions"`
	}
	if json.Unmarshal(stripJSONC(data), &doc) != nil {
		return ""
	}
	if _, ok := doc.CompilerOptions["baseUrl"]; ok {
		return fmt.Sprintf("%s sets baseUrl, which TypeScript 7 no longer reads; replace it with paths entries that start with ./", tsconfig)
	}
	return ""
}

// trailingComma is a comma before a closing brace or bracket.
var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// stripJSONC removes the comments and trailing commas a tsconfig may carry.
func stripJSONC(data []byte) []byte {
	var out bytes.Buffer
	var str jsonString
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if str.open || ch == '"' {
			_ = out.WriteByte(ch)
			str.step(ch)
			continue
		}
		if next, newline, isComment := skipComment(data, i); isComment {
			if next < 0 {
				return out.Bytes()
			}
			if newline {
				_ = out.WriteByte('\n')
			}
			i = next
			continue
		}
		_ = out.WriteByte(ch)
	}
	return trailingComma.ReplaceAll(out.Bytes(), []byte("$1"))
}

// jsonString tracks whether a scan is inside a JSON string.
type jsonString struct{ open, escaped bool }

// step reads one byte of, or opening, a string.
func (s *jsonString) step(ch byte) {
	switch {
	case !s.open:
		s.open = true
	case s.escaped:
		s.escaped = false
	case ch == '\\':
		s.escaped = true
	case ch == '"':
		s.open = false
	}
}

// skipComment reads a comment starting at i: the index of its last byte
// (-1 for a block comment never closed), and whether a newline stands in for
// it. isComment is false when no comment starts at i.
func skipComment(data []byte, i int) (next int, newline, isComment bool) {
	if data[i] != '/' || i+1 >= len(data) {
		return 0, false, false
	}
	switch data[i+1] {
	case '/':
		for i < len(data) && data[i] != '\n' {
			i++
		}
		return i, true, true
	case '*':
		end := bytes.Index(data[i+len("/*"):], []byte("*/"))
		if end < 0 {
			return -1, false, true
		}
		return i + len("/*") + end + len("*/") - 1, false, true
	}
	return 0, false, false
}

// spawnWorker runs one job in a worker process of the platform's own binary,
// so the compiler's memory is outside the replica's. The process's Go runtime
// is limited to the compiler's limit and its headroom.
func spawnWorker(ctx context.Context, j job, memoryLimit int64) (outcome, error) {
	exe, err := os.Executable()
	if err != nil {
		return outcome{}, fmt.Errorf("finding the platform binary: %w", err)
	}
	in, err := json.Marshal(j)
	if err != nil {
		return outcome{}, fmt.Errorf("encoding the job: %w", err)
	}
	cmd := exec.CommandContext(ctx, exe, WorkerCommand) // #nosec G204 -- the platform's own binary, a fixed argument
	cmd.Stdin = bytes.NewReader(in)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Env = append(os.Environ(), "GOMEMLIMIT="+strconv.FormatInt(WorkerCeiling(memoryLimit), 10))
	runErr := cmd.Run()
	var o outcome
	if err := json.Unmarshal(stdout.Bytes(), &o); err != nil {
		if runErr != nil {
			return outcome{OverMemory: strings.Contains(runErr.Error(), "killed")}, fmt.Errorf("the check's worker process ended: %w", runErr)
		}
		return outcome{}, fmt.Errorf("the check's worker process answered nothing it could read: %w", err)
	}
	return o, nil
}

// readFile reads a file the parent named in a job.
func readFile(name string) ([]byte, error) {
	data, err := os.ReadFile(name) // #nosec G304 -- a path the parent laid out for this job
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return data, nil
}

// diskHint is what a check that could not write its files tells an operator:
// the setting that names a writable directory for them.
func diskHint(err error) string {
	if _, isPath := errors.AsType[*fs.PathError](err); !isPath {
		return ""
	}
	return "; set sites.typecheck.dir to a writable directory (a deployment with no /tmp mounts a volume there)"
}
