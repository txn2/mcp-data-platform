//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Issue #2476: an application import wrote each script in a bundle straight
// to the script store, past the save gate every other source save crosses.
// Each script is now put through the importing deployment's gate (format,
// lint, limits, tests, coverage) as the importing administrator before
// anything is written, and the recorded runs its tests replay travel in the
// bundle so the importing deployment runs the same tests.
//
// The source is #2412's: the dev stack's application of an endpoint script
// and a state-keeping script, exported; the target is a second platform
// process on a database of its own.
//
// Wire forms: manage_application import takes bundle as a string (a
// managed-resource reference) and map as an object; the portal import route
// takes bundle as a JSON object and map as an object. Each refusal and the
// passing import are sent through one of the two, and both are exercised.

// bundle2476 is a bundle decoded loosely, so a criterion can change one
// script and write it back with every other field as it was.
type bundle2476 map[string]any

func decode2476(t *testing.T, raw []byte) bundle2476 {
	t.Helper()
	var b bundle2476
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("the bundle is not JSON: %v", err)
	}
	return b
}

// script returns the bundle's script whose name begins with prefix.
func (b bundle2476) script(t *testing.T, prefix string) map[string]any {
	t.Helper()
	scripts, _ := b["scripts"].([]any)
	for _, s := range scripts {
		m, _ := s.(map[string]any)
		if name, _ := m["name"].(string); strings.HasPrefix(name, prefix) {
			return m
		}
	}
	t.Fatalf("the bundle holds no script named %s*", prefix)
	return nil
}

func (b bundle2476) encode(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// nothingWritten2476 fails when the second deployment holds the application,
// a script of the bundle, or a migration of its schema.
func nothingWritten2476(t *testing.T, second *second2412, src *source2412, bundle bundle2476) {
	t.Helper()
	if res, text, err := second.admin.callRaw("manage_application", map[string]any{"command": "get", "application": src.app}); err == nil && !res.IsError {
		t.Errorf("the refused import wrote application %s: %s", src.app, firstRunes(text, 300))
	}
	scripts, _ := bundle["scripts"].([]any)
	for _, s := range scripts {
		name, _ := s.(map[string]any)["name"].(string)
		if res, _, err := second.admin.callRaw("manage_script", map[string]any{"command": "get", "name": name}); err == nil && !res.IsError {
			t.Errorf("the refused import wrote script %s", name)
		}
	}
	status := second.admin.call("manage_schema", map[string]any{"command": "status", "connection": second.conn, "schema": src.schema})
	if pending, _ := status["pending"].([]any); len(pending) != 0 {
		t.Errorf("the refused import stored migrations: %v", status)
	}
}

// importMCP2476 uploads the bundle into the second deployment's library and
// imports it by reference through manage_application.
func importMCP2476(t *testing.T, second *second2412, bundle []byte, mapping map[string]string) (ok bool, text string) {
	t.Helper()
	id, _ := uploadResource1568(t, second.admin, "bundle-2476-"+unique1579()+".json", "application/json", bundle)
	res, text, err := second.admin.callRaw("manage_application", map[string]any{
		"command": "import", "bundle": "mcp:resource:" + id, "map": mapping,
	})
	if err != nil {
		t.Fatalf("manage_application import: %v", err)
	}
	return !res.IsError, text
}

// TestIssue2476_AScriptTheTargetsLintRefusesIsRefusedBeforeAnythingIsWritten:
// the state-keeping script's main loses its docstring in the bundle, which
// the target's lint refuses; the import through manage_application is
// refused naming the script and the rule, and the target holds no part, no
// migration and no release of it.
func TestIssue2476_AScriptTheTargetsLintRefusesIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	src := newSource2412(t)
	_, raw := src.export(t)
	bundle := decode2476(t, raw)
	keeper := bundle.script(t, "acc-2412-state")
	source, _ := keeper["source"].(string)
	stripped := strings.Replace(source, "    \"\"\"Keeps the token it is given in the script's state.\"\"\"\n", "", 1)
	if stripped == source {
		t.Fatalf("the keeper's docstring is not in its source: %s", source)
	}
	keeper["source"] = stripped
	second := startSecond2412(t)
	mapping := map[string]string{src.conn: second.conn, src.secret: src.secret}

	ok, text := importMCP2476(t, second, bundle.encode(t), mapping)
	name, _ := keeper["name"].(string)
	if ok || !strings.Contains(text, "script "+name+" is refused") || !strings.Contains(text, "missing-docstring") {
		t.Fatalf("want a refusal naming %s and missing-docstring, got ok=%v %s", name, ok, text)
	}
	nothingWritten2476(t, second, src, bundle)
}

// TestIssue2476_AScriptWhoseTestsFailOnTheTargetIsRefused: the keeper's main
// reports kept False where its test, replaying the run carried in the
// bundle, asserts what the run produced; the import through the portal
// route is refused naming the script and the failing test, and nothing is
// written.
func TestIssue2476_AScriptWhoseTestsFailOnTheTargetIsRefused(t *testing.T) {
	src := newSource2412(t)
	_, raw := src.export(t)
	bundle := decode2476(t, raw)
	keeper := bundle.script(t, "acc-2412-state")
	source, _ := keeper["source"].(string)
	changed := strings.Replace(source, `{"kept": True,`, `{"kept": False,`, 1)
	if changed == source {
		t.Fatalf("the keeper's result is not in its source: %s", source)
	}
	keeper["source"] = changed
	second := startSecond2412(t)

	status, out := importOn(t, second, bundle.encode(t), map[string]string{src.conn: second.conn, src.secret: src.secret})
	name, _ := keeper["name"].(string)
	detail, _ := out["detail"].(string)
	if status != http.StatusUnprocessableEntity || !strings.Contains(detail, "script "+name+" is refused") || !strings.Contains(detail, "a test failed") {
		t.Fatalf("want a 422 naming %s and the failing test, got %d %v", name, status, out)
	}
	nothingWritten2476(t, second, src, bundle)
}

// TestIssue2476_ABundleThatPassesImportsWithItsRecordings: the bundle as
// exported imports through manage_application and releases, each script's
// tests passing on the target against the recordings the bundle carries, and
// each of those recordings is held on the target afterwards, kept for the
// script the import wrote.
func TestIssue2476_ABundleThatPassesImportsWithItsRecordings(t *testing.T) {
	src := newSource2412(t)
	_, raw := src.export(t)
	bundle := decode2476(t, raw)
	var runIDs []string
	scripts, _ := bundle["scripts"].([]any)
	for _, s := range scripts {
		recs, _ := s.(map[string]any)["recordings"].([]any)
		if len(recs) == 0 {
			t.Errorf("script %v carries no recording of the runs its tests replay", s.(map[string]any)["name"])
		}
		for _, r := range recs {
			id, _ := r.(map[string]any)["run_id"].(string)
			runIDs = append(runIDs, id)
		}
	}
	second := startSecond2412(t)

	ok, text := importMCP2476(t, second, raw, map[string]string{src.conn: second.conn, src.secret: src.secret})
	if !ok {
		t.Fatalf("the import was refused: %s", text)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	if rel, _ := out["release"].(map[string]any); rel["action"] != "import" {
		t.Fatalf("the import did not release: %s", firstRunes(text, 600))
	}
	for _, id := range runIDs {
		got := second.admin.call("manage_script", map[string]any{"command": "recording", "run_id": id})
		meta, _ := got["recording"].(map[string]any)
		if meta["run_id"] != id || meta["kept"] != true || meta["script_id"] == "" {
			t.Errorf("recording %s is not held and kept for the imported script on the target: %v", id, got)
		}
	}
}
