package sitetool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
	"github.com/txn2/mcp-data-platform/internal/site/sitetypecheck"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
)

func content(name, body string) File { return File{Path: name, Content: &body} }

func TestBuildCommands(t *testing.T) {
	// Each run starts with no session shown the guide.
	UseGuideStore(searchgate.NewMemoryStore(guideTTL))
	f := newFixture()
	createSite(t, f)
	ctx := admin()
	build := `{"entries":["app.tsx"],"imports_from":"package.json","sourcemap":"private"}`
	published := call(t, f, ctx, Input{Command: CmdPublish, Site: siteName, Files: []File{
		content(sitebuild.ControlFile, build),
		content("app.tsx", "import { createRoot } from \"react-dom/client\";\nfunction boom(): never { throw new Error(\"x\"); }\ncreateRoot(document.body).render(<p onClick={boom} />);\n"),
		content("index.html", `<script type="module" src="/app.js"></script>`),
		content("AGENTS.md", "# How this site is built\nentries: app.tsx"),
		content("package.json", `{"dependencies":{"react":"18.3.1","react-dom":"18.3.1"}}`),
		content("package-lock.json", `{"lockfileVersion":3,"packages":{"node_modules/react":{"version":"18.3.1"},"node_modules/react-dom":{"version":"18.3.1","peerDependencies":{"react":"^18.3.1"}}}}`),
	}})
	builds, ok := published[fieldBuilds].([]any)
	require.True(t, ok, "publish reports the compile: %v", published)
	require.Len(t, builds, 1)
	guide, ok := published[fieldGuide].(map[string]any)
	require.True(t, ok, "the first publish in a session carries the guide: %v", published)
	assert.Contains(t, guide["content"], "How this site is built")

	again := call(t, f, ctx, Input{Command: CmdPublish, Site: siteName, Base: 1, DryRun: true, Files: []File{content("index.html", "<p>changed</p>")}})
	assert.Nil(t, again[fieldGuide], "a later call in the session does not repeat the guide")
	assert.NotNil(t, again[fieldBuilds], "a dry run reports the compile")

	got := call(t, f, ctx, Input{Command: CmdGet, Site: siteName})
	assert.NotNil(t, got[fieldGuide], "get returns the guide")

	proposed := call(t, f, ctx, Input{Command: CmdImports, Site: siteName})
	imports, _ := proposed["imports"].(map[string]any)
	assert.Equal(t, "https://esm.sh/react-dom@18.3.1/client?deps=react@18.3.1", imports["react-dom/client"])
	assert.Equal(t, "package-lock.json", proposed["lockfile"])

	report := call(t, f, ctx, Input{Command: CmdBuildReport, Site: siteName, Release: 1})
	assert.Len(t, report[fieldBuilds], 1)

	lines := make([]string, 0, 10)
	lines = append(lines, "Error: x", " at y (https://x/other.js:1:1)")
	for line := 1; line <= 8; line++ {
		lines = append(lines, " at boom (https://x/app.js:"+strconv.Itoa(line)+":3)")
	}
	trace := strings.Join(lines, "\n")
	symbol := call(t, f, ctx, Input{Command: CmdSymbolicate, Site: siteName, Stack: trace})
	raw, _ := json.Marshal(symbol["frames"])
	assert.Contains(t, string(raw), "app.tsx", "the frame maps to the source")
	assert.Contains(t, string(raw), "no source map for other.js")

	assert.Contains(t, refusal(t, f, ctx, Input{Command: CmdSymbolicate, Site: siteName, Stack: strings.Repeat("x", maxStackBytes+1)}), "more than")
}

func TestBuildCommandRefusals(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	ctx := admin()
	call(t, f, ctx, Input{Command: CmdPublish, Site: siteName, Files: []File{content("index.html", "<p>plain</p>")}})
	assert.Contains(t, refusal(t, f, ctx, Input{Command: CmdImports, Site: siteName}), "has no _build.json")
	report := call(t, f, ctx, Input{Command: CmdBuildReport, Site: siteName})
	assert.Contains(t, report["message"], "recorded no compile")
	assert.Contains(t, refusal(t, f, ctx, Input{Command: CmdSymbolicate, Site: siteName}), "needs stack")
	symbol := call(t, f, ctx, Input{Command: CmdSymbolicate, Site: siteName, Stack: "at x (https://h/app.js:1:1)\nat y (https://h/app.js:2:1)"})
	raw, _ := json.Marshal(symbol["frames"])
	assert.Contains(t, string(raw), "no source map for app.js")

	call(t, f, ctx, Input{Command: CmdPublish, Site: siteName, Base: 1, Files: []File{
		content(sitebuild.ControlFile, `{"entries":["app.tsx"]}`), content("app.tsx", `console.log(1);`),
	}})
	assert.Contains(t, refusal(t, f, ctx, Input{Command: CmdImports, Site: siteName}), "no package-lock.json")
	assert.Contains(t, refusal(t, f, ctx, Input{Command: CmdPublish, Site: siteName, Base: 2, Files: []File{content(sitebuild.ControlFile, `{"entries":[]}`)}}), "entries is empty")
}

// fakeTypechecks records queued checks and answers the latest.
type fakeTypechecks struct {
	queued []string
	latest *sitetypecheck.Check
}

func (f *fakeTypechecks) Enqueue(_ context.Context, r sitetypecheck.Request) (int64, error) {
	f.queued = append(f.queued, fmt.Sprintf("%s#%d@%s:%s by %s", r.SiteID, r.Release, r.Environment, r.Tsconfig, r.By))
	return int64(len(f.queued)), nil
}

func (f *fakeTypechecks) Latest(context.Context, string, int) (*sitetypecheck.Check, error) {
	return f.latest, nil
}

// TestTypecheckCommand is #2793 through manage_site: typecheck queues a
// release's check when its _build.json asks for one, refuses one that asks for
// none or a deployment that runs none, and build_report carries the result.
func TestTypecheckCommand(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	ctx := admin()
	assert.Contains(t, refusal(t, f, ctx, Input{Command: CmdTypecheck, Site: siteName}), "runs no type checks")

	queue := &fakeTypechecks{latest: &sitetypecheck.Check{
		ID: 7, Status: sitetypecheck.StatusErrors,
		Result: &sitetypecheck.Result{Status: sitetypecheck.StatusErrors, Diagnostics: []sitetypecheck.Diagnostic{{File: "src/a.ts", Line: 1, Column: 14, Code: "TS2322", Message: "bad"}}},
	}}
	f.tool.Typechecks = queue
	f.tool.Service.AfterPublish = sitetypecheck.AfterPublish(queue, f.tool.Service)
	call(t, f, ctx, Input{Command: CmdPublish, Site: siteName, Files: []File{
		content(sitebuild.ControlFile, `{"entries":["app.tsx"]}`), content("app.tsx", "export const a = 1;\n"),
	}})
	assert.Contains(t, refusal(t, f, ctx, Input{Command: CmdTypecheck, Site: siteName}), "asks for no type check")

	published := call(t, f, ctx, Input{Command: CmdPublish, Site: siteName, Files: []File{
		content(sitebuild.ControlFile, `{"entries":["app.tsx"],"typecheck":{"tsconfig":"tsconfig.app.json"}}`), content("app.tsx", "export const a = 1;\n"),
	}})
	require.Len(t, queue.queued, 1, "a publish of a site asking for a check queues one")
	// The publish reports the release's newest check, as the queue holds it.
	queuedByPublish, _ := published[fieldTypecheck].(map[string]any)
	assert.InDelta(t, 7, queuedByPublish["id"], 0)
	assert.Equal(t, "errors", queuedByPublish["status"])
	got := call(t, f, ctx, Input{Command: CmdTypecheck, Site: siteName})
	require.Len(t, queue.queued, 2)
	assert.Contains(t, queue.queued[1], "#2@dev:tsconfig.app.json by "+adminEmail)
	tc, _ := got[fieldTypecheck].(map[string]any)
	assert.Equal(t, "queued", tc["status"])

	report := call(t, f, ctx, Input{Command: CmdBuildReport, Site: siteName})
	check, _ := report[fieldTypecheck].(map[string]any)
	assert.Equal(t, "errors", check["status"])
}
