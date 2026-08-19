package scriptdraft

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// A draft run introduces no authority: it is the caller's own session, and the
// only thing this package decides is whose identity goes on it. These tests are
// about that decision and about the two refusals that come before it.

// jane is the person every admitted draft here runs as.
var jane = Identity{
	UserID: "u1", Email: "jane@example.com",
	Roles: []string{"dp_analyst"}, AuthType: middleware.AuthTypeOIDC,
}

// server assembles an MCP server with one tool, so a draft has something real
// to connect to and the session handshake is exercised rather than stubbed.
func server(t *testing.T) *mcp.Server {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "noop", Description: "does nothing"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	return s
}

// TestRun_ExecutesTheSourceAndReportsIt is the ordinary case: real interpreter,
// real result, no persistence.
func TestRun_ExecutesTheSourceAndReportsIt(t *testing.T) {
	outcome, err := New(server(t)).Run(context.Background(), Request{
		Source: "print(\"hello \" + run.params[\"who\"])\n",
		Name:   "greeter", Params: map[string]any{"who": "world"},
		Identity: jane,
	})

	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.False(t, outcome.Failed())
	require.NotNil(t, outcome.Result)
	assert.Contains(t, outcome.Result.Log, "hello world")
}

// TestRun_MintsARunIDThatIsAlsoTheSessionID is what makes a run one thing in
// audit rather than one row per platform call.
func TestRun_MintsARunIDThatIsAlsoTheSessionID(t *testing.T) {
	outcome, err := New(server(t)).Run(context.Background(), Request{
		Source: "x = 1\n", Name: "trivial", Identity: jane,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, outcome.RunID)
	assert.True(t, strings.HasPrefix(outcome.RunID, pkgsession.ScriptSessionPrefix),
		"the run id is the session id the audit rows carry")
}

// TestRun_CarriesAFailureRatherThanReturningIt keeps the log, which is the
// whole reason to have run a draft at all.
func TestRun_CarriesAFailureRatherThanReturningIt(t *testing.T) {
	outcome, err := New(server(t)).Run(context.Background(), Request{
		Source: "print(\"before\")\nfail(\"deliberate\")\n", Name: "boom", Identity: jane,
	})

	require.NoError(t, err, "a script failure is not the platform's failure")
	require.NotNil(t, outcome)
	assert.True(t, outcome.Failed())
	assert.Contains(t, outcome.Err.Error(), "deliberate")
	require.NotNil(t, outcome.Result)
	assert.Contains(t, outcome.Result.Log, "before")
}

// TestRun_PinsTheFireTimeSoADraftNeverReadsAClock is the determinism contract:
// what an author verifies in the loop is what a scheduled run will do.
func TestRun_PinsTheFireTimeSoADraftNeverReadsAClock(t *testing.T) {
	runner := New(server(t))
	pinned := time.Date(2026, 8, 18, 7, 0, 0, 0, time.UTC)
	runner.now = func() time.Time { return pinned }

	outcome, err := runner.Run(context.Background(), Request{
		Source: "print(run.fire_time)\n", Name: "clock", Identity: jane,
	})

	require.NoError(t, err)
	require.NotNil(t, outcome.Result)
	assert.Contains(t, outcome.Result.Log, "2026-08-18")
}

// TestRun_RefusesARequestWithNobodyToRunAs is the structural half of the
// no-new-authority property: a draft has no identity of its own to fall back
// to, so a request carrying none cannot execute.
func TestRun_RefusesARequestWithNobodyToRunAs(t *testing.T) {
	_, err := New(server(t)).Run(context.Background(), Request{
		Source: "x = 1\n", Name: "anonymous",
	})

	require.ErrorIs(t, err, ErrNoIdentity)
}

// TestRun_RefusesWhenThereIsNoServerToRunAgainst is the honest shape for a
// deployment that cannot execute anything.
func TestRun_RefusesWhenThereIsNoServerToRunAgainst(t *testing.T) {
	_, err := New(nil).Run(context.Background(), Request{
		Source: "x = 1\n", Name: "nowhere", Identity: jane,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

// TestRun_OnANilRunnerRefuses covers the composition root handing a nil through
// rather than panicking per request.
func TestRun_OnANilRunnerRefuses(t *testing.T) {
	var runner *Runner
	_, err := runner.Run(context.Background(), Request{Source: "x = 1\n", Identity: jane})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

func TestOutcome_FailedOnNothing(t *testing.T) {
	var none *Outcome
	assert.False(t, none.Failed())
}

// TestRun_BoundsConcurrentDrafts is the one lever that bounds the memory a
// pathological draft can reach: an interpreter's heap cannot be capped, so the
// number running at once is what is capped instead.
func TestRun_BoundsConcurrentDrafts(t *testing.T) {
	runner := New(server(t))
	// Fill every slot, so the next request has nowhere to run.
	for range maxConcurrentDrafts {
		runner.slots <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The wait is real time, so the request gives up on the CALLER's context
	// rather than holding the test for it. That is the same path a browser tab
	// closing takes.
	cancel()
	_, err := runner.Run(ctx, Request{Source: "x = 1\n", Name: "queued", Identity: jane})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestRun_ReleasesItsSlot keeps a failed run from leaking the slot it took,
// which would shrink the bound by one on every failure until nothing could run.
func TestRun_ReleasesItsSlot(t *testing.T) {
	runner := New(server(t))

	for range maxConcurrentDrafts + 2 {
		outcome, err := runner.Run(context.Background(), Request{
			Source: "fail(\"deliberate\")\n", Name: "boom", Identity: jane,
		})
		require.NoError(t, err)
		assert.True(t, outcome.Failed())
	}
	assert.Empty(t, runner.slots, "every slot must be back")
}

func TestErrBusy_IsASentinelASurfaceCanMatch(t *testing.T) {
	assert.ErrorIs(t, ErrBusy, ErrBusy)
	assert.Contains(t, ErrBusy.Error(), "try again")
}
