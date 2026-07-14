// Package claudecli adapts the benchmark to a real Claude Code client: instead
// of driving the harness's own agent loop against a harness-owned MCP session,
// it invokes `claude -p` headless, pointed at the bench platform through a
// generated per-attempt MCP config authenticated with the attempt's identity-
// pool key. Claude Code connects to the platform directly and threads the
// platform_info-minted handle itself, exactly as a production agent does, so
// the run exercises the real handle-threading and search-first steering rather
// than the harness's synthetic loop.
//
// This path is a supported, clearly-labeled alternative to the canonical
// anthropic adapter for subscription-funded runs. Because `claude -p` reinserts
// Claude Code's own system prompt, tool-use policy, context management and
// retries — all of which change across Claude Code releases — its numbers are
// internally valid within one run but not comparable across Claude Code
// versions. The run manifest therefore records the Claude Code version so a
// claude-cli run is never silently compared against a raw Messages API run.
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultDisallowedTools are the built-in Claude Code tools the bench run
// forbids: the benchmark measures data work through the bench MCP server only,
// so the model must not touch the filesystem, shell, or web. The MCP data tools
// (mcp__<server>__*) are allowed via AllowedTools. This is defense in depth
// alongside StrictMCPConfig and the isolated working directory.
var defaultDisallowedTools = []string{
	"Bash", "Edit", "Write", "NotebookEdit", "Read", "Glob", "Grep",
	"WebFetch", "WebSearch", "Task", "TodoWrite",
}

// Options configures the runner. Zero values fall back to the documented
// defaults so a caller need only set Model and ServerName.
type Options struct {
	// Bin is the claude executable (default "claude").
	Bin string
	// Model is the --model value (an alias like "sonnet" or a full id). Required.
	Model string
	// ServerName is the MCP server key in the generated config; Claude Code
	// namespaces its tools as mcp__<ServerName>__<tool> (default "bench").
	ServerName string
	// PermissionMode is the --permission-mode (default "bypassPermissions" so
	// the headless run never blocks on a permission prompt).
	PermissionMode string
	// DisallowedTools overrides defaultDisallowedTools when non-nil.
	DisallowedTools []string
	// ExtraArgs are appended verbatim, for operator tuning without a rebuild.
	ExtraArgs []string
	// WorkDir is the parent directory for per-attempt temp working directories
	// (default os.TempDir()). Each attempt runs in a fresh empty subdirectory so
	// the model has no repository context and file tools cannot reach the repo.
	WorkDir string
	// Exec is the process runner; nil uses execCommand. It is a dependency-
	// injection seam (like http.Client.Transport): tests supply a stub that
	// returns a canned stream-json transcript so the runner — and the pipeline
	// and lifecycle paths that drive it — are exercisable without the real
	// claude binary.
	Exec CommandRunner
}

// CommandSpec is one child-process invocation.
type CommandSpec struct {
	Dir   string
	Bin   string
	Args  []string
	Stdin []byte
}

// CommandRunner runs a child process and returns its stdout, stderr and error.
type CommandRunner func(ctx context.Context, spec CommandSpec) (stdout, stderr []byte, err error)

// Runner invokes `claude -p` for benchmark episodes.
type Runner struct {
	opts Options
}

// New returns a Runner, applying defaults. Model is required.
func New(opts Options) (*Runner, error) {
	if strings.TrimSpace(opts.Model) == "" {
		return nil, errors.New("claudecli: model is required")
	}
	if opts.Bin == "" {
		opts.Bin = "claude"
	}
	if opts.ServerName == "" {
		opts.ServerName = "bench"
	}
	if opts.PermissionMode == "" {
		opts.PermissionMode = "bypassPermissions"
	}
	if opts.DisallowedTools == nil {
		opts.DisallowedTools = defaultDisallowedTools
	}
	if opts.WorkDir == "" {
		opts.WorkDir = os.TempDir()
	}
	if opts.Exec == nil {
		opts.Exec = execCommand
	}
	return &Runner{opts: opts}, nil
}

// Model returns the configured model id (for the run manifest).
func (r *Runner) Model() string { return r.opts.Model }

// ServerName returns the MCP server key used in generated configs.
func (r *Runner) ServerName() string { return r.opts.ServerName }

// Request is one episode's inputs.
type Request struct {
	// Endpoint is the platform MCP + REST base URL (the streamable endpoint).
	Endpoint string
	// Credential is the bearer token value for this attempt's pool identity.
	Credential string
	// System is appended to Claude Code's own system prompt (the FINAL ANSWER
	// format convention and grounding rules).
	System string
	// Prompt is the task prompt.
	Prompt string
}

// Version reports the Claude Code version string (`claude --version`), for the
// manifest's client-path record.
func (r *Runner) Version(ctx context.Context) (string, error) {
	stdout, stderr, err := r.opts.Exec(ctx, CommandSpec{Bin: r.opts.Bin, Args: []string{"--version"}})
	if err != nil {
		return "", fmt.Errorf("claude --version: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(string(stdout)), nil
}

// Run executes one episode: it writes an isolated MCP config authenticated with
// the request's credential, invokes `claude -p`, and parses the stream-json
// output into a Result. A non-zero exit is not by itself fatal — claude emits a
// terminal result event on model errors too — so the stream is parsed
// regardless and the error is surfaced only when the stream carries no result.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	dir, err := os.MkdirTemp(r.opts.WorkDir, "bench-claudecli-")
	if err != nil {
		return Result{}, fmt.Errorf("create work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfgPath := filepath.Join(dir, "mcp-config.json")
	if err := writeMCPConfig(cfgPath, r.opts.ServerName, req.Endpoint, req.Credential); err != nil {
		return Result{}, err
	}

	args := r.buildArgs(cfgPath, req.System)
	stdout, stderr, runErr := r.opts.Exec(ctx, CommandSpec{
		Dir:   dir,
		Bin:   r.opts.Bin,
		Args:  args,
		Stdin: []byte(req.Prompt),
	})

	res, parseErr := Parse(r.opts.ServerName, req.Prompt, stdout)
	if parseErr != nil {
		exit := "nil"
		if runErr != nil {
			exit = runErr.Error()
		}
		return Result{}, fmt.Errorf("%w; claude exit: %s; stderr: %.500s", parseErr, exit, strings.TrimSpace(string(stderr)))
	}
	return res, nil
}

// buildArgs assembles the `claude -p` argument vector. List-valued flags are
// passed as a single comma-joined token so the variadic flag never swallows a
// following flag (the prompt arrives on stdin, not as a positional).
func (r *Runner) buildArgs(cfgPath, system string) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--model", r.opts.Model,
		"--mcp-config", cfgPath,
		"--strict-mcp-config",
		"--permission-mode", r.opts.PermissionMode,
		"--allowedTools", serverToolPrefix(r.opts.ServerName),
	}
	if len(r.opts.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(r.opts.DisallowedTools, ","))
	}
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}
	args = append(args, r.opts.ExtraArgs...)
	return args
}

// mcpConfig is the --mcp-config file shape for a single streamable-HTTP server.
type mcpConfig struct {
	MCPServers map[string]mcpServerConfig `json:"mcpServers"`
}

// mcpServerConfig is one HTTP MCP server entry.
type mcpServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// writeMCPConfig writes the per-attempt MCP config pointing Claude Code at the
// platform's streamable endpoint, authenticated as this attempt's identity. The
// file is 0600: it carries the attempt's bearer token.
func writeMCPConfig(path, server, endpoint, credential string) error {
	cfg := mcpConfig{MCPServers: map[string]mcpServerConfig{
		server: {
			Type:    "http",
			URL:     endpoint,
			Headers: map[string]string{"Authorization": "Bearer " + credential},
		},
	}}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp config: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write mcp config: %w", err)
	}
	return nil
}

// execCommand is the default commandRunner. It captures stdout and stderr
// separately and returns the process error (if any) alongside both, so the
// caller can parse a partial stream even on a non-zero exit. The child env
// strips ANTHROPIC_API_KEY so the run is always subscription-funded: a key
// sourced for the metered anthropic adapter must never silently make a
// claude-cli run bill the API (issue #949's whole point is the keyless path).
func execCommand(ctx context.Context, spec CommandSpec) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, spec.Bin, spec.Args...) // #nosec G204 -- bin is operator-configured, args are harness-built
	cmd.Dir = spec.Dir
	cmd.Env = envWithoutAPIKey(os.Environ())
	if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// envWithoutAPIKey returns env with any ANTHROPIC_API_KEY entry removed, so the
// child claude authenticates against the logged-in subscription rather than a
// metered API key.
func envWithoutAPIKey(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
