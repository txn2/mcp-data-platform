package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pullImages runs dev/start.sh's pull_images against a docker that fails its
// first `failures` calls, with no wait between attempts, and returns how many
// times docker was called and whether the step succeeded.
func pullImages(t *testing.T, failures int) (calls int, ok bool) {
	t.Helper()
	bin := t.TempDir()
	count := filepath.Join(bin, "calls")
	fake := "#!/bin/sh\nn=$(cat " + count + " 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > " + count +
		"\n[ $n -gt " + strconv.Itoa(failures) + " ] && exit 0\necho 'Error response from daemon: received unexpected HTTP status: 503 Service Unavailable' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil { //nolint:gosec // an executable stub in this test's own directory
		t.Fatal(err)
	}
	script := `eval "$(sed -n '/^pull_images() {/,/^}/p' "$1")" && pull_images`
	cmd := exec.CommandContext(t.Context(), "bash", "-c", script, "bash", devStart) //nolint:gosec // fixed script path
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "DEV_PULL_RETRY_DELAY=0")
	err := cmd.Run()
	b, _ := os.ReadFile(count) //nolint:gosec // the counter file in this test's own directory
	calls, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	return calls, err == nil
}

// A registry answering 503 to one pull failed a whole nightly group before a
// criterion ran (#2278): the pull is retried, and fails only after five.
func TestDevStartRetriesAFailedImagePull(t *testing.T) {
	if calls, ok := pullImages(t, 2); !ok || calls != 3 {
		t.Errorf("two failed pulls then one that works: ok=%v after %d calls; want ok after 3", ok, calls)
	}
	if calls, ok := pullImages(t, 5); ok || calls != 5 {
		t.Errorf("every pull failing: ok=%v after %d calls; want a failure after 5", ok, calls)
	}
}

// The pull runs before any compose service is started.
func TestDevStartPullsBeforeStarting(t *testing.T) {
	src, err := os.ReadFile(devStart)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	pull := strings.Index(s, "\npull_images || fail")
	up := strings.Index(s, "docker compose -f dev/docker-compose.yml up -d")
	if pull < 0 || up < 0 || pull > up {
		t.Fatalf("dev/start.sh must call pull_images before its first `docker compose up` (pull at %d, up at %d)", pull, up)
	}
}
