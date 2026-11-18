package gates

// A recorded run is stale only when the diff changes what its criteria
// exercise (#2693): within the package registering a tool, a changed function
// counts against an acceptance file when the tool's handler reaches it through
// a command the file names, or through the code every command runs. On #2692
// the package rule marked 139 tickets stale for a change to manage_site's
// set_domain and the api gateway's OAuth config type.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// shopSource is a tool with commands: open reaches openShop and price, close
// reaches closeShop, and handle and handlers run for every command.
const shopSource = `package shop

type handler func(in Input) (any, error)

// Input is the tool's arguments.
type Input struct {
	Command string ` + "`json:\"command\"`" + `
	Price   int    ` + "`json:\"price\"`" + `
}

const description = "A shop." +
	" Open and close it."

func register(s any) {
	toolkit.AddTool(s, &mcp.Tool{Name: "shop_manage", Description: description}, handle)
}

func handle(in Input) (any, error) {
	return handlers()[in.Command](in)
}

func handlers() map[string]handler {
	return map[string]handler{
		"open": func(in Input) (any, error) {
			return openShop(in)
		},
		"close": closeShop,
	}
}

func openShop(in Input) (any, error) {
	return price(in.Price), nil
}

func closeShop(in Input) (any, error) {
	return nil, nil
}

func price(n int) int {
	return n
}
`

// reachRepo is a git tree whose base commit registers shop_manage and holds
// three acceptance files: one calling open, one calling close, and one whose
// command is not a literal.
func reachRepo(t *testing.T) string {
	t.Helper()
	root := newRepo(t)
	require(t, os.MkdirAll(filepath.Join(root, "test", "toolsurface"), 0o750))
	require(t, os.WriteFile(filepath.Join(root, "test", "toolsurface", "toolsurface.go"), []byte(`package toolsurface

var Annotations = map[string]Expectation{
	"shop_manage": {},
}
`), 0o600))
	dir := filepath.Join(root, "pkg", "toolkits", "shop")
	require(t, os.MkdirAll(dir, 0o750))
	require(t, os.WriteFile(filepath.Join(dir, "shop.go"), []byte(shopSource), 0o600))
	writeAcceptance(t, root, "9401", "// Wire forms: none.\n\nfunc TestIssue9401_Open(t *testing.T) { c.call(\"shop_manage\", map[string]any{\"command\": \"open\"}) }\n")
	writeAcceptance(t, root, "9402", "// Wire forms: none.\n\nfunc TestIssue9402_Close(t *testing.T) { c.call(\"shop_manage\", map[string]any{\"command\": \"close\"}) }\n")
	writeAcceptance(t, root, "9403", "// Wire forms: none.\n\nfunc TestIssue9403_Any(t *testing.T) { shopDo9403(c, \"open\") }\n")
	require(t, os.WriteFile(filepath.Join(root, "test", "acceptance", "shop_helpers_test.go"), []byte(
		"package acceptance\n\nfunc shopDo9403(c *client, command string) { c.call(\"shop_manage\", map[string]any{\"command\": command}) }\n"), 0o600))
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "-c", "user.email=gate@example.com", "-c", "user.name=gate", "commit", "-q", "-m", "base")
	return root
}

// reachGate runs one subcommand with the shop source on stdin and the diff
// read against the base commit.
func reachGate(t *testing.T, root string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", //nolint:gosec // a fixed script under a temp root this test wrote
		append([]string{filepath.Join(root, scriptRelPath)}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "ACCEPTANCE_DIFF_RANGE=HEAD")
	cmd.Stdin = strings.NewReader("pkg/toolkits/shop/shop.go\n")
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func TestStalenessFollowsTheCommandACriterionCalls(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, old, new, want string
	}{
		{"a command no criterion calls", "return nil, nil", "return \"closed\", nil", "9402,9403"},
		{"a function one command reaches", "return n\n", "return n + 1\n", "9401,9403"},
		{"a command's inline handler", "return openShop(in)", "return openShop(Input{})", "9401,9403"},
		{"the code every command runs", "return handlers()[in.Command](in)", "return handlers()[in.Command](Input{})", "9401,9402,9403"},
		{"a field one command reads", "Price   int    ", "Price   int64  ", "9401,9403"},
		{"the type every handler has", "type handler func(in Input) (any, error)", "type handler func(Input) (any, error)", "9401,9402,9403"},
		{"the advertised description", "\" Open and close it.\"", "\" Open it, or close it.\"", ""},
		{"a comment", "// Input is the tool's arguments.", "// Input holds the tool's arguments.", ""},
		{"a constant group no command names", "const description", "const (\n\topStock = \"stock\"\n)\n\nconst description", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := reachRepo(t)
			source := filepath.Join(root, "pkg", "toolkits", "shop", "shop.go")
			if !strings.Contains(shopSource, tc.old) {
				t.Fatalf("the fixture holds no %q", tc.old)
			}
			// #nosec G703 -- source is a constant path under t.TempDir().
			require(t, os.WriteFile(source, []byte(strings.Replace(shopSource, tc.old, tc.new, 1)), 0o600))
			if out, ok := reachGate(t, root, "affected"); !ok || strings.TrimSpace(out) != tc.want {
				t.Errorf("selected %q (ok %v); want %q", out, ok, tc.want)
			}
		})
	}
}

// The check names, per stale ticket, the changed function that reached it.
func TestTheCheckNamesTheChangedFunction(t *testing.T) {
	t.Parallel()
	root := reachRepo(t)
	source := filepath.Join(root, "pkg", "toolkits", "shop", "shop.go")
	// #nosec G703 -- source is a constant path under t.TempDir().
	require(t, os.WriteFile(source, []byte(strings.Replace(shopSource, "return nil, nil", "return \"closed\", nil", 1)), 0o600))
	out, ok := reachGate(t, root, "check", "--changed-stdin")
	if ok || !strings.Contains(out, "#9402") || !strings.Contains(out, "(closeShop), which shop_manage reaches") || strings.Contains(out, "#9401") {
		t.Fatalf("ok %v; want #9402 failed naming closeShop and #9401 left alone:\n%s", ok, out)
	}
}

// #2692's own diff: manage_site is stale only for the files calling a command
// whose handler the change reached, and every api and graphql tool for the
// OAuth config type its handler reaches. The tree at #2692's merge is checked
// out beside a clone sharing this repository's objects, and today's script
// reads it.
func TestTheStaleSetOn2692FollowsTheCommands(t *testing.T) {
	t.Parallel()
	const base, head = "3f0d433b4", "0bb734fb0"
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	require(t, err)
	root := filepath.Join(t.TempDir(), "tree")
	gitIn(t, repo, "clone", "-q", "--shared", "--no-checkout", repo, root)
	gitIn(t, root, "checkout", "-q", head, "--", "go.mod", "internal", "pkg", "test/acceptance", "test/toolsurface")
	script, err := os.ReadFile(filepath.Join(repo, scriptRelPath)) // #nosec G304 -- this repository's own script
	require(t, err)
	require(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o750))
	// #nosec G703 -- root is under t.TempDir() and the join is a constant path.
	require(t, os.WriteFile(filepath.Join(root, scriptRelPath), script, 0o600))
	cmd := exec.CommandContext(t.Context(), "python3", "-c", `
import importlib.util, subprocess, sys
spec = importlib.util.spec_from_file_location("ev", "scripts/acceptance-evidence.py")
m = importlib.util.module_from_spec(spec); sys.modules["ev"] = m; spec.loader.exec_module(m)
changed = subprocess.run(["git", "diff", "--name-only", sys.argv[1], sys.argv[2]], capture_output=True, text=True, check=True).stdout.split()
src = {p.relative_to(m.REPO_ROOT).as_posix(): p.read_text() for p in (m.REPO_ROOT / "test" / "acceptance").glob("*_test.go")}
joined = "\n".join(src.values())
foreign = m.foreign_methods(joined)
funcs = {n: b for n, b in m.package_functions(joined).items() if not n.startswith("connect") and n not in foreign}
callers = sum(1 for tools in m.tools_called().values() if "manage_site" in tools)
stale = m.affected(changed)
print("callers", callers, "stale", sum(1 for h in stale.values() if "manage_site" in h))
for path, hits in sorted(stale.items()):
    ops = m.ops_called(src[path], funcs, set())
    for tool, (source, what) in sorted(hits.items()):
        print(tool, path, "*" if ops is None else ",".join(sorted(ops)), "|", what)
`, base, head) // #nosec G204 -- a fixed program over a clone of this repository
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "ACCEPTANCE_DIFF_RANGE="+base)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	domain := []string{"add_domain", "set_domain", "remove_domain", "get", "domains"}
	var callers, stale int
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 4 && fields[0] == "callers":
			callers, stale = atoi(t, fields[1]), atoi(t, fields[3])
		case len(fields) > 3 && fields[0] == "manage_site":
			ops := fields[2]
			if ops != "*" && !anyOf(strings.Split(ops, ","), domain) {
				t.Errorf("%s is stale for manage_site and calls none of %v: %s", fields[1], domain, line)
			}
		case len(fields) > 3 && (strings.HasPrefix(fields[0], "api_") || strings.HasPrefix(fields[0], "graphql_")):
			if !strings.Contains(line, "OAuth2Config") {
				t.Errorf("an api or graphql tool is stale for something other than the OAuth config: %s", line)
			}
		}
	}
	if callers == 0 || stale == 0 || stale*2 > callers {
		t.Errorf("manage_site: %d of %d calling files stale; want some, and under half\n%s", stale, callers, raw)
	}
}

func anyOf(have, want []string) bool {
	for _, h := range have {
		if slices.Contains(want, h) {
			return true
		}
	}
	return false
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("not a count: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// A command added under a new constant is that command's (#2696): the
// constant keys its dispatch entry, and a file naming other commands is not
// stale for it. Only the file whose command is not a literal is.
func TestANewCommandIsNotEveryCommandsChange(t *testing.T) {
	t.Parallel()
	root := reachRepo(t)
	source := filepath.Join(root, "pkg", "toolkits", "shop", "shop.go")
	changed := strings.Replace(shopSource, `"close": closeShop,`, "\"close\": closeShop,\n\t\topStock: closeShop,", 1)
	changed = strings.Replace(changed, "const description", "const opStock = \"stock\"\n\nconst description", 1)
	// #nosec G703 -- source is a constant path under t.TempDir().
	require(t, os.WriteFile(source, []byte(changed), 0o600))
	if out, ok := reachGate(t, root, "affected"); !ok || strings.TrimSpace(out) != "9403" {
		t.Errorf("selected %q (ok %v); want 9403", out, ok)
	}
}
