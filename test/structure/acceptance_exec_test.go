package structure_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// An acceptance criterion that runs a binary passes where it was written and
// fails on the CI runner when the runner does not have that binary, which
// nothing reported before the pull request's acceptance job ran: #2397 lost a
// cycle on ffmpeg (#2398). Every binary test/acceptance runs is listed, with
// where the CI job gets it, in .github/acceptance-binaries.tsv.

const acceptanceBinariesFile = ".github/acceptance-binaries.tsv"

// binarySource is where the acceptance job gets one binary.
type binarySource struct {
	step       string // a step of the acceptance job that installs it
	darwinOnly bool   // only a darwin machine has it
}

func readAcceptanceBinaries(t *testing.T, raw string) map[string]binarySource {
	t.Helper()
	out := map[string]binarySource{}
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			t.Fatalf("%s:%d: want <binary>\\t<source>, got %q", acceptanceBinariesFile, i+1, line)
		}
		var src binarySource
		switch s := fields[1]; {
		case s == "runner-image":
		case s == "darwin-only":
			src.darwinOnly = true
		case strings.HasPrefix(s, "step:"):
			src.step = strings.TrimPrefix(s, "step:")
		default:
			t.Fatalf("%s:%d: unknown source %q (runner-image, step:<name>, darwin-only)", acceptanceBinariesFile, i+1, s)
		}
		out[fields[0]] = src
	}
	return out
}

// execCall is one exec.Command or exec.CommandContext in the suite.
type execCall struct {
	pos       token.Position
	binary    string // the binary, when the first argument names one
	built     bool   // the platform binary the suite builds with go build
	underGOOS bool   // the enclosing function branches on runtime.GOOS
}

// acceptanceExecs reads every exec.Command(Context) call in the files given.
func acceptanceExecs(t *testing.T, files map[string]string) []execCall {
	t.Helper()
	fset := token.NewFileSet()
	parsed := make([]*ast.File, 0, len(files))
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, files[name], 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed = append(parsed, f)
	}
	builders := goBuildFuncs(parsed)
	consts := stringConsts(parsed)
	var calls []execCall
	for _, f := range parsed {
		fileBuilds := runsGoBuild(f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			goos := branchesOnGOOS(fn.Body)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				arg, ok := execBinaryArg(call)
				if !ok {
					return true
				}
				c := execCall{pos: fset.Position(call.Pos()), underGOOS: goos}
				c.binary, c.built = resolveBinary(arg, fn.Body, consts, builders)
				if c.binary == "" && !c.built && fileBuilds {
					// A binary handed in by a caller in a file that builds
					// the platform: the suite's own build.
					c.built = true
				}
				calls = append(calls, c)
				return true
			})
		}
	}
	return calls
}

// execBinaryArg is the binary argument of exec.Command(name, ...) or
// exec.CommandContext(ctx, name, ...).
func execBinaryArg(call *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
		return nil, false
	}
	switch {
	case sel.Sel.Name == "Command" && len(call.Args) > 0:
		return call.Args[0], true
	case sel.Sel.Name == "CommandContext" && len(call.Args) > 1:
		return call.Args[1], true
	}
	return nil, false
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// isGoBuild reports whether call is exec.Command(Context)("go", "build", ...).
func isGoBuild(call *ast.CallExpr) bool {
	arg, ok := execBinaryArg(call)
	if !ok {
		return false
	}
	if s, _ := stringLit(arg); s != "go" {
		return false
	}
	for _, a := range call.Args {
		if s, _ := stringLit(a); s == "build" {
			return true
		}
	}
	return false
}

func runsGoBuild(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && isGoBuild(call) {
			found = true
		}
		return !found
	})
	return found
}

// goBuildFuncs names the package functions, and the package variables holding
// a function (sync.OnceValues), that build the platform.
func goBuildFuncs(files []*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body != nil && d.Recv == nil && runsGoBuild(d.Body) {
					out[d.Name.Name] = true
				}
			case *ast.GenDecl:
				if d.Tok == token.VAR {
					markBuildingVars(d, out)
				}
			}
		}
	}
	return out
}

func markBuildingVars(d *ast.GenDecl, out map[string]bool) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if i < len(vs.Values) && runsGoBuild(vs.Values[i]) {
				out[name.Name] = true
			}
		}
	}
}

func stringConsts(files []*ast.File) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if s, ok := stringLit(vs.Values[i]); ok {
							out[name.Name] = s
						}
					}
				}
			}
		}
	}
	return out
}

// resolveBinary names the binary an argument holds: a literal, a string
// constant, the result of exec.LookPath("<name>"), or the platform a package
// function builds. It returns "" and false when it cannot tell.
func resolveBinary(arg ast.Expr, body *ast.BlockStmt, consts map[string]string, builders map[string]bool) (string, bool) {
	if s, ok := stringLit(arg); ok {
		return s, false
	}
	switch e := arg.(type) {
	case *ast.CallExpr:
		return resolveCall(e, builders)
	case *ast.Ident:
		if s, ok := consts[e.Name]; ok {
			return s, false
		}
		var name string
		var built bool
		ast.Inspect(body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Rhs) == 0 {
				return true
			}
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == e.Name {
					if call, ok := as.Rhs[0].(*ast.CallExpr); ok {
						if n, b := resolveCall(call, builders); n != "" || b {
							name, built = n, b
						}
					}
				}
			}
			return true
		})
		return name, built
	}
	return "", false
}

func resolveCall(call *ast.CallExpr, builders map[string]bool) (string, bool) {
	if id, ok := call.Fun.(*ast.Ident); ok && builders[id.Name] {
		return "", true
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "LookPath" && len(call.Args) == 1 {
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "exec" {
			s, _ := stringLit(call.Args[0])
			return s, false
		}
	}
	return "", false
}

func branchesOnGOOS(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "GOOS" {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "runtime" {
				found = true
			}
		}
		return !found
	})
	return found
}

// execViolations is what the structure test reports: a binary the CI job is not
// listed as providing, a darwin-only binary run outside a runtime.GOOS branch,
// and a binary argument the check cannot name.
func execViolations(calls []execCall, provided map[string]binarySource) []string {
	var out []string
	for _, c := range calls {
		at := fmt.Sprintf("%s:%d", c.pos.Filename, c.pos.Line)
		switch {
		case c.built:
		case c.binary == "":
			out = append(out, at+": exec runs a binary this check cannot name; run a literal binary listed in "+acceptanceBinariesFile)
		default:
			src, ok := provided[c.binary]
			switch {
			case !ok:
				out = append(out, fmt.Sprintf("%s: runs %q, which the acceptance CI job does not provide; install it in .github/workflows/acceptance-run.yml and list it in %s, or draw the fixture another way", at, c.binary, acceptanceBinariesFile))
			case src.darwinOnly && !c.underGOOS:
				out = append(out, fmt.Sprintf("%s: runs %q, which only darwin has, outside a runtime.GOOS check that skips on the other platforms", at, c.binary))
			}
		}
	}
	return out
}

func acceptanceSources(t *testing.T) map[string]string {
	t.Helper()
	dir := rootPath(t, "test", "acceptance")
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p) // #nosec G304 -- the repository's own test files
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Join("test", "acceptance", filepath.Base(p))] = string(raw)
	}
	return out
}

// TestAcceptanceExecsOnlyWhatCIProvides is #2398's gate over the suite as it
// stands: every binary it runs is listed, every "step:" source names a step of
// the acceptance job, and no listed binary has stopped being run.
func TestAcceptanceExecsOnlyWhatCIProvides(t *testing.T) {
	listRaw, err := os.ReadFile(rootPath(t, ".github", "acceptance-binaries.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	provided := readAcceptanceBinaries(t, string(listRaw))
	job, err := os.ReadFile(rootPath(t, ".github", "workflows", "acceptance-run.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for bin, src := range provided {
		if src.step != "" && !strings.Contains(string(job), "- name: "+src.step+"\n") {
			t.Errorf("%s lists %q from step %q, which acceptance-run.yml does not have", acceptanceBinariesFile, bin, src.step)
		}
	}
	calls := acceptanceExecs(t, acceptanceSources(t))
	for _, v := range execViolations(calls, provided) {
		t.Error(v)
	}
	used := map[string]bool{}
	for _, c := range calls {
		used[c.binary] = true
	}
	for bin := range provided {
		if !used[bin] {
			t.Errorf("%s lists %q, which no test under test/acceptance runs any longer; remove it", acceptanceBinariesFile, bin)
		}
	}
}

// TestAcceptanceExecsRefusesAnUnprovidedBinary is #2398's first criterion: a
// criterion that draws a fixture with ffmpeg fails, naming the file, the line
// and the binary; a darwin-only binary outside a runtime.GOOS branch fails, and
// the same call inside one, a LookPath result and the suite's own build pass.
func TestAcceptanceExecsRefusesAnUnprovidedBinary(t *testing.T) {
	provided := readAcceptanceBinaries(t, "go\tstep:Set up Go\nsemgrep\tstep:Install semgrep\nhdiutil\tdarwin-only\n")
	src := `package acceptance

import (
	"os/exec"
	"runtime"
)

func clip() {
	_ = exec.Command("ffmpeg", "-f", "lavfi", "-i", "testsrc", "clip.mp4").Run()
}

func ramDisk() {
	_ = exec.Command("hdiutil", "attach").Run()
}

func ramDiskGuarded() {
	if runtime.GOOS != "darwin" {
		return
	}
	_ = exec.Command("hdiutil", "attach").Run()
}

func scan() {
	semgrep, _ := exec.LookPath("semgrep")
	_ = exec.Command(semgrep, "scan").Run()
}

func build() string {
	_ = exec.Command("go", "build", "-o", "bin", "./cmd/mcp-data-platform").Run()
	return "bin"
}

func start() {
	_ = exec.Command(build(), "--config", "x").Run()
}

`
	other := `package acceptance

import "os/exec"

func unknown(name string) {
	_ = exec.Command(name).Run()
}
`
	got := execViolations(acceptanceExecs(t, map[string]string{
		"test/acceptance/issue_9999_test.go": src,
		"test/acceptance/issue_9998_test.go": other,
	}), provided)
	want := []string{
		`test/acceptance/issue_9998_test.go:6: exec runs a binary this check cannot name`,
		`test/acceptance/issue_9999_test.go:9: runs "ffmpeg", which the acceptance CI job does not provide`,
		`test/acceptance/issue_9999_test.go:13: runs "hdiutil", which only darwin has, outside a runtime.GOOS check`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d violations, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("violation %d = %q, want prefix %q", i, got[i], w)
		}
	}
}
