package sitetypecheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tgz is a gzip tarball of files under package/.
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "package/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func sri(b []byte) string {
	sum := sha512.Sum512(b)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// fakeGetter serves URLs from a map and counts reads.
type fakeGetter struct {
	bodies map[string][]byte
	reads  int
}

func (f *fakeGetter) Get(_ context.Context, url string, _ int64) ([]byte, error) {
	f.reads++
	b, ok := f.bodies[url]
	if !ok {
		return nil, errors.New("404 " + url)
	}
	return b, nil
}

// memObjects is an in-memory bucket.
type memObjects map[string][]byte

func (m memObjects) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	m[key] = data
	return nil
}

func (m memObjects) GetObject(_ context.Context, _, key string) (data []byte, contentType string, err error) {
	b, ok := m[key]
	if !ok {
		return nil, "", errors.New("not found")
	}
	return b, "", nil
}

func TestReadLocked_EveryLockfile(t *testing.T) {
	npm := `{"packages":{"":{"name":"site"},"node_modules/react":{"version":"18.3.1","resolved":"https://registry.npmjs.org/react/-/react-18.3.1.tgz","integrity":"sha512-a"},
	"node_modules/@types/react":{"version":"18.3.3","resolved":"https://registry.npmjs.org/@types/react/-/react-18.3.3.tgz","integrity":"sha512-b"},
	"node_modules/a/node_modules/b":{"version":"1.0.0","resolved":"https://r/b.tgz","integrity":"sha512-c"},
	"node_modules/linked":{"link":true},"node_modules/nohash":{"version":"1.0.0","resolved":"https://r/x.tgz"}}}`
	got, err := readLocked(fstest.MapFS{"package-lock.json": {Data: []byte(npm)}}, defaultRegistry)
	if err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 0, len(got.Packages))
	for _, p := range got.Packages {
		dirs = append(dirs, p.Dir)
	}
	if strings.Join(dirs, ",") != "node_modules/@types/react,node_modules/a/node_modules/b,node_modules/react" {
		t.Errorf("npm dirs = %v", dirs)
	}
	if len(got.Unverifiable) != 1 || got.Unverifiable[0] != "nohash@1.0.0" {
		t.Errorf("unverifiable = %v", got.Unverifiable)
	}

	pnpm := "lockfileVersion: '9.0'\npackages:\n  react@18.3.1:\n    resolution: {integrity: sha512-a}\n  react@9.0.0:\n    resolution: {integrity: sha512-z}\n  '@types/react@18.3.3':\n    resolution: {integrity: sha512-b}\n"
	got, err = readLocked(fstest.MapFS{"pnpm-lock.yaml": {Data: []byte(pnpm)}}, defaultRegistry)
	if err != nil || len(got.Packages) != 2 {
		t.Fatalf("pnpm = %+v %v", got, err)
	}
	for _, p := range got.Packages {
		if p.Name == "react" && (p.Version != "18.3.1" || p.URL != "https://registry.npmjs.org/react/-/react-18.3.1.tgz") {
			t.Errorf("pnpm react = %+v (semver order and the registry tarball)", p)
		}
		if p.Name == "@types/react" && p.URL != "https://registry.npmjs.org/@types/react/-/react-18.3.3.tgz" {
			t.Errorf("scoped tarball = %s", p.URL)
		}
	}

	yarn := "# yarn lockfile v1\n\n\"react@^18.3.1\":\n  version \"18.3.1\"\n  resolved \"https://registry.yarnpkg.com/react/-/react-18.3.1.tgz#abc\"\n  integrity sha512-a\n"
	got, err = readLocked(fstest.MapFS{"yarn.lock": {Data: []byte(yarn)}}, defaultRegistry)
	if err != nil || len(got.Packages) != 1 || got.Packages[0].URL != "https://registry.yarnpkg.com/react/-/react-18.3.1.tgz" {
		t.Fatalf("yarn = %+v %v", got, err)
	}
	if _, err := readLocked(fstest.MapFS{"yarn.lock": {Data: []byte("__metadata:\n  version: 6\n")}}, defaultRegistry); err == nil {
		t.Error("a Yarn 2+ lockfile was read")
	}
	if got, _ := readLocked(fstest.MapFS{}, defaultRegistry); got.File != "" {
		t.Error("no lockfile named one")
	}
}

func TestVerifyAndKeep(t *testing.T) {
	body := tgz(t, map[string]string{"package.json": `{"name":"x"}`, "index.d.ts": "export {}", "index.js": "x", "sub/a.d.mts": "a", "../evil.d.ts": "no"})
	if err := verifyIntegrity(body, sri(body)); err != nil {
		t.Fatal(err)
	}
	if err := verifyIntegrity(body, sri([]byte("other"))); !errors.Is(err, ErrIntegrity) {
		t.Errorf("a mismatch passed: %v", err)
	}
	if err := verifyIntegrity(body, "md5-x"); !errors.Is(err, ErrIntegrity) {
		t.Errorf("an unusable hash passed: %v", err)
	}
	// A SHA-1 hash holds nothing to what was locked: it is not checked, and a
	// package locked with it alone is reported, never fetched.
	sha1Only := "sha1-" + base64.StdEncoding.EncodeToString(make([]byte, 20))
	if err := verifyIntegrity(body, sha1Only); !errors.Is(err, ErrIntegrity) {
		t.Errorf("a SHA-1 hash was checked: %v", err)
	}
	var l Locked
	l.add(Tarball{Name: "old", Version: "1.0.0", URL: "https://registry.npmjs.org/old/-/old-1.0.0.tgz", Integrity: sha1Only})
	l.add(Tarball{Name: "new", Version: "1.0.0", URL: "https://registry.npmjs.org/new/-/new-1.0.0.tgz", Integrity: sri(body) + " " + sha1Only})
	if len(l.Packages) != 1 || l.Packages[0].Name != "new" || len(l.Unverifiable) != 1 || l.Unverifiable[0] != "old@1.0.0" {
		t.Errorf("packages %v unverifiable %v", l.Packages, l.Unverifiable)
	}
	files, err := typesOf(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files["index.js"] != nil || files["sub/a.d.mts"] == nil {
		t.Errorf("kept = %v", keys(files))
	}
	for k := range files {
		if strings.Contains(k, "..") {
			t.Errorf("kept a path leaving the package: %s", k)
		}
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFetchTypes_CachesByIntegrity(t *testing.T) {
	body := tgz(t, map[string]string{"package.json": "{}", "index.d.ts": "export {}"})
	g := &fakeGetter{bodies: map[string][]byte{"https://r/x.tgz": body}}
	c := Cache{Objects: memObjects{}, Bucket: "b", Prefix: "sites/_typecheck"}
	pkg := Tarball{Name: "x", Version: "1.0.0", URL: "https://r/x.tgz", Integrity: sri(body)}
	if _, fetched, err := fetchTypes(context.Background(), g, c, pkg); err != nil || !fetched {
		t.Fatalf("first fetch: %v %v", fetched, err)
	}
	if _, fetched, err := fetchTypes(context.Background(), g, c, pkg); err != nil || fetched || g.reads != 1 {
		t.Fatalf("a second check fetched again: %v %v reads=%d", fetched, err, g.reads)
	}
	bad := pkg
	bad.Integrity = sri([]byte("tampered"))
	if _, _, err := fetchTypes(context.Background(), g, c, bad); !errors.Is(err, ErrIntegrity) {
		t.Errorf("a tampered tarball was used: %v", err)
	}
}

func TestParseAndInterpret(t *testing.T) {
	out := "src/a.ts(1,14): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"/src/b.ts(3,1): error TS2307: Cannot find module 'date-fns-tz' or its corresponding type declarations.\n" +
		"src/c.ts(2,1): error TS2307: Cannot find module '@acme/ui/button'.\n" +
		"src/d.ts(2,1): error TS2307: Cannot find module './local'.\nnoise\n"
	v := interpret(outcome{ExitCode: 2, Output: out}, DefaultMemoryLimit)
	status, diags, missing := v.Status, v.Diagnostics, v.Missing
	if status != StatusErrors || len(diags) != 4 || diags[1].File != "src/b.ts" || diags[0].Code != "TS2322" || diags[0].Column != 14 {
		t.Fatalf("status %s diags %+v", status, diags)
	}
	if strings.Join(missing, ",") != "@acme/ui,date-fns-tz" {
		t.Errorf("missing = %v", missing)
	}
	for o, want := range map[outcome]string{
		{ExitCode: 0}:               StatusClean,
		{OverMemory: true}:          StatusOverLimit,
		{Error: "trap"}:             StatusFailed,
		{ExitCode: 1, Output: "no"}: StatusFailed,
	} {
		if got := interpret(o, DefaultMemoryLimit).Status; got != want {
			t.Errorf("%+v -> %s, want %s", o, got, want)
		}
	}
}

// TestInterpret_DiagnosticsWithNoLocation: a configuration diagnostic carries
// no file (#2834); it is a diagnostic like any other, and output with no
// diagnostic at all is quoted in the failure rather than lost.
func TestInterpret_DiagnosticsWithNoLocation(t *testing.T) {
	out := "error TS18003: No inputs were found in config file '/project/tsconfig.json'. Specified 'include' paths were '[\"nothing-here/**/*.ts\"]' and 'exclude' paths were '[]'.\n" +
		"/project/src/a.ts(2,3): error TS2322: Type 'string' is not assignable to type 'number'.\n"
	v := interpret(outcome{ExitCode: 2, Output: out}, DefaultMemoryLimit)
	require.Equal(t, StatusErrors, v.Status, v.Message)
	require.Len(t, v.Diagnostics, 2)
	assert.Equal(t, Diagnostic{
		Category: "error", Code: "TS18003",
		Message: `No inputs were found in config file 'tsconfig.json'. Specified 'include' paths were '["nothing-here/**/*.ts"]' and 'exclude' paths were '[]'.`,
	}, v.Diagnostics[0])
	assert.Equal(t, "src/a.ts", v.Diagnostics[1].File)

	v = interpret(outcome{ExitCode: 3, Output: "Version 7.1.0\nsomething /project/x went wrong\n"}, DefaultMemoryLimit)
	assert.Equal(t, StatusFailed, v.Status)
	assert.Equal(t, "the compiler exited 3 with no diagnostic this check reads; it wrote:\nVersion 7.1.0\nsomething x went wrong", v.Message)

	v = interpret(outcome{ExitCode: 3, Output: strings.Repeat("é", maxExitOutputBytes)}, DefaultMemoryLimit)
	assert.True(t, strings.HasSuffix(v.Message, "\n..."), "a long output is cut")
	assert.Less(t, len(v.Message), maxExitOutputBytes+200)
	assert.True(t, utf8.ValidString(v.Message))

	v = interpret(outcome{ExitCode: 3, Output: " \n"}, DefaultMemoryLimit)
	assert.Equal(t, "the compiler exited 3 and reported nothing", v.Message)
}

func TestUnsupportedAndJSONC(t *testing.T) {
	src := fstest.MapFS{
		"tsconfig.json":     {Data: []byte("{\n // a comment\n \"compilerOptions\": {\"baseUrl\": \".\", /* x */ \"strict\": true,},\n}")},
		"tsconfig.ok.json":  {Data: []byte(`{"compilerOptions":{"paths":{"@/*":["./src/*"]}}}`)},
		"tsconfig.url.json": {Data: []byte(`{"compilerOptions":{"strict":true,"x":"http://a//b"}}`)},
	}
	if msg := unsupported(src, "tsconfig.json"); !strings.Contains(msg, "baseUrl") {
		t.Errorf("baseUrl not caught: %q", msg)
	}
	if msg := unsupported(src, "tsconfig.ok.json"); msg != "" {
		t.Errorf("a supported tsconfig refused: %q", msg)
	}
	if msg := unsupported(src, "tsconfig.url.json"); msg != "" {
		t.Errorf("a // inside a string read as a comment: %q", msg)
	}
	if msg := unsupported(src, "tsconfig.app.json"); !strings.Contains(msg, "does not have") {
		t.Errorf("a missing tsconfig: %q", msg)
	}
}

// TestCheck_WithAFakeWorker drives a whole check without the compiler: the
// lockfile's packages are fetched, verified and laid out where the release's
// node_modules would be, and the worker's outcome is read.
func TestCheck_WithAFakeWorker(t *testing.T) {
	dir := t.TempDir()
	compilerTgz := tgz(t, map[string]string{"lib/tsc.wasm": "\x00asm", "lib/lib.d.ts": "", "README.md": ""})
	typesTgz := tgz(t, map[string]string{"package.json": "{}", "index.d.ts": "export declare const x: number;"})
	g := &fakeGetter{bodies: map[string][]byte{
		registryTarball(defaultRegistry, CompilerPackage, CompilerVersion): compilerTgz,
		"https://registry.npmjs.org/x/-/x-1.0.0.tgz":                       typesTgz,
	}}
	c := NewChecker(Config{Dir: dir}, g, Cache{})
	c.compilers.cache = Cache{Objects: memObjects{}, Bucket: "b"}
	// The fake registry's compiler tarball is not the pinned one, so put it in
	// the cache the store reads first, as an earlier fetch would have.
	kept, err := compilerFrom(compilerTgz)
	if err != nil {
		t.Fatal(err)
	}
	c.compilers.cache.put(context.Background(), "compiler", compilerIntegrity, kept)
	var seen job
	c.spawn = func(_ context.Context, j job, _ int64) (outcome, error) {
		seen = j
		if _, err := os.Stat(j.Project + "/node_modules/x/index.d.ts"); err != nil {
			t.Errorf("the package's types were not laid out: %v", err)
		}
		_ = os.WriteFile(j.State+"/tsbuildinfo", []byte("next"), 0o600)
		return outcome{ExitCode: 2, Output: "src/a.ts(1,1): error TS2322: bad\n", CompileMS: 42, CacheError: "disk full"}, nil
	}
	lock := `{"packages":{"node_modules/x":{"version":"1.0.0","resolved":"https://registry.npmjs.org/x/-/x-1.0.0.tgz","integrity":"` + sri(typesTgz) + `"}}}`
	src := fstest.MapFS{
		"tsconfig.json": {Data: []byte(`{}`)}, "package-lock.json": {Data: []byte(lock)}, "src/a.ts": {Data: []byte("x")},
		"node_modules/ignored/index.d.ts": {Data: []byte("x")},
	}
	res, state := c.Check(context.Background(), src, "tsconfig.json", []byte("prev"))
	if res.Status != StatusErrors || len(res.Diagnostics) != 1 || res.Packages != 1 || res.Fetched != 1 || string(state) != "next" {
		t.Fatalf("res = %+v state %q", res, state)
	}
	if seen.Tsconfig != "tsconfig.json" || seen.MemoryLimit != DefaultMemoryLimit || seen.CacheDir != "" {
		t.Errorf("job = %+v", seen)
	}
	if res.CompileMS != 42 || res.CacheError != "disk full" {
		t.Errorf("the worker's compile time and cache error did not reach the result: %+v", res)
	}
	if raw, _ := json.Marshal(res); strings.Contains(string(raw), "disk full") {
		t.Errorf("the cache error is for the log, not the stored result: %s", raw)
	}
	res, _ = c.Check(context.Background(), fstest.MapFS{"tsconfig.json": {Data: []byte(`{"compilerOptions":{"baseUrl":"."}}`)}}, "tsconfig.json", nil)
	if res.Status != StatusUnsupported || res.Diagnostics == nil || res.Missing == nil {
		t.Errorf("unsupported = %+v", res)
	}
}

// TestCheck_RealCompiler runs the pinned compiler in process when
// TSC_WASM_TGZ names its downloaded tarball; skipped otherwise.
func TestCheck_RealCompiler(t *testing.T) {
	path := os.Getenv("TSC_WASM_TGZ")
	if path == "" {
		t.Skip("TSC_WASM_TGZ names no compiler tarball")
	}
	body, err := os.ReadFile(path) // #nosec G304 G703 -- the compiler tarball a developer names in TSC_WASM_TGZ
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGetter{bodies: map[string][]byte{registryTarball(defaultRegistry, CompilerPackage, CompilerVersion): body}}
	c := NewChecker(Config{Dir: t.TempDir()}, g, Cache{})
	c.spawn = func(ctx context.Context, j job, _ int64) (outcome, error) { return runJob(ctx, j), nil }
	src := fstest.MapFS{
		"tsconfig.json": {Data: []byte(`{"compilerOptions":{"strict":true,"noUnusedLocals":true,"target":"es2022","module":"esnext","moduleResolution":"bundler"},"include":["src"]}`)},
		"src/a.ts":      {Data: []byte("export const v: number = \"seven\";\nconst unused = 1;\n")},
	}
	res, state := c.Check(context.Background(), src, "tsconfig.json", nil)
	if res.Status != StatusErrors || len(res.Diagnostics) != 2 || res.Diagnostics[0].Code != "TS2322" || res.Diagnostics[0].Line != 1 || res.Diagnostics[0].Column != 14 {
		t.Fatalf("res = %+v", res)
	}
	if len(state) == 0 {
		t.Error("no incremental state was kept")
	}
	src["src/a.ts"] = &fstest.MapFile{Data: []byte("export const v: number = 7;\n")}
	if res, _ := c.Check(context.Background(), src, "tsconfig.json", state); res.Status != StatusClean {
		t.Errorf("fixed source = %+v", res)
	}

	// #2834: an include that matches no file is reported as TS18003, a
	// diagnostic with no file location.
	none := fstest.MapFS{"tsconfig.json": {Data: []byte(`{"compilerOptions":{"strict":true,"noEmit":true,"lib":["ES2022","DOM"]},"include":["nothing-here/**/*.ts"]}`)}}
	res, _ = c.Check(context.Background(), none, "tsconfig.json", nil)
	if res.Status != StatusErrors || len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != "TS18003" || res.Diagnostics[0].File != "" {
		t.Fatalf("empty include = %+v", res)
	}

	// #2833: with the compilation cache kept, the second check reads the
	// compiled compiler rather than compiling it again.
	cached := NewChecker(Config{Dir: t.TempDir(), CompileCache: true}, g, Cache{})
	cached.spawn = c.spawn
	cold, _ := cached.Check(context.Background(), none, "tsconfig.json", nil)
	warm, _ := cached.Check(context.Background(), none, "tsconfig.json", nil)
	t.Logf("compile_ms cold %d warm %d", cold.CompileMS, warm.CompileMS)
	if cold.CompileMS <= 0 || warm.CompileMS*4 > cold.CompileMS || warm.Diagnostics[0].Code != "TS18003" {
		t.Errorf("cold %+v warm %+v", cold, warm)
	}
}
