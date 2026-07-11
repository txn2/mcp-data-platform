package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestInitLogging(t *testing.T) {
	tests := []struct {
		env   string
		level slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},        // default
		{"unknown", slog.LevelInfo}, // unrecognized falls through
	}

	for _, tt := range tests {
		t.Run("LOG_LEVEL="+tt.env, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.env)
			initLogging()

			handler := slog.Default().Handler()
			// Verify the handler is enabled at the expected level
			if !handler.Enabled(context.Background(), tt.level) {
				t.Errorf("expected handler enabled at %v", tt.level)
			}
			// For non-debug levels, debug should be disabled
			if tt.level > slog.LevelDebug && handler.Enabled(context.Background(), slog.LevelDebug) {
				t.Errorf("expected debug disabled when LOG_LEVEL=%q", tt.env)
			}
		})
	}
}

func TestStartServer_UnknownTransport(t *testing.T) {
	err := startServer(context.TODO(), nil, nil, serverOptions{transport: "websocket"})
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
	if !strings.Contains(err.Error(), "unknown transport") {
		t.Errorf("error = %q, want 'unknown transport'", err.Error())
	}
}

func TestStartServer_HTTPBackwardCompat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- startServer(ctx, mcpServer, nil, serverOptions{
			transport: "sse",
			address:   "127.0.0.1:0",
		})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("startServer with 'sse' transport returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startServer did not shut down in time")
	}
}

// newTestPlatform creates a minimal platform for testing.
func newTestPlatform(t *testing.T, cfg *platform.Config) *platform.Platform {
	t.Helper()
	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	return p
}

// TestCloseServer_HandlesUnstartedPlatform asserts the shutdown path
// is safe when the platform was constructed but never started. This
// is the path tests and CLI sub-commands take when they only need a
// configured platform; closeServer must not panic and must complete
// in well under the K8s grace period budget.
func TestCloseServer_HandlesUnstartedPlatform(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Auth:   platform.AuthConfig{AllowAnonymous: true},
	})
	result := &serverResult{platform: p}

	done := make(chan struct{})
	go func() {
		defer close(done)
		closeServer(result)
	}()

	select {
	case <-done:
	case <-time.After(lifecycleStopTimeout + 5*time.Second):
		t.Fatal("closeServer did not return; Stop+Close must complete inside the timeout budget")
	}
}

// TestCloseServer_NilPlatformIsSafe asserts closeServer tolerates a
// serverResult whose platform field is nil (the stdio-bootstrap path
// when NewWithDefaults is used). Stop must not be invoked on a nil
// pointer and Close must be skipped.
func TestCloseServer_NilPlatformIsSafe(_ *testing.T) {
	result := &serverResult{platform: nil}
	// Must not panic.
	closeServer(result)
}

const (
	migrateTestFilePerms  = 0o600
	migrateTestInput      = "server:\n  name: test\n"
	migrateTestVersioned  = "apiVersion: v1\nserver:\n  name: test\n"
	migrateTestInputFile  = "input.yaml"
	migrateTestOutputFile = "output.yaml"
)

// writeMigrateInput creates a temp file with the given content and returns its path.
func writeMigrateInput(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, migrateTestInputFile)
	if err := os.WriteFile(p, []byte(content), migrateTestFilePerms); err != nil {
		t.Fatalf("writing input: %v", err)
	}
	return p
}

// readMigrateOutput reads the output file from a migrate test.
func readMigrateOutput(t *testing.T, path string) string {
	t.Helper()
	// #nosec G304 -- test helper reading from controlled temp dir
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	return string(out)
}

func TestRunMigrateConfig_FileToFile(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeMigrateInput(t, dir, migrateTestInput)
	outputPath := filepath.Join(dir, migrateTestOutputFile)

	err := runMigrateConfig([]string{"--config", inputPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("runMigrateConfig() error = %v", err)
	}

	out := readMigrateOutput(t, outputPath)
	if !strings.Contains(out, "apiVersion: v1") {
		t.Errorf("output missing apiVersion: v1, got:\n%s", out)
	}
	if !strings.Contains(out, "name: test") {
		t.Errorf("output missing original content, got:\n%s", out)
	}
}

func TestRunMigrateConfig_Idempotent(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeMigrateInput(t, dir, migrateTestVersioned)
	outputPath := filepath.Join(dir, migrateTestOutputFile)

	err := runMigrateConfig([]string{"--config", inputPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("runMigrateConfig() error = %v", err)
	}

	out := readMigrateOutput(t, outputPath)
	if out != migrateTestVersioned {
		t.Errorf("expected idempotent output, got:\n%s", out)
	}
}

func TestRunMigrateConfig_WithTargetVersion(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeMigrateInput(t, dir, migrateTestInput)
	outputPath := filepath.Join(dir, migrateTestOutputFile)

	err := runMigrateConfig([]string{
		"--config", inputPath,
		"--output", outputPath,
		"--target-version", "v1",
	})
	if err != nil {
		t.Fatalf("runMigrateConfig() error = %v", err)
	}

	out := readMigrateOutput(t, outputPath)
	if !strings.Contains(out, "apiVersion: v1") {
		t.Errorf("output missing apiVersion: v1, got:\n%s", out)
	}
}

func TestRunMigrateConfig_MissingInputFile(t *testing.T) {
	err := runMigrateConfig([]string{"--config", "/nonexistent/path.yaml"})
	if err == nil {
		t.Fatal("expected error for missing input file")
	}
	if !strings.Contains(err.Error(), "opening config") {
		t.Errorf("error = %q, want 'opening config'", err.Error())
	}
}

func TestRunMigrateConfig_BadOutputPath(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeMigrateInput(t, dir, migrateTestInput)

	err := runMigrateConfig([]string{
		"--config", inputPath,
		"--output", "/nonexistent/dir/output.yaml",
	})
	if err == nil {
		t.Fatal("expected error for bad output path")
	}
	if !strings.Contains(err.Error(), "creating output") {
		t.Errorf("error = %q, want 'creating output'", err.Error())
	}
}

func TestRunMigrateConfig_UnknownSourceVersion(t *testing.T) {
	dir := t.TempDir()
	inputPath := writeMigrateInput(t, dir, "apiVersion: v99\nserver:\n  name: test\n")

	err := runMigrateConfig([]string{"--config", inputPath})
	if err == nil {
		t.Fatal("expected error for unknown source version")
	}
	if !strings.Contains(err.Error(), "migrating config") {
		t.Errorf("error = %q, want 'migrating config'", err.Error())
	}
}
