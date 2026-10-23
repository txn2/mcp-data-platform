package gates

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// devStartFunc runs one function cut out of dev/start.sh, so the rest of the
// script (which starts the stack) never runs, and returns what it printed.
func devStartFunc(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	script := `eval "$(sed -n '/^` + name + `() {/,/^}/p' "$1")" && shift && ` + name + ` "$@"`
	cmd := exec.CommandContext(t.Context(), "bash", append([]string{"-c", script, "bash", devStart}, args...)...) //nolint:gosec // fixed script path; the arguments are this test's own
	out, err := cmd.Output()
	return string(out), err
}

// The managed-resource store's readiness waited while curl's status read
// exactly 000, and a fallback after curl made a failed request read 000000,
// so a store that never answered was reported ready (#2264). http_status
// prints the three digits whatever happened.
func TestHTTPStatusIsThreeDigitsWhateverTheAnswer(t *testing.T) {
	answering := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer answering.Close()
	got, err := devStartFunc(t, "http_status", answering.URL+"/")
	if err != nil || got != "403" {
		t.Fatalf("an S3 store refusing an anonymous GET: %q, %v; want 403", got, err)
	}

	dead, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := dead.Addr().String()
	_ = dead.Close()
	got, err = devStartFunc(t, "http_status", "http://"+addr+"/")
	if err != nil || got != "000" {
		t.Fatalf("a closed port: %q, %v; want 000", got, err)
	}
}

// A listener that accepts and never completes TLS (the store with a key it
// cannot read) is given up on rather than waited on forever.
func TestHTTPStatusGivesUpOnASilentListener(t *testing.T) {
	silent, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close() //nolint:errcheck // test listener
	held := make(chan net.Conn, 4)
	t.Cleanup(func() {
		for {
			select {
			case c := <-held:
				_ = c.Close()
			default:
				return
			}
		}
	})
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			held <- c // held open, never answered, until the test ends
		}
	}()
	start := time.Now()
	got, err := devStartFunc(t, "http_status", "https://"+silent.Addr().String()+"/")
	if err != nil || got != "000" {
		t.Fatalf("a silent listener: %q, %v; want 000", got, err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Fatalf("a silent listener held the probe %v", took)
	}
}

// openssl writes the store's key mode 600, and the store's image reads it as
// another user, so on Linux the HTTPS listener could not load it (#2264).
func TestStoreKeyIsReadableByTheStore(t *testing.T) {
	key := filepath.Join(t.TempDir(), "private.key")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := devStartFunc(t, "store_key_readable", key); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o044 != 0o044 {
		t.Fatalf("the store's key is %v; its group and others must read it", info.Mode().Perm())
	}

	src, err := os.ReadFile(devStart)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "\nstore_key_readable dev/.tls/seaweedfs/private.key\n") {
		t.Fatal("dev/start.sh no longer makes the store's key readable on every start")
	}
}

// No status check in dev/start.sh falls back after curl, which writes 000
// itself: the fallback is what made a dead store read as ready (#2264).
func TestDevStartHasNoCurlStatusFallback(t *testing.T) {
	src, err := os.ReadFile(devStart)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, `|| echo "000"`) {
			t.Errorf("dev/start.sh:%d appends a fallback to curl's status: %s", i+1, strings.TrimSpace(line))
		}
	}
}
