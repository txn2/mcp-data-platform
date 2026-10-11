//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2080: platform.export to "resources" names its library with scope=
// and scope_id=, held to the resource rule against the identity the run
// presents, at save and at the write.
//
// Wire forms: manage_script's command, name, description and source are typed
// strings and run_script's name a typed string and wait_seconds a typed
// integer, one form each, sent as literal tools/call params. scope and
// scope_id are platform.export keyword arguments inside the script source, so
// their form is the Starlark string literal the save requires; a computed one
// is asserted to be refused.

const issue2080Folder = "acceptance/issue-2080"

// issue2080Source pages two rows into one file in the global library with a
// table registered over it, writes a second file there, and records where the
// second landed. The appended output comes first, which is the order the
// run's record and testing.outputs() must agree on: the saved test is a replay
// that compares them.
const issue2080Source = `
def page(units):
    """Appends one page of rows to the shared file in the global library."""
    platform.export(
        name = "Acceptance 2080 shared",
        rows = [{"store_id": 1, "units": units}],
        format = "csv",
        destination = "resources",
        key = "acceptance/issue-2080/%s",
        scope = "global",
        register = {"connection": "acme-scratch-resources", "table_name": "acc_2080_%s"},
        append = True,
    )

def main():
    """Pages two rows into one file in the global library, writes a second, and records where the second landed."""
    page(10)
    page(20)
    out = platform.export(
        name = "Acceptance 2080 whole",
        rows = [{"store_id": 2}],
        format = "csv",
        destination = "resources",
        key = "acceptance/issue-2080/whole-%s",
        scope = "global",
    )
    platform.save_state({
        "runs": str(int(run.state.get("runs", "0")) + 1),
        "scope": out["scope"],
        "resource_id": out["resource_id"],
        "uri": out["uri"],
        "version": str(out["version"]),
    })
`

// TestIssue2080_AnExportNamedIntoTheGlobalLibraryLandsThereAndVersions: an
// administrator's script files its output in the global library; the record
// says so; the same key and scope next run are the next version of the same
// file, with append=True and register= as without.
func TestIssue2080_AnExportNamedIntoTheGlobalLibraryLandsThereAndVersions(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-2080-" + stamp
	paged, whole := "paged-"+stamp+".csv", stamp+".csv"
	c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #2080: an output filed in the global library.",
		"source":      fmt.Sprintf(issue2080Source, paged, stamp, whole),
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	runOnce := func() map[string]any {
		t.Helper()
		run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
		if run["status"] != "succeeded" {
			t.Fatalf("the run did not succeed: %v", run)
		}
		got := c.call("manage_script", map[string]any{"command": "state", "name": name, "state_action": "get"})
		state, _ := got["state"].(map[string]any)
		if state == nil {
			t.Fatalf("the run recorded no state: %v", got)
		}
		outputs, _ := run["outputs"].([]any)
		for _, o := range outputs {
			out, _ := o.(map[string]any)
			if out["key"] == issue2080Folder+"/"+paged && out["scope"] != "global" {
				t.Errorf("the run recorded the paged output in %v; want the global library: %v", out["scope"], out)
			}
		}
		return state
	}

	first := runOnce()
	if first["scope"] != "global" {
		t.Fatalf("the record names scope %v; want global: %v", first["scope"], first)
	}
	uri, _ := first["uri"].(string)
	if uri != "mcp://global/"+issue2080Folder+"/whole-"+whole {
		t.Errorf("uri = %q; want the file at the key in the global library", uri)
	}
	second := runOnce()
	if second["resource_id"] != first["resource_id"] || second["uri"] != uri {
		t.Fatalf("the next run wrote a different file: %v then %v", first, second)
	}
	var v1, v2 int
	_, _ = fmt.Sscan(fmt.Sprint(first["version"]), &v1)
	_, _ = fmt.Sscan(fmt.Sprint(second["version"]), &v2)
	if v2 != v1+1 {
		t.Errorf("versions %d then %d; want the next version of the same file", v1, v2)
	}

	// The paged file, appended and registered, is in the global library too,
	// and is one file across runs.
	got := c.call("manage_resource", map[string]any{"action": "list", "scope": "global", "path": issue2080Folder})
	files := map[string]int{}
	listed, _ := got["resources"].([]any)
	for _, r := range listed {
		m, _ := r.(map[string]any)
		name, _ := m["filename"].(string)
		files[name]++
	}
	if files[paged] != 1 || files["whole-"+whole] != 1 {
		t.Errorf("the global library under %s holds %d of the paged file and %d of the whole one; want one each: %v",
			issue2080Folder, files[paged], files["whole-"+whole], got)
	}
}

// TestIssue2080_ASaveNamingALibraryItsAuthorCannotWriteIsRefused: a person
// who is no platform administrator cannot save a script that files in the
// global library; the refusal names the library.
func TestIssue2080_ASaveNamingALibraryItsAuthorCannotWriteIsRefused(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := fmt.Sprintf("acc-2080-refused-%d", time.Now().UnixNano())
	res, text, err := owner.callRaw("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #2080: a save its author may not make.",
		"source": `def main():
    """Files a file in the global library."""
    platform.export(name = "x", rows = [{"a": 1}], format = "csv", destination = "resources", key = "acceptance/issue-2080/x.csv", scope = "global")
`,
	})
	if err != nil {
		t.Fatalf("manage_script create: %v", err)
	}
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	if !res.IsError && !strings.Contains(text, "not saved") {
		t.Fatalf("the save went through: %s", text)
	}
	if !strings.Contains(text, "the global library") || !strings.Contains(text, devOwnerEmail) {
		t.Errorf("the refusal does not name the library and the author: %s", text)
	}
}

// TestIssue2080_ValidateReportsTheLibraryAndRefusesAComputedScope: validate
// lists the library in the reach, and a scope the source computes is refused.
func TestIssue2080_ValidateReportsTheLibraryAndRefusesAComputedScope(t *testing.T) {
	c := connect(t)
	out := c.call("manage_script", map[string]any{
		"command": "validate",
		"source": `def main():
    """Files a file in the finance persona's library."""
    platform.export(name = "x", rows = [{"a": 1}], format = "csv", destination = "resources", key = "a/x.csv", scope = "persona", scope_id = "finance")
`,
	})
	libraries, _ := out["resource_libraries"].([]any)
	if len(libraries) != 1 || fmt.Sprint(libraries[0]) != "map[scope:persona scope_id:finance]" {
		t.Errorf("resource_libraries = %v; want the finance persona library", out["resource_libraries"])
	}

	computed := c.call("manage_script", map[string]any{
		"command": "validate",
		"source": `def main():
    """Files a file in a library it computes."""
    where = "glo" + "bal"
    platform.export(name = "x", rows = [{"a": 1}], format = "csv", destination = "resources", key = "a/x.csv", scope = where)
`,
	})
	if computed["ok"] != false || !strings.Contains(fmt.Sprint(computed["findings"]), "written out as strings") {
		t.Errorf("a computed scope was not refused: %v", computed)
	}
}
