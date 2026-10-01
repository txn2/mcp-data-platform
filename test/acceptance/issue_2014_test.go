//go:build integration

package acceptance

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #2014: a Trino connection with a password over plain HTTP is one the
// Trino client refuses to open. These criteria hold where that refusal lands:
// a configuration file declaring one is refused before the database is
// touched; the admin API refuses to save one; and one already saved in the
// database does not stop the platform from starting.
//
// The first and third criteria start this tree's own binary, because what they
// are about is what a process does at start, which the running dev stack has
// already done. The second goes through the dev stack's admin API.
//
// Wire forms: the admin route's body is JSON with a typed config object, sent
// as one; `port` is sent as a number and `ssl` as a boolean, the forms the
// connection editor sends. No MCP tool parameter is touched.

// platformProcess is one platform process this suite started from this tree.
type platformProcess struct {
	cmd  *exec.Cmd
	out  *syncBuffer
	base string
	done chan error
}

// syncBuffer is a bytes.Buffer two goroutines write and one reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // a bytes.Buffer write
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var (
	builtPlatformOnce sync.Once
	builtPlatform     string
	builtPlatformErr  error
)

// platformBinary builds cmd/mcp-data-platform from this tree once per run.
func platformBinary(t *testing.T) string {
	t.Helper()
	builtPlatformOnce.Do(func() {
		dir, err := os.MkdirTemp("", "acceptance-platform-")
		if err != nil {
			builtPlatformErr = err
			return
		}
		builtPlatform = filepath.Join(dir, "mcp-data-platform")
		cmd := exec.Command("go", "build", "-o", builtPlatform, "./cmd/mcp-data-platform") // #nosec G204 -- fixed arguments
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			builtPlatformErr = fmt.Errorf("go build: %w: %s", err, out)
		}
	})
	if builtPlatformErr != nil {
		t.Fatalf("building the platform: %v", builtPlatformErr)
	}
	return builtPlatform
}

// freeAddress is a loopback address nothing is listening on.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// startPlatform starts the binary on config, a YAML document whose server
// address is the %s it is given, and stops it when the test ends.
func startPlatform(t *testing.T, config string) *platformProcess {
	t.Helper()
	addr := freeAddress(t)
	path := filepath.Join(t.TempDir(), "platform.yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(config, addr)), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	p := &platformProcess{out: &syncBuffer{}, base: "http://" + addr, done: make(chan error, 1)}
	p.cmd = exec.Command(platformBinary(t), "--config", path, "--transport", "http", "--address", addr) // #nosec G204 -- the binary this suite built
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("starting the platform: %v", err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() { p.stop(t) })
	return p
}

// stop ends the process if it is still running.
func (p *platformProcess) stop(t *testing.T) {
	t.Helper()
	if p.cmd.ProcessState != nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// exited waits for the process to end on its own and returns its output.
func (p *platformProcess) exited(t *testing.T, within time.Duration) string {
	t.Helper()
	select {
	case err := <-p.done:
		if err == nil {
			t.Fatalf("the platform exited cleanly; want a refusal:\n%s", p.out.String())
		}
		return p.out.String()
	case <-time.After(within):
		t.Fatalf("the platform was still running after %s; want it to refuse to start:\n%s", within, p.out.String())
		return ""
	}
}

// serving waits until the process answers /healthz.
func (p *platformProcess) serving(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case err := <-p.done:
			t.Fatalf("the platform exited (%v) before it served:\n%s", err, p.out.String())
		default:
		}
		res, err := http.Get(p.base + "/healthz") //nolint:noctx // a loopback probe
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the platform did not serve within %s:\n%s", within, p.out.String())
}

// devPostgresPort is the dev stack's Postgres port.
func devPostgresPort() string {
	if v := os.Getenv("DEV_PG_PORT"); v != "" {
		return v
	}
	return "5432"
}

// devPSQL runs one statement in the dev stack's Postgres, in database db.
func devPSQL(t *testing.T, db, statement string) {
	t.Helper()
	out, err := exec.Command("docker", "exec", "acme-dev-postgres", // #nosec G204 -- the dev stack's own container
		"psql", "-v", "ON_ERROR_STOP=1", "-U", "platform", "-d", db, "-c", statement).CombinedOutput()
	if err != nil {
		t.Fatalf("psql %q: %v: %s", statement, err, out)
	}
}

const issue2014Config = `
server:
  name: acceptance-2014
  transport: http
  address: "%%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2014-key"
        name: "acceptance"
        roles: ["admin"]
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
database:
  dsn: "%s"
scripts:
  worker:
    enabled: false
notifications:
  enabled: false
thumbnails:
  enabled: false
toolkits:
  trino:
    enabled: true
    default: main
    instances:
      main:
        host: trino.example.com
        user: svc
        password: secret
        ssl: %s
`

// TestIssue2014_AConfigFileWithAPasswordOverPlainHTTPIsRefusedBeforeTheDatabase
// starts the platform on a file declaring the default Trino connection with a
// password and ssl: false, and a database nothing listens on. The refusal is
// the Trino connection's, naming it and the fix, so it came before the process
// opened the database, let alone migrated it. The same file with ssl: true
// passes the check and stops at the database instead.
func TestIssue2014_AConfigFileWithAPasswordOverPlainHTTPIsRefusedBeforeTheDatabase(t *testing.T) {
	nowhere := "postgres://platform:platform_secret@" + freeAddress(t) + "/none?sslmode=disable&connect_timeout=2"

	refused := startPlatform(t, fmt.Sprintf(issue2014Config, nowhere, "false")).exited(t, 2*time.Minute)
	for _, want := range []string{"before any database migration ran", `"main"`, "set ssl: true"} {
		if !strings.Contains(refused, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, refused)
		}
	}
	if strings.Contains(refused, "connecting to database") {
		t.Errorf("the process reached the database before refusing:\n%s", refused)
	}

	passed := startPlatform(t, fmt.Sprintf(issue2014Config, nowhere, "true")).exited(t, 2*time.Minute)
	if strings.Contains(passed, "set ssl: true") {
		t.Errorf("a password over TLS was refused:\n%s", passed)
	}
}

// TestIssue2014_TheAdminAPIRefusesToSaveAConnectionTheClientRefuses saves a
// Trino connection with a password over plain HTTP through the dev stack's
// admin API. The save is refused with the reason, and nothing is stored.
func TestIssue2014_TheAdminAPIRefusesToSaveAConnectionTheClientRefuses(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2014-%d", time.Now().UnixNano())
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/trino/"+name, http.NoBody) })

	status, body := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/trino/"+name, jsonBody(t, map[string]any{
		"config": map[string]any{
			"host": "trino.internal", "port": 8080, "user": "svc", "password": "secret", "ssl": false,
		},
		"description": "Acceptance #2014: a password over plain HTTP.",
	}))
	if status != http.StatusBadRequest {
		t.Fatalf("the save answered HTTP %d, want 400: %v", status, body)
	}
	if text := fmt.Sprint(body); !strings.Contains(text, "set ssl: true") || !strings.Contains(text, name) {
		t.Errorf("the refusal does not name the connection and the fix: %v", body)
	}
	if got, _ := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/trino/"+name, http.NoBody); got != http.StatusNotFound {
		t.Errorf("the refused connection was stored: GET answered HTTP %d", got)
	}
}

// TestIssue2014_AStoredConnectionTheClientRefusesDoesNotStopTheStart is the
// upgrade of a deployment whose database holds such a connection, saved before
// the client refused it: the platform starts on a fresh database of its own,
// the row is written the way an earlier release stored it, and the platform
// restarts on it. It serves, and says which stored connection it left out.
func TestIssue2014_AStoredConnectionTheClientRefusesDoesNotStopTheStart(t *testing.T) {
	db := fmt.Sprintf("acceptance_2014_%d", time.Now().UnixNano())
	devPSQL(t, "mcp_platform", "CREATE DATABASE "+db)
	t.Cleanup(func() { devPSQL(t, "mcp_platform", "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
	dsn := "postgres://platform:platform_secret@localhost:" + devPostgresPort() + "/" + db + "?sslmode=disable"
	config := fmt.Sprintf(issue2014Config, dsn, "true")

	first := startPlatform(t, config)
	first.serving(t, 3*time.Minute)
	first.stop(t)

	devPSQL(t, db, `INSERT INTO connection_instances (kind, name, config, created_by) VALUES ('trino', 'stored2014', `+
		`'{"host": "trino.internal", "port": 8080, "user": "svc", "password": "secret", "ssl": false}', 'acceptance')`)

	second := startPlatform(t, config)
	second.serving(t, 3*time.Minute)
	out := second.out.String()
	for _, want := range []string{"a stored connection its client refuses was not loaded", "stored2014", "set ssl: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("the start does not say %q:\n%s", want, out)
		}
	}
}
