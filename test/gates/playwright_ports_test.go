package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// devPortLoop is the line of dev/start.sh that checks every port the stack is
// about to take; its numeric words are the ports it takes at a fixed number.
var devPortLoop = regexp.MustCompile(`(?m)^for port in (.*); do$`)

// playwrightDefault is a Playwright config's port default: an environment
// variable read with a numeric fallback, as `process.env["E2E_PORT"] ?? 5273`.
var playwrightDefault = regexp.MustCompile(`process\.env\["([A-Z0-9_]+)"\]\s*\?\?\s*"?(\d+)"?`)

// devFixedPorts returns the ports dev/start.sh takes at a fixed number. The
// relocatable ones are shell variables there and are not listed.
func devFixedPorts(t *testing.T) map[int]bool {
	t.Helper()
	raw, err := os.ReadFile(devStart)
	require(t, err)
	m := devPortLoop.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s has no `for port in ...; do` line; this gate reads the stack's ports from it", devStart)
	}
	ports := map[int]bool{}
	for word := range strings.FieldsSeq(string(m[1])) {
		if n, err := strconv.Atoi(word); err == nil {
			ports[n] = true
		}
	}
	if !ports[5173] {
		t.Fatalf("the ports read from %s do not include Vite's 5173: %v", devStart, ports)
	}
	return ports
}

// TestPlaywrightDefaultsAvoidDevPorts pins #2523: no Playwright suite under
// ui/e2e defaults to a port `make dev` takes. A suite that reuses whatever
// server is on its port and defaults to the dev stack's Vite binds to the live
// backend, so make verify could not run beside a stack, while a diff under
// dev/ needs one for dev-smoke-diff.
func TestPlaywrightDefaultsAvoidDevPorts(t *testing.T) {
	taken := devFixedPorts(t)
	configs, err := filepath.Glob("../../ui/e2e/*/playwright.config.ts")
	require(t, err)
	if len(configs) == 0 {
		t.Fatal("no ui/e2e/*/playwright.config.ts found")
	}
	checked := 0
	for _, path := range configs {
		raw, err := os.ReadFile(path) //nolint:gosec // a path globbed from this repository
		require(t, err)
		for _, m := range playwrightDefault.FindAllStringSubmatch(string(raw), -1) {
			port, _ := strconv.Atoi(m[2])
			if port < 1024 || port > 65535 {
				continue
			}
			checked++
			if taken[port] {
				t.Errorf("%s defaults %s to %d, a port dev/start.sh takes; pick one `make dev` never uses", path, m[1], port)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Playwright config states a port default; the gate read nothing")
	}
}

// TestDevFixedPortsReadsTheLoop: the reader finds the fixed ports in the loop
// and leaves the relocatable shell variables out.
func TestDevFixedPortsReadsTheLoop(t *testing.T) {
	ports := devFixedPorts(t)
	for _, want := range []int{5173, 9090, 9091} {
		if !ports[want] {
			t.Errorf("port %d missing from %v", want, ports)
		}
	}
	if m := playwrightDefault.FindStringSubmatch(`const PORT = Number(process.env["E2E_PORT"] ?? 5173);`); len(m) < 3 || m[2] != "5173" {
		t.Errorf("the default pattern does not read a config's default: %v", m)
	}
}
