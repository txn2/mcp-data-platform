//go:build integration

package acceptance

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1894: a log record written on a tool call's path carries the trace
// id of the call's span, on stderr and, with OTEL_LOGS_EXPORTER=otlp, at the
// collector; the Semgrep rule refuses a context-less slog call in a covered
// package.
//
// Wire forms: list_connections takes no arguments; the criteria send none.
//
// The call that logs: the inventory analyst's persona does not allow
// list_connections (dev/platform.yaml), and the authorization middleware
// writes "tool call authorization denied" for it, inner to the tracing
// middleware, so the record's context carries the call's span.
const deniedLogMessage = "tool call authorization denied"

// stderrLogFiles are where each dev replica's stderr goes (DEV_AIR_LOG and
// DEV_AIR_B_LOG from dev/.dev-ports.env, written by dev/start.sh). The
// proxy in front of the replicas may send the call to either.
func stderrLogFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, key := range []string{"DEV_AIR_LOG", "DEV_AIR_B_LOG"} {
		if v := os.Getenv(key); v != "" {
			files = append(files, v)
		}
	}
	if len(files) == 0 {
		t.Fatalf("DEV_AIR_LOG is not set; `make acceptance` loads it from dev/.dev-ports.env, which `make dev` writes")
	}
	return files
}

// findStderrLine returns the first JSON line in the replicas' stderr whose
// msg, tool and trace_id are as given.
func findStderrLine(t *testing.T, msg, tool, traceID string) map[string]any {
	t.Helper()
	for _, path := range stderrLogFiles(t) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var rec map[string]any
			if json.Unmarshal(line, &rec) != nil {
				continue
			}
			if rec["msg"] == msg && rec["tool"] == tool && rec["trace_id"] == traceID {
				_ = f.Close()
				return rec
			}
		}
		_ = f.Close()
	}
	return nil
}

// exportedLog is one log record as the dev collector wrote it.
type exportedLog struct {
	Body     string
	TraceID  string
	SpanID   string
	Resource map[string]string
	Attrs    map[string]string
}

// readLogs parses every record the collector has written to logs.jsonl.
func readLogs(t *testing.T) []exportedLog {
	t.Helper()
	f, err := os.Open(otelSignalFile("logs.jsonl"))
	if err != nil {
		t.Fatalf("the dev collector's log file: %v. `make dev` starts the platform with OTEL_LOGS_EXPORTER=otlp", err)
	}
	defer f.Close() //nolint:errcheck // test
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var out []exportedLog
	for sc.Scan() {
		var line struct {
			ResourceLogs []struct {
				Resource struct {
					Attributes []otlpAttr `json:"attributes"`
				} `json:"resource"`
				ScopeLogs []struct {
					LogRecords []struct {
						Body struct {
							String string `json:"stringValue"`
						} `json:"body"`
						TraceID    string     `json:"traceId"`
						SpanID     string     `json:"spanId"`
						Attributes []otlpAttr `json:"attributes"`
					} `json:"logRecords"`
				} `json:"scopeLogs"`
			} `json:"resourceLogs"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("collector line is not OTLP JSON: %v", err)
		}
		for _, rl := range line.ResourceLogs {
			res := attrsOf(rl.Resource.Attributes)
			for _, sl := range rl.ScopeLogs {
				for _, rec := range sl.LogRecords {
					out = append(out, exportedLog{Body: rec.Body.String, TraceID: rec.TraceID, SpanID: rec.SpanID, Resource: res, Attrs: attrsOf(rec.Attributes)})
				}
			}
		}
	}
	return out
}

// deniedCall makes the analyst's refused list_connections and returns the
// span of that call.
func deniedCall(t *testing.T) exportedSpan {
	t.Helper()
	analyst := connectBare(t, analystKey1892)
	res, text := callBare(t, analyst, "list_connections", nil)
	if !res.IsError || !strings.Contains(text, "not authorized") {
		t.Fatalf("list_connections as the analyst should be refused by the persona; got error=%v %q", res.IsError, text)
	}
	return awaitSpan(t, analyst.ID(), "list_connections")
}

// TestIssue1894_AToolCallsLogLineCarriesTheSpansTraceId: the stderr line the
// refusal wrote has a trace_id equal to the exported span's trace id, and a
// span_id equal to the span's.
func TestIssue1894_AToolCallsLogLineCarriesTheSpansTraceId(t *testing.T) {
	span := deniedCall(t)
	deadline := time.Now().Add(spanWait)
	for {
		if rec := findStderrLine(t, deniedLogMessage, "list_connections", span.TraceID); rec != nil {
			if rec["span_id"] != span.SpanID {
				t.Errorf("stderr span_id = %v, want the span's %s", rec["span_id"], span.SpanID)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no stderr line %q for list_connections with trace_id %s within %s (files %v)", deniedLogMessage, span.TraceID, spanWait, stderrLogFiles(t))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue1894_AnExportedLogRecordCarriesTheTraceId: the same record,
// exported over OTLP through the slog bridge, carries the span's trace and
// span ids and the deployment's resource.
func TestIssue1894_AnExportedLogRecordCarriesTheTraceId(t *testing.T) {
	span := deniedCall(t)
	deadline := time.Now().Add(spanWait)
	for {
		for _, rec := range readLogs(t) {
			if rec.Body == deniedLogMessage && rec.TraceID == span.TraceID && rec.Attrs["tool"] == "list_connections" {
				if rec.SpanID != span.SpanID {
					t.Errorf("exported span id = %s, want %s", rec.SpanID, span.SpanID)
				}
				assertDeploymentResource1893(t, "log record", rec.Resource)
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no exported log record %q with trace id %s within %s", deniedLogMessage, span.TraceID, spanWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestIssue1894_TheSemgrepRuleRefusesAContextlessCall: the rule in
// .semgrep/go-slog-context.yml fails on a new context-less slog call in a
// covered package, inside a closure of such a function too, and passes the
// Context form, a blank-identifier context, and a function with no context.
func TestIssue1894_TheSemgrepRuleRefusesAContextlessCall(t *testing.T) {
	semgrep, err := exec.LookPath("semgrep")
	if err != nil {
		t.Fatalf("semgrep is not installed; make verify's semgrep gate needs it and so does this criterion")
	}
	rule, err := filepath.Abs(filepath.Join("..", "..", ".semgrep", "go-slog-context.yml"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "pkg", "middleware")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	run := func(src string) (int, string) {
		if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(semgrep, "scan", "--config", rule, "--error", "--quiet", "--json", ".") //nolint:gosec // test runs the gate's tool on a fixture
		cmd.Dir = root
		out, err := cmd.Output()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("semgrep: %v", err)
		}
		var report struct {
			Results []struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"results"`
		}
		if err := json.Unmarshal(out, &report); err != nil {
			t.Fatalf("semgrep output: %v: %s", err, out)
		}
		lines := make([]string, 0, len(report.Results))
		for _, r := range report.Results {
			lines = append(lines, strconv.Itoa(r.Start.Line))
		}
		return code, strings.Join(lines, ",")
	}

	code, lines := run(`package middleware

import (
	"context"
	"log/slog"
)

func refuse(ctx context.Context, n int) {
	slog.Info("refused", "n", n)
	go func() {
		slog.Warn("in a closure")
	}()
}

type T struct{}

func (t *T) method(a string, reqCtx context.Context) {
	slog.Debug("method")
}
`)
	if code == 0 || lines != "9,11,18" {
		t.Errorf("the rule should fail (exit 1) on lines 9, 11 and 18; exit %d, lines %q", code, lines)
	}

	code, lines = run(`package middleware

import (
	"context"
	"log/slog"
)

func ok(ctx context.Context) {
	slog.InfoContext(ctx, "fine")
}

func blank(_ context.Context) {
	slog.Info("no usable context")
}

func noCtx() {
	slog.Info("no context in scope")
}
`)
	if code != 0 || lines != "" {
		t.Errorf("the rule should pass the Context form, a blank context and a context-less function; exit %d, lines %q", code, lines)
	}
}
