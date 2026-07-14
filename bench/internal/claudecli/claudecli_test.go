package claudecli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNewRequiresModel(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error when model is empty")
	}
	r, err := New(Options{Model: "sonnet"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.ServerName() != "bench" {
		t.Errorf("default ServerName = %q, want bench", r.ServerName())
	}
	if r.Model() != "sonnet" {
		t.Errorf("Model = %q", r.Model())
	}
}

// captureRunner records the last commandSpec and returns a canned stream.
type captureRunner struct {
	last   CommandSpec
	stdout string
	stderr string
	err    error
}

func (c *captureRunner) run(_ context.Context, spec CommandSpec) ([]byte, []byte, error) {
	c.last = spec
	return []byte(c.stdout), []byte(c.stderr), c.err
}

func TestRunBuildsConfigAndArgs(t *testing.T) {
	cr := &captureRunner{stdout: sampleStream}
	r, err := New(Options{Model: "claude-sonnet-5", ServerName: "bench", Exec: cr.run})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := r.Run(context.Background(), Request{
		Endpoint:   "http://localhost:8098",
		Credential: "secret-key-001",
		System:     "answer with FINAL ANSWER",
		Prompt:     "What is the revenue?",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Handle != "dps_abc123" {
		t.Errorf("Handle = %q", res.Handle)
	}

	// The prompt is delivered on stdin, never as a positional arg.
	if string(cr.last.Stdin) != "What is the revenue?" {
		t.Errorf("stdin = %q", cr.last.Stdin)
	}
	args := strings.Join(cr.last.Args, " ")
	for _, want := range []string{
		"-p", "--output-format stream-json", "--verbose",
		"--model claude-sonnet-5", "--strict-mcp-config",
		"--permission-mode bypassPermissions", "--allowedTools mcp__bench__",
		"--append-system-prompt", "--disallowedTools",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q; got %s", want, args)
		}
	}
	// The generated MCP config must have been written and removed after the run
	// (the temp dir is cleaned up); verify its content via the recorded path.
	if cr.last.Dir == "" {
		t.Fatal("no working directory set")
	}
	if _, statErr := os.Stat(cr.last.Dir); !os.IsNotExist(statErr) {
		t.Errorf("work dir %s should be removed after Run", cr.last.Dir)
	}
}

func TestRunWritesValidMCPConfig(t *testing.T) {
	// Intercept via a runner that reads the config file before it is cleaned up.
	var captured mcpConfig
	reader := func(_ context.Context, spec CommandSpec) ([]byte, []byte, error) {
		for _, a := range spec.Args {
			if strings.HasSuffix(a, "mcp-config.json") {
				raw, err := os.ReadFile(a) // #nosec G304 -- test-controlled path
				if err != nil {
					t.Fatalf("read config: %v", err)
				}
				if err := json.Unmarshal(raw, &captured); err != nil {
					t.Fatalf("unmarshal config: %v", err)
				}
			}
		}
		return []byte(sampleStream), nil, nil
	}
	r, err := New(Options{Model: "sonnet", ServerName: "bench", Exec: reader})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := r.Run(context.Background(), Request{Endpoint: "http://host:9", Credential: "k1"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	srv, ok := captured.MCPServers["bench"]
	if !ok {
		t.Fatalf("config has no bench server: %+v", captured)
	}
	if srv.Type != "http" || srv.URL != "http://host:9" {
		t.Errorf("server = %+v", srv)
	}
	if srv.Headers["Authorization"] != "Bearer k1" {
		t.Errorf("Authorization = %q", srv.Headers["Authorization"])
	}
}

func TestRunParseFailureSurfacesStderr(t *testing.T) {
	cr := &captureRunner{stdout: "no result here", stderr: "boom", err: context.DeadlineExceeded}
	r, err := New(Options{Model: "sonnet", Exec: cr.run})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, runErr := r.Run(context.Background(), Request{Endpoint: "http://h", Credential: "k"})
	if runErr == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(runErr.Error(), "boom") {
		t.Errorf("error should surface stderr: %v", runErr)
	}
}

func TestVersion(t *testing.T) {
	cr := &captureRunner{stdout: "2.1.208 (Claude Code)\n"}
	r, err := New(Options{Model: "sonnet", Exec: cr.run})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v, err := r.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != "2.1.208 (Claude Code)" {
		t.Errorf("Version = %q", v)
	}
	if len(cr.last.Args) != 1 || cr.last.Args[0] != "--version" {
		t.Errorf("args = %v", cr.last.Args)
	}
}

func TestExecCommandRealProcess(t *testing.T) {
	// Exercise the default runner against a trivial, portable process so the
	// exec plumbing (stdin, stdout capture) is covered without the claude binary.
	stdout, _, err := execCommand(context.Background(), CommandSpec{
		Bin:   "cat",
		Stdin: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("execCommand: %v", err)
	}
	if string(stdout) != "hello" {
		t.Errorf("stdout = %q, want hello", stdout)
	}
}

func TestEnvWithoutAPIKey(t *testing.T) {
	in := []string{"PATH=/bin", "ANTHROPIC_API_KEY=secret", "HOME=/home/x", "ANTHROPIC_API_KEY_OTHER=keep"}
	out := envWithoutAPIKey(in)
	for _, kv := range out {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Errorf("ANTHROPIC_API_KEY not stripped: %v", out)
		}
	}
	// A similarly-named var must not be stripped.
	var keptOther bool
	for _, kv := range out {
		if kv == "ANTHROPIC_API_KEY_OTHER=keep" {
			keptOther = true
		}
	}
	if !keptOther {
		t.Errorf("stripped an unrelated var: %v", out)
	}
	if len(out) != 3 {
		t.Errorf("got %d entries, want 3", len(out))
	}
}

func TestExecCommandError(t *testing.T) {
	_, _, err := execCommand(context.Background(), CommandSpec{Bin: "this-binary-does-not-exist-xyz"})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestDisallowedToolsOverride(t *testing.T) {
	cr := &captureRunner{stdout: sampleStream}
	r, err := New(Options{Model: "sonnet", DisallowedTools: []string{"Bash"}, Exec: cr.run})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := r.Run(context.Background(), Request{Endpoint: "http://h", Credential: "k"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	args := strings.Join(cr.last.Args, " ")
	if !strings.Contains(args, "--disallowedTools Bash ") && !strings.HasSuffix(args, "--disallowedTools Bash") {
		t.Errorf("disallowed override not applied: %s", args)
	}
}
