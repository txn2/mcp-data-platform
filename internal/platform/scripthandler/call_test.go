package scripthandler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// policySource defines every tool-call kind's entry, so one script serves as
// any of them.
const policySource = "def decide(call):\n    return platform.allow()\n\n" +
	"def transform(call, result):\n    return result\n\ndef main():\n    pass\n"

// TestSetCall_AnAdministratorAddsAndChangesAPolicy: an administrator adds a
// policy, approving its version, then changes only what they send.
func TestSetCall_AnAdministratorAddsAndChangesAPolicy(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	svc := w.service()

	out, err := svc.Set(ctx, SetRequest{
		Caller: admin, Script: sc, Kind: KindPolicy, Approve: true, Args: map[string]any{"limit": 3},
		Call: callhandler.Patch{Connection: new("billing-api"), Operations: new([]string{"addSubscribers"}), Users: new([]string{"Jo@Example.com"})},
	})
	require.NoError(t, err)
	id, _ := out[fieldHandler].(string)
	assert.Equal(t, "billing-api", out["connection"])
	assert.Equal(t, []string{"jo@example.com"}, out["users"], "people are matched by address, case folded")
	assert.Equal(t, callhandler.FailHold, out["on_failure"], "a policy holds its call when it fails, by default")
	assert.Equal(t, map[string]any{"limit": 3}, out["args"])
	assert.Equal(t, 1, out["running_version"], "approve approved the latest version as the administrator")
	assert.Equal(t, admin.Email, out["approved_by"])
	assert.Contains(t, out, "recent_failures", "an administrator reads the failures")

	changed, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Handler: id, Call: callhandler.Patch{OnFailure: new(callhandler.FailDeny)}})
	require.NoError(t, err)
	assert.Equal(t, callhandler.FailDeny, changed["on_failure"])
	assert.Equal(t, []string{"addSubscribers"}, changed["operations"], "what was not sent is kept")

	got, err := svc.Get(ctx, id, jane)
	require.NoError(t, err)
	assert.NotContains(t, got, "recent_failures", "the owner, who is not an administrator, reads no failure")
}

// TestSetCall_OnlyAnAdministrator: someone other than an administrator, the
// script's owner included, cannot add, change, turn off or delete a
// tool-call handler.
func TestSetCall_OnlyAnAdministrator(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	svc := w.service()
	for _, kind := range []string{KindPolicy, KindTransform, KindWatch} {
		_, err := svc.Set(ctx, SetRequest{Caller: jane, Script: sc, Kind: kind, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
		require.ErrorIs(t, err, ErrNotAllowed, kind)
		assert.Contains(t, err.Error(), "an administrator", kind)
	}
	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.NoError(t, err)
	id, _ := out[fieldHandler].(string)
	_, err = svc.Enable(ctx, id, false, jane)
	require.ErrorIs(t, err, ErrNotAllowed)
	_, err = svc.Delete(ctx, id, jane)
	require.ErrorIs(t, err, ErrNotAllowed)
	_, err = svc.Failures(ctx, id, jane)
	require.ErrorIs(t, err, ErrNotFound, "another person's failures are not there to name")

	off, err := svc.Enable(ctx, id, false, admin)
	require.NoError(t, err)
	assert.Equal(t, false, off["enabled"])
	gone, err := svc.Delete(ctx, id, admin)
	require.NoError(t, err)
	assert.Equal(t, true, gone["deleted"])
}

// TestSetCall_Refusals: a handler its kind could not run, one another script
// holds, past the most a script may hold, and a store that fails.
func TestSetCall_Refusals(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	other := w.add("s2", "other", "jane@example.com", policySource)
	svc := w.service()

	_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy})
	require.ErrorIs(t, err, ErrInvalid, "a policy names its connection")
	_, err = svc.Set(ctx, SetRequest{
		Caller: admin, Script: sc, Kind: KindTransform,
		Call: callhandler.Patch{Connection: new("c"), Everyone: new(true), OnFailure: new(callhandler.FailOriginal), Protected: new([]string{"body.ssn"})},
	})
	require.ErrorIs(t, err, ErrInvalid, "return-original is refused with protected fields")
	assert.Contains(t, err.Error(), "protect")

	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: other, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.NoError(t, err)
	otherID, _ := out[fieldHandler].(string)
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Handler: otherID})
	require.ErrorIs(t, err, ErrNotFound, "a handler of another script is not this script's")

	for range MaxPerKind {
		_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindWatch, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
		require.NoError(t, err)
	}
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindWatch, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.ErrorIs(t, err, ErrInvalid, "past the most of a kind")

	w.calls.err = errors.New("db down")
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.ErrorIs(t, err, ErrStore)
	w.calls.err = nil

	w.versions.err = errors.New("approvals down")
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Approve: true, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.ErrorIs(t, err, ErrStore)

	svc.Calls = nil
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.ErrorIs(t, err, ErrInvalid, "a deployment that keeps no tool-call handlers")
}

// TestFailures: an administrator reads a tool-call handler's failures; a
// handler of another kind keeps none here.
func TestFailures(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	svc := w.service()
	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.NoError(t, err)
	id, _ := out[fieldHandler].(string)
	w.calls.failures = map[string][]callhandler.Failure{id: {{HandlerID: id, Error: "timed out"}}}
	got, err := svc.Failures(ctx, id, admin)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "timed out", got[0].Error)

	sched, err := svc.Set(ctx, SetRequest{Caller: admin, Script: w.add("s2", "daily", "jane@example.com", mainSource), Kind: KindSchedule, Cron: "0 9 * * *", Timezone: "UTC"})
	require.NoError(t, err)
	schedID, _ := sched[fieldHandler].(string)
	_, err = svc.Failures(ctx, schedID, admin)
	require.ErrorIs(t, err, ErrInvalid)
}

// TestSetCall_TheScriptDefinesTheKindsEntry: a script whose version the
// handler would run defines no entry of its kind is refused, naming the
// version and what the kind calls; with approve the latest is what runs, and
// without it the version already approved (#2650).
func TestSetCall_TheScriptDefinesTheKindsEntry(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "mailer", "jane@example.com", mainSource)
	svc := w.service()
	for _, kind := range []string{KindPolicy, KindTransform} {
		_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: kind, Approve: true, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
		require.ErrorIs(t, err, ErrInvalid, kind)
		assert.Contains(t, err.Error(), "version 1 of script mailer cannot be a "+kind, kind)
		assert.Contains(t, err.Error(), callhandler.Contract(kind), kind)
	}
	_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindWatch, Approve: true, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.NoError(t, err, "a watch runs main()")

	// The latest defines decide, but without approve the approved version
	// 1, which does not, is what would run.
	sc.Source, sc.Version = policySource, 2
	w.versions.sources = map[string]map[int]string{sc.ID: {1: mainSource}}
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "version 1 of script mailer")
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Approve: true, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.NoError(t, err, "approving the latest, which defines decide")
}

// TestSetCall_EveryoneIsChosen: a new handler naming no persona and no person
// is refused unless everyone is set; everyone with a list is a
// contradiction; a change that leaves who alone keeps an existing handler's
// everyone, and one that empties a list without everyone is refused (#2650).
func TestSetCall_EveryoneIsChosen(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	svc := w.service()
	_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Personas: new([]string{})}})
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "set everyone to true")
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true), Users: new([]string{"jo@example.com"})}})
	require.ErrorIs(t, err, ErrInvalid, "everyone and a person")

	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c"), Everyone: new(true)}})
	require.NoError(t, err)
	assert.Equal(t, true, out["everyone"])
	id, _ := out[fieldHandler].(string)
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Handler: id, Call: callhandler.Patch{TimeoutMS: new(500)}})
	require.NoError(t, err, "a change that leaves who alone")

	named, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Handler: id, Call: callhandler.Patch{Personas: new([]string{"finance"})}})
	require.NoError(t, err)
	assert.Equal(t, false, named["everyone"])
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Handler: id, Call: callhandler.Patch{Personas: new([]string{})}})
	require.ErrorIs(t, err, ErrInvalid, "emptying the list without everyone")
	back, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Handler: id, Call: callhandler.Patch{Everyone: new(true)}})
	require.NoError(t, err)
	assert.Equal(t, true, back["everyone"])
	assert.Empty(t, back["personas"])
}

// TestSetCall_TheNameCallsCarry: a handler naming a connection stored under
// another name its calls carry is refused naming that name, and the
// connection's page and test resolve either name to it (#2649).
func TestSetCall_TheNameCallsCarry(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	svc := w.service()
	svc.CallNames = func(name string) (string, bool) {
		if name == "mcp-test-server" {
			return "mcptest", true
		}
		return name, false
	}
	_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("mcp-test-server"), Everyone: new(true)}})
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), `name "mcptest"`)
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("mcptest"), Everyone: new(true)}})
	require.NoError(t, err)
	assert.Equal(t, "mcptest", svc.CallName("mcp-test-server"))
	assert.Equal(t, "billing", svc.CallName("billing"))
	for _, name := range []string{"mcp-test-server", "mcptest"} {
		list, err := svc.ConnectionHandlers(ctx, name, admin)
		require.NoError(t, err)
		assert.Len(t, list, 1, name)
	}
	assert.Equal(t, "x", (*Service)(nil).CallName("x"))
}

// TestHandlerScripts: the dialog is offered the scripts that run whose latest
// version defines the kind's entry, each with the version approved and
// whether it defines it too; anyone else is refused (#2650).
func TestHandlerScripts(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("s1", "limit", "jane@example.com", policySource)
	w.add("s2", "mailer", "jane@example.com", mainSource)
	old := w.add("s3", "older", "jane@example.com", policySource)
	old.Version = 2
	svc := w.service()
	w.versions.approved = map[string]int{"s3": 1}
	w.versions.sources = map[string]map[int]string{"s3": {1: mainSource}}

	got, err := svc.HandlerScripts(ctx, KindPolicy, "", admin)
	require.NoError(t, err)
	assert.Equal(t, "decide(call)", got.Entry)
	names := map[string]HandlerScriptChoice{}
	for _, c := range got.Scripts {
		names[c.Name] = c
	}
	assert.Contains(t, names, "limit")
	assert.NotContains(t, names, "mailer", "a script with no decide(call) is not offered")
	assert.Equal(t, 1, names["older"].Running)
	assert.False(t, names["older"].RunningDefines, "the approved version defines no decide(call)")
	assert.Equal(t, 3, got.Looked)

	watches, err := svc.HandlerScripts(ctx, KindWatch, "", admin)
	require.NoError(t, err)
	assert.Len(t, watches.Scripts, 3, "every script here defines main()")

	_, err = svc.HandlerScripts(ctx, KindPolicy, "", jane)
	require.ErrorIs(t, err, ErrNotAllowed)
	_, err = svc.HandlerScripts(ctx, KindSchedule, "", admin)
	require.ErrorIs(t, err, ErrInvalid)
}

// TestCallNamesOver: the resolver every construction of the service sets
// reads the live toolkits, and none takes every name as its own (#2649).
func TestCallNamesOver(t *testing.T) {
	assert.Nil(t, CallNamesOver(nil))
	names := CallNamesOver(kitList{})
	require.NotNil(t, names)
	bound, stored := names("billing")
	assert.Equal(t, "billing", bound)
	assert.False(t, stored)
}

type kitList struct{}

func (kitList) All() []registry.Toolkit { return nil }
