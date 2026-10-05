package gates

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// semgrepDiffScript and relayRule are the gate under test and the rule #2018
// added to it.
const (
	semgrepDiffScript = "scripts/semgrep-diff.py"
	relayRule         = ".semgrep-diff/go-relay.yml"
)

// requireSemgrep returns when semgrep is on PATH. `make verify` runs
// semgrep-diff, so a machine running verify has it; CI's unit job does not
// install it, and there the test is skipped rather than failed. A local run
// without it fails, so a missing binary cannot read as a green rule.
func requireSemgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("semgrep"); err == nil {
		return
	}
	if os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("semgrep is not installed on this CI runner; make verify runs this test locally")
	}
	t.Fatal("semgrep is not on PATH; make semgrep-diff and this test need it")
}

// relayBase is a handler file that already holds a deliberate relay, the
// shape internal/platform/utilhandler/fetch.go has today.
const relayBase = `package h

import (
	"io"
	"net/http"
)

func relay(w http.ResponseWriter, resp *http.Response) {
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
`

// pdfRefusal2017 is the #2017 shape: a refused in-process read answered with
// the recorded headers and body.
const pdfRefusal2017 = relayBase + `
type documentRecorder struct {
	header http.Header
	status int
	body   bytesBuffer
}

type bytesBuffer struct{ b []byte }

func (b *bytesBuffer) Bytes() []byte { return b.b }

func refuse(w http.ResponseWriter, rec *documentRecorder) {
	for name, values := range rec.header {
		if name != "Content-Length" {
			w.Header()[name] = values
		}
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.body.Bytes())
}
`

// pdfRefusalFixed is the fix #2017 shipped: a fixed allowlist of headers and
// the status alone, through http.Error and the platform's own text.
const pdfRefusalFixed = relayBase + `
var relayedHeaders = []string{"Retry-After", "WWW-Authenticate", "Location"}

func refuse(w http.ResponseWriter, header http.Header, code int) {
	for _, name := range relayedHeaders {
		if v := header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	http.Error(w, "The document could not be read: "+http.StatusText(code)+".", code)
}
`

// upstreamRead is an upstream body read whole and written, and an upstream
// header map copied wholesale.
const upstreamRead = relayBase + `
func readAndRelay(w http.ResponseWriter, resp *http.Response) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	_, _ = w.Write(data)
}
`

// TestSemgrepDiffRefusesARelayedBody holds the #2018 rule to the #2017 shape
// through the real gate script, and to the scoping that keeps a relay already
// on main from firing when its line did not change.
func TestSemgrepDiffRefusesARelayedBody(t *testing.T) {
	requireSemgrep(t)
	// #nosec G304 -- relayRule is this package's constant rule path.
	rule, err := os.ReadFile(filepath.Join("..", "..", relayRule))
	require(t, err)

	cases := []struct {
		name   string
		change string
		expect expect
	}{
		{
			name:   "the #2017 shape is refused, body and headers both",
			change: pdfRefusal2017,
			expect: expect{want: []string{"[relayed-response-body]", "[relayed-response-headers]", "blobserve.Serve"}},
		},
		{
			name:   "an upstream body read whole, and its header map copied, is refused",
			change: upstreamRead,
			expect: expect{want: []string{"[relayed-response-body]", "[relayed-response-headers]"}},
		},
		{
			name:   "the fix #2017 shipped passes",
			change: pdfRefusalFixed,
			expect: expect{pass: true, mustNot: []string{"[relayed-response-"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newGitRepo(t, semgrepDiffScript, map[string]string{
				relayRule:         string(rule),
				"internal/h/h.go": relayBase,
			})
			writeFile(t, root, "internal/h/h.go", tc.change)
			out, passed := runScript(t, root, []string{semgrepDiffScript, relayRule}, nil)
			assertOutput(t, out, passed, tc.expect)
		})
	}
}

// TestRelayRuleSparesTheSanctionedWriters runs the rule over whole files that
// serve foreign bytes on purpose -- blobserve, the API gateway's raw
// passthrough on both sides, and the PDF route as #2017 left it -- and
// requires no finding, so an edit to any of them is not refused for the shape
// it was built to avoid.
func TestRelayRuleSparesTheSanctionedWriters(t *testing.T) {
	requireSemgrep(t)
	files := []string{
		"pkg/blobserve/blobserve.go",
		"pkg/toolkits/apigateway/raw.go",
		"internal/httpserver/gatewayhttp/handler.go",
		"internal/httpserver/pdfhttp/pdfhttp.go",
	}
	args := append([]string{"scan", "--config", relayRule, "--json", "--quiet", "--"}, files...)
	cmd := exec.CommandContext(t.Context(), "semgrep", args...) //nolint:gosec // fixed binary, constant arguments
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	require(t, err)
	var report struct {
		Results []struct {
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"results"`
		Errors []json.RawMessage `json:"errors"`
	}
	require(t, json.Unmarshal(out, &report))
	if len(report.Errors) > 0 {
		t.Fatalf("semgrep reported errors: %s", report.Errors)
	}
	for _, r := range report.Results {
		t.Errorf("%s:%d: %s fires on a sanctioned writer", r.Path, r.Start.Line, r.CheckID)
	}
}

// TestSemgrepDiffRunsEveryRuleFile pins that make semgrep-diff hands the
// script the rule directory, so a rule file added beside the others is run
// rather than sitting unread until someone edits the Makefile.
func TestSemgrepDiffRunsEveryRuleFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	require(t, err)
	if indexOf(recipe(t, string(raw), "semgrep-diff"), "scripts/semgrep-diff.py .semgrep-diff/") < 0 {
		t.Error("make semgrep-diff does not run the whole .semgrep-diff/ directory")
	}
}
