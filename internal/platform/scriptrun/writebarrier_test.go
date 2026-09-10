package scriptrun

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/toolwrite"
)

// declaringCaller is a recordingCaller that also answers what the server
// advertises about a tool, which is how a proxied tool reaches the barrier.
type declaringCaller struct {
	recordingCaller
	// declared maps a tool name to its advertised read-only annotation. A tool
	// absent from the map is not advertised at all.
	declared map[string]bool
	asked    []string
}

func (c *declaringCaller) DeclaresReadOnly(_ context.Context, name string) (readOnly, known bool) {
	c.asked = append(c.asked, name)
	readOnly, known = c.declared[name]
	return readOnly, known
}

// barred runs source with the write barrier on.
func barred(t *testing.T, source string, caller Caller) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "run_1",
		FireTime: fireTime, Caller: caller, Writes: WritesRefused,
	})
}

// TestWriteBarrier_RefusesADeclaredWrite is the defect #1664 reports: a draft
// executed manage_resource create for real, so the rehearsal left a resource
// behind.
func TestWriteBarrier_RefusesADeclaredWrite(t *testing.T) {
	caller := &recordingCaller{}
	result, err := barred(t, `
platform.call("manage_resource", {"action": "create", "filename": "daily.csv"})
print("landed")
`, caller)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "manage_resource action=create persists outside this run")
	assert.Contains(t, err.Error(), "allow_writes")
	assert.Empty(t, caller.calls, "the tool is never called: the refusal is before the call, not after it")
	require.NotNil(t, result)
	assert.NotContains(t, result.Log, "landed", "the run stops at the refusal")
	require.NotNil(t, result.RefusedWrite)
	assert.Equal(t, "manage_resource", result.RefusedWrite.Tool)
	assert.Equal(t, "manage_resource action=create", result.RefusedWrite.Call)
	assert.Empty(t, result.Writes)
}

// TestWriteBarrier_AdmitsAReadThroughCall keeps the barrier from turning a
// draft into a run that can do nothing: the read half of an action tool is not
// a write.
func TestWriteBarrier_AdmitsAReadThroughCall(t *testing.T) {
	caller := &recordingCaller{}
	result, err := barred(t, `
res = platform.call("manage_table", {"action": "list", "reference": "mcp:resource:x"})
print("read %d" % res["row_count"])
`, caller)

	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, "manage_table", caller.calls[0].name)
	assert.Equal(t, "read 1\n", result.Log)
	assert.Empty(t, result.Writes, "a read is not recorded as a write")
	assert.Nil(t, result.RefusedWrite)
}

// TestWriteBarrier_RefusesAnUnclassifiedTool is the deny-by-default rule: the
// platform does not guess "read" about a tool nobody classified.
func TestWriteBarrier_RefusesAnUnclassifiedTool(t *testing.T) {
	caller := &recordingCaller{}
	_, err := barred(t, `platform.call("vendor__create_invoice", {"amount": 10})`, caller)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot tell whether vendor__create_invoice persists")
	assert.Empty(t, caller.calls)
}

// TestWriteBarrier_TakesAProxiedToolsOwnDeclaration covers the tools the
// platform did not define: a gateway connection's upstream names them and
// annotates them, and its ReadOnlyHint is the only statement anyone has made.
func TestWriteBarrier_TakesAProxiedToolsOwnDeclaration(t *testing.T) {
	caller := &declaringCaller{declared: map[string]bool{
		"vendor__list_contacts":  true,
		"vendor__create_invoice": false,
	}}

	_, err := barred(t, `platform.call("vendor__list_contacts", {})`, caller)
	require.NoError(t, err, "an upstream that says the tool reads is taken at its word")
	assert.Equal(t, []string{"vendor__list_contacts"}, caller.asked)

	_, err = barred(t, `platform.call("vendor__create_invoice", {})`, &declaringCaller{
		declared: map[string]bool{"vendor__create_invoice": false},
	})
	require.Error(t, err, "an upstream that declares nothing leaves the call a write")
}

// TestWriteBarrier_DeclarationCannotOverruleARule pins that the annotation is
// consulted only where no rule exists. A tool the platform classifies is
// classified by the platform, whatever it advertises about itself.
func TestWriteBarrier_DeclarationCannotOverruleARule(t *testing.T) {
	caller := &declaringCaller{declared: map[string]bool{"trino_execute": true}}

	_, err := barred(t, `platform.call("trino_execute", {"connection": "w", "sql": "DROP TABLE t"})`, caller)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "trino_execute persists outside this run")
	assert.Empty(t, caller.asked, "a classified tool is never asked what it thinks of itself")
}

// TestWriteBarrier_ClassifierReadsAnOperationID covers the addressing
// api_discover hands an author: without the lookup a read-only pull is refused,
// with it the call goes through.
func TestWriteBarrier_ClassifierReadsAnOperationID(t *testing.T) {
	source := `platform.call("api_invoke_endpoint", {"connection": "crm", "operation_id": "listContacts"})`

	caller := &recordingCaller{}
	_, err := barred(t, source, caller)
	require.Error(t, err, "with nothing to resolve the id, the platform cannot tell")

	resolving := &recordingCaller{}
	_, err = Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "run_1", FireTime: fireTime,
		Caller: resolving, Writes: WritesRefused,
		Classifier: toolwrite.Classifier{ResolveMethod: func(string, string, string) (string, bool) {
			return "GET", true
		}},
	})
	require.NoError(t, err)
	require.Len(t, resolving.calls, 1)
}

// TestWriteBarrier_OffRecordsEveryWrite is the account a draft with the barrier
// lifted owes its author: those calls landed, and nothing else in the response
// says so.
func TestWriteBarrier_OffRecordsEveryWrite(t *testing.T) {
	caller := &recordingCaller{}
	result, err := Run(context.Background(), Options{
		Source: `
platform.call("manage_resource", {"action": "create", "filename": "daily.csv"})
platform.call("manage_table", {"action": "list"})
platform.call("manage_table", {"action": "register", "name": "daily"})
`,
		Name: "test", RunID: "run_1", FireTime: fireTime, Caller: caller,
		Writes: WritesReported,
	})

	require.NoError(t, err)
	require.Len(t, caller.calls, 3, "every call is made")
	require.Len(t, result.Writes, 2, "and the two that persist are recorded, in call order")
	assert.Equal(t, WriteRecord{Tool: "manage_resource", Call: "manage_resource action=create"}, result.Writes[0])
	assert.Equal(t, WriteRecord{Tool: "manage_table", Call: "manage_table action=register"}, result.Writes[1])
	assert.Nil(t, result.RefusedWrite)
}

// TestWriteBarrier_NamedHelpersAreUnaffected pins that the barrier is about
// platform.call alone. The three helpers preview on their own, and a barred run
// still exercises them.
func TestWriteBarrier_NamedHelpersAreUnaffected(t *testing.T) {
	caller := &recordingCaller{}
	result, err := barred(t, `
res = platform.query(connection="primary", sql="SELECT region FROM t")
platform.export("daily", res["rows"], "csv")
platform.save_state({"cursor": "2026-08-13"})
`, caller)

	require.NoError(t, err)
	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview, "the export still previews rather than writing")
	require.NotNil(t, result.State)
	assert.Empty(t, result.Writes, "and none of it is a persisting platform.call")
}

// TestWriteBarrier_ARefusedToolIsNotRecordedAsAWrite pins that the account a
// draft gives is of what happened, not of what was attempted. A tool that
// refused the arguments persisted nothing, and listing it under "these calls
// persisted for real" would be a false statement about the deployment.
func TestWriteBarrier_ARefusedToolIsNotRecordedAsAWrite(t *testing.T) {
	caller := &recordingCaller{err: errors.New("path is required")}
	result, err := Run(context.Background(), Options{
		Source: `platform.call("manage_resource", {"action": "create"})`,
		Name:   "test", RunID: "run_1", FireTime: fireTime, Caller: caller,
		Writes: WritesReported,
	})

	require.Error(t, err, "the tool's own refusal fails the run")
	require.NotNil(t, result)
	assert.Empty(t, result.Writes, "a call that did not return did not persist")
}

// TestWriteBarrier_APlatformRunRecordsNothing pins the third state. A run makes
// every write and nobody reads a list of them, so accumulating one would be
// memory the engine otherwise bounds, spent on a slice nothing reads.
func TestWriteBarrier_APlatformRunRecordsNothing(t *testing.T) {
	caller := &recordingCaller{}
	result, err := Run(context.Background(), Options{
		Source: `
platform.call("manage_resource", {"action": "create", "filename": "daily.csv"})
platform.call("trino_execute", {"connection": "w", "sql": "DELETE FROM t"})
`,
		Name: "test", RunID: "run_1", FireTime: fireTime, Caller: caller,
	})

	require.NoError(t, err)
	assert.Len(t, caller.calls, 2, "a platform run makes every call")
	assert.Empty(t, result.Writes)
	assert.Nil(t, result.RefusedWrite)
}

// TestWriteBarrier_AsksTheServerOncePerTool pins the cache. The lookup walks a
// tool listing, and the answer cannot change inside a run, so a script calling
// one unclassified tool in a loop must not walk it once per iteration.
func TestWriteBarrier_AsksTheServerOncePerTool(t *testing.T) {
	caller := &declaringCaller{declared: map[string]bool{"vendor__list_contacts": true}}
	_, err := barred(t, `
for i in range(4):
    platform.call("vendor__list_contacts", {"page": i})
`, caller)

	require.NoError(t, err)
	assert.Len(t, caller.calls, 4, "every call is made")
	assert.Equal(t, []string{"vendor__list_contacts"}, caller.asked, "and the server is asked once")
}

// TestWriteBarrier_APlatformRunAsksNothing pins the hot path: classifying on a
// run that makes every call and records none would put a tool listing behind
// every call to an unclassified tool, unattended.
func TestWriteBarrier_APlatformRunAsksNothing(t *testing.T) {
	caller := &declaringCaller{declared: map[string]bool{"vendor__create_invoice": false}}
	_, err := Run(context.Background(), Options{
		Source: `platform.call("vendor__create_invoice", {"amount": 10})`,
		Name:   "test", RunID: "run_1", FireTime: fireTime, Caller: caller,
	})

	require.NoError(t, err)
	assert.Len(t, caller.calls, 1)
	assert.Empty(t, caller.asked)
}
