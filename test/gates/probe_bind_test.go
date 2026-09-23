package gates

import (
	"net"
	"os/exec"
	"strconv"
	"testing"
)

// devStart is the script whose port probe these tests run. `make dev` refuses
// to start, or relocates a port, on the probe's answer, so a probe stricter
// than the listener it predicts fails a start that would have worked (#1841).
const devStart = "../../dev/start.sh"

// probeBind runs the real probe_bind function, cut out of dev/start.sh so the
// rest of the script (which starts the stack) never runs. It reports whether
// the probe called the port free.
func probeBind(t *testing.T, addr string, port int) bool {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "bash", "-c", //nolint:gosec // fixed script path; addr and port are this test's own
		`eval "$(sed -n '/^probe_bind() {/,/^}/p' "$1")" && probe_bind "$2" "$3"`,
		"probe", devStart, addr, strconv.Itoa(port))
	return cmd.Run() == nil
}

// plainBindFails reports whether a bind WITHOUT SO_REUSEADDR is refused, which
// is what the probe answered before #1841 and what proves the port is in the
// state under test.
func plainBindFails(t *testing.T, port int) bool {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", "-c", //nolint:gosec // fixed program; port is this test's own
		"import socket,sys; socket.socket().bind(('127.0.0.1', int(sys.argv[1])))",
		strconv.Itoa(port))
	return cmd.Run() != nil
}

// listen takes an ephemeral loopback port.
func listen(t *testing.T) (ln net.Listener, port int) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require(t, err)
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", ln.Addr())
	}
	return ln, addr.Port
}

func TestProbeBindCallsATimeWaitPortFree(t *testing.T) {
	ln, port := listen(t)
	client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
	require(t, err)
	server, err := ln.Accept()
	require(t, err)
	// The accepted side closes first, so the TIME_WAIT socket is the one bound
	// to the listening port, as a stack's server side is after `make dev-stop`.
	require(t, server.Close())
	require(t, client.Close())
	require(t, ln.Close())

	if !plainBindFails(t, port) {
		t.Fatalf("a plain bind of port %d succeeded, so the port is not in TIME_WAIT and the test proves nothing", port)
	}
	if !probeBind(t, "127.0.0.1", port) {
		t.Fatalf("probe_bind called port %d busy with only TIME_WAIT sockets on it; the stack's listeners bind it", port)
	}
}

func TestProbeBindCallsALiveListenerBusy(t *testing.T) {
	ln, port := listen(t)
	defer func() { _ = ln.Close() }()

	if probeBind(t, "127.0.0.1", port) {
		t.Fatalf("probe_bind called port %d free while a listener holds it", port)
	}
}
