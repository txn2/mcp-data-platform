package scripthandler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
)

const policySource = "def decide(call):\n    return platform.allow()\n"

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
		_, err := svc.Set(ctx, SetRequest{Caller: jane, Script: sc, Kind: kind, Call: callhandler.Patch{Connection: new("c")}})
		require.ErrorIs(t, err, ErrNotAllowed, kind)
		assert.Contains(t, err.Error(), "an administrator", kind)
	}
	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c")}})
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
		Call: callhandler.Patch{Connection: new("c"), OnFailure: new(callhandler.FailOriginal), Protected: new([]string{"body.ssn"})},
	})
	require.ErrorIs(t, err, ErrInvalid, "return-original is refused with protected fields")
	assert.Contains(t, err.Error(), "protect")

	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: other, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c")}})
	require.NoError(t, err)
	otherID, _ := out[fieldHandler].(string)
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Handler: otherID})
	require.ErrorIs(t, err, ErrNotFound, "a handler of another script is not this script's")

	for range MaxPerKind {
		_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindWatch, Call: callhandler.Patch{Connection: new("c")}})
		require.NoError(t, err)
	}
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindWatch, Call: callhandler.Patch{Connection: new("c")}})
	require.ErrorIs(t, err, ErrInvalid, "past the most of a kind")

	w.calls.err = errors.New("db down")
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c")}})
	require.ErrorIs(t, err, ErrStore)
	w.calls.err = nil

	w.versions.err = errors.New("approvals down")
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Approve: true, Call: callhandler.Patch{Connection: new("c")}})
	require.ErrorIs(t, err, ErrStore)

	svc.Calls = nil
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c")}})
	require.ErrorIs(t, err, ErrInvalid, "a deployment that keeps no tool-call handlers")
}

// TestFailures: an administrator reads a tool-call handler's failures; a
// handler of another kind keeps none here.
func TestFailures(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "limit", "jane@example.com", policySource)
	svc := w.service()
	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: sc, Kind: KindPolicy, Call: callhandler.Patch{Connection: new("c")}})
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
