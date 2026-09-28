//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1941: a library is a script with no main(), pure code another script
// loads by name and pinned version. These criteria save libraries and the
// scripts that load them through manage_script and run them through
// run_script on the running platform.
//
// Wire forms: manage_script's command, name, description, source,
// change_summary and user_agreed, and run_script's name, are typed (strings,
// and a boolean), and run_script's wait_seconds an integer, so each admits one
// JSON form and is sent as a literal tools/call parameter of it.

// issue1941Library is a library whose one function multiplies by factor, with
// the test a save requires.
func issue1941Library(factor int) string {
	return fmt.Sprintf(`"""Scales numbers."""

def scale(n):
    """Multiplies n by the library's factor."""
    return n * %[1]d

def test_scale():
    """Scales one."""
    assert.eq(scale(1), %[1]d)
`, factor)
}

// issue1941Caller loads version of library and hands back scale(21).
func issue1941Caller(library string, version int) string {
	return fmt.Sprintf(`load("lib:%s@%d", "scale")

def main():
    """Hands back 21 scaled by the library it loads."""
    platform.result({"scaled": scale(21)})
`, library, version)
}

func issue1941Stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// issue1941CreateLibrary saves a library through manage_script and deletes it
// when the test ends, after the scripts that load it.
func issue1941CreateLibrary(t *testing.T, c *client, name, source string) map[string]any {
	t.Helper()
	out := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source, "description": "Acceptance #1941: a library.",
	})
	if out["status"] != "created" || out["library"] != true {
		t.Fatalf("the library %s was not saved as a library: %v", name, out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return out
}

// issue1941Run runs a saved script and returns what it handed back.
func issue1941Run(t *testing.T, c *client, name string) string {
	t.Helper()
	ran := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if ran["status"] != "succeeded" {
		t.Fatalf("run_script %s: %v", name, ran)
	}
	result, _ := ran["result"].(map[string]any)
	return fmt.Sprint(result["scaled"])
}

// TestIssue1941_ALibraryLoadedAtItsVersionRuns is criterion 1: a library
// saved with a function and a test, loaded at @1 by a script, runs through
// run_script and produces the expected output.
func TestIssue1941_ALibraryLoadedAtItsVersionRuns(t *testing.T) {
	c := connect(t)
	stamp := issue1941Stamp()
	lib := "acc-1941-scaler-" + stamp
	created := issue1941CreateLibrary(t, c, lib, issue1941Library(2))
	if next, _ := created["next"].(string); !strings.Contains(next, `load("lib:`+lib+`@1"`) {
		t.Errorf("the save does not say how the library is loaded: %q", next)
	}

	name := "acc-1941-caller-" + stamp
	c.saveScript(map[string]any{
		"command": "create", "name": name, "source": issue1941Caller(lib, 1),
		"description": "Acceptance #1941: a script loading a library.",
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	if got := issue1941Run(t, c, name); got != "42" {
		t.Fatalf("the script handed back %s; want 42 (21 scaled by 2)", got)
	}

	report := c.call("manage_script", map[string]any{"command": "validate", "source": issue1941Caller(lib, 1)})
	if !strings.Contains(fmt.Sprint(report["libraries"]), lib) {
		t.Errorf("validate does not report the library it loads: %v", report["libraries"])
	}
	refused, text, err := c.callRaw("run_script", map[string]any{"name": lib, "wait_seconds": 5})
	if err != nil || !refused.IsError || !strings.Contains(text, "is a library") {
		t.Errorf("run_script ran a library: %v %s", err, text)
	}
}

// TestIssue1941_AScriptKeepsTheVersionItPins is criterion 2: saving version 2
// of the library with a changed function does not change what the script
// pinned to @1 produces; repinning to @2 does.
func TestIssue1941_AScriptKeepsTheVersionItPins(t *testing.T) {
	c := connect(t)
	stamp := issue1941Stamp()
	lib := "acc-1941-pinned-" + stamp
	issue1941CreateLibrary(t, c, lib, issue1941Library(2))
	name := "acc-1941-pinning-" + stamp
	c.saveScript(map[string]any{
		"command": "create", "name": name, "source": issue1941Caller(lib, 1),
		"description": "Acceptance #1941: a script pinned to a library version.",
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	updated := c.call("manage_script", map[string]any{
		"command": "update", "name": lib, "source": issue1941Library(3),
		"change_summary": "Scales by 3 instead of 2.", "user_agreed": true,
	})
	if updated["status"] != "updated" {
		t.Fatalf("version 2 of the library was not saved: %v", updated)
	}
	if got := issue1941Run(t, c, name); got != "42" {
		t.Fatalf("pinned to @1, the script handed back %s after the library's version 2; want 42", got)
	}

	c.saveEdit(map[string]any{"command": "update", "name": name, "source": issue1941Caller(lib, 2)}, nil)
	if got := issue1941Run(t, c, name); got != "63" {
		t.Fatalf("repinned to @2, the script handed back %s; want 63 (21 scaled by 3)", got)
	}
}

// TestIssue1941_ALibraryNamingPlatformIsRefusedNamingTheLine is criterion 3.
func TestIssue1941_ALibraryNamingPlatformIsRefusedNamingTheLine(t *testing.T) {
	c := connect(t)
	source := `"""Reads orders."""

def orders():
    """Reads every order."""
    return platform.query("SELECT 1")["rows"]
`
	refused := c.call("manage_script", map[string]any{
		"command": "create", "name": "acc-1941-effect-" + issue1941Stamp(), "source": source,
		"description": "Acceptance #1941.",
	})
	if refused["status"] != "invalid" {
		t.Fatalf("a library naming platform saved: %v", refused)
	}
	if !hasFinding1944(findings1944(refused), "library-effect", 5) {
		t.Errorf("no library-effect finding on line 5: %v", refused["findings"])
	}
}

// TestIssue1941_ALoadOfAMissingLibraryOrVersionIsRefused is criterion 4.
func TestIssue1941_ALoadOfAMissingLibraryOrVersionIsRefused(t *testing.T) {
	c := connect(t)
	stamp := issue1941Stamp()
	lib := "acc-1941-present-" + stamp
	issue1941CreateLibrary(t, c, lib, issue1941Library(2))
	for _, module := range []string{"lib:" + lib + "@2", "lib:acc-1941-absent-" + stamp + "@1"} {
		source := strings.Replace(issue1941Caller(lib, 1), "lib:"+lib+"@1", module, 1)
		out, text, err := c.callRaw("manage_script", map[string]any{
			"command": "create", "name": "acc-1941-missing-" + issue1941Stamp(), "source": source,
			"description": "Acceptance #1941.",
		})
		if err != nil {
			t.Fatalf("%s: %v", module, err)
		}
		if !strings.Contains(text, module+" does not exist") || strings.Contains(text, `"status":"created"`) {
			t.Errorf("%s: the save was not refused naming the load: isError=%v %s", module, out.IsError, text)
		}
	}
}

// TestIssue1941_ALoadCycleIsRefused is criterion 5. A load names a saved
// version, and a saved version never changes, so the save that would close a
// loop between two libraries names a version that does not exist yet: B@1
// loads A@1, and A's next version loading B@2 -- the version that would load
// it back -- is refused. A library loading itself is refused as the cycle it
// is.
func TestIssue1941_ALoadCycleIsRefused(t *testing.T) {
	c := connect(t)
	stamp := issue1941Stamp()
	a := "acc-1941-cycle-a-" + stamp
	b := "acc-1941-cycle-b-" + stamp
	issue1941CreateLibrary(t, c, a, issue1941Library(2))
	issue1941CreateLibrary(t, c, b, fmt.Sprintf(`load("lib:%s@1", "scale")

def twice(n):
    """Scales n twice."""
    return scale(scale(n))

def test_twice():
    """Scales one twice."""
    assert.eq(twice(1), 4)
`, a))
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": b}) })

	closing := fmt.Sprintf(`load("lib:%s@2", "twice")

def scale(n):
    """Multiplies n by two."""
    return n * 2

def test_scale():
    """Scales one."""
    assert.eq(scale(1), 2)
    assert.eq(twice(1), 4)
`, b)
	_, text, err := c.callRaw("manage_script", map[string]any{
		"command": "update", "name": a, "source": closing, "change_summary": "Loads b.", "user_agreed": true,
	})
	if err != nil || !strings.Contains(text, "lib:"+b+"@2 does not exist") {
		t.Errorf("the save closing the loop was not refused: %v %s", err, text)
	}

	self := strings.Replace(closing, "lib:"+b+"@2", "lib:"+a+"@1", 1)
	_, text, err = c.callRaw("manage_script", map[string]any{
		"command": "update", "name": a, "source": self, "change_summary": "Loads itself.", "user_agreed": true,
	})
	if err != nil || !strings.Contains(text, "a library cannot load itself") {
		t.Errorf("a library loading itself was not refused: %v %s", err, text)
	}
}
