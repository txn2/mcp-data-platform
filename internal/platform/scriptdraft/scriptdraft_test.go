package scriptdraft

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
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
	outcome, err := New(server(t), nil).Run(context.Background(), Request{
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
	outcome, err := New(server(t), nil).Run(context.Background(), Request{
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
	outcome, err := New(server(t), nil).Run(context.Background(), Request{
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
	runner := New(server(t), nil)
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
	_, err := New(server(t), nil).Run(context.Background(), Request{
		Source: "x = 1\n", Name: "anonymous",
	})

	require.ErrorIs(t, err, ErrNoIdentity)
}

// TestRun_RefusesWhenThereIsNoServerToRunAgainst is the honest shape for a
// deployment that cannot execute anything.
func TestRun_RefusesWhenThereIsNoServerToRunAgainst(t *testing.T) {
	_, err := New(nil, nil).Run(context.Background(), Request{
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
	runner := New(server(t), nil)
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
	runner := New(server(t), nil)

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

// recordingExports is a composition root's writer factory, recording the
// drafts it was asked to serve.
type recordingExports struct {
	targets []Target
	written []scriptrun.ExportRequest
}

func (r *recordingExports) exports(t Target) scriptrun.Exporter {
	r.targets = append(r.targets, t)
	return r
}

func (r *recordingExports) Export(_ context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	r.written = append(r.written, req)
	return &scriptrun.ExportResult{ResourceRef: "mcp:resource:r1", ResourceID: "r1", Bytes: 1}, nil
}

func (*recordingExports) PublishData(context.Context, scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	return &scriptrun.ExportResult{}, nil
}

// TestRun_ExportsThroughTheWriterOnlyWhenAllowed is #1822: a draft allowed to
// write persists its exports through the writer the composition root gave the
// runner, under the caller's identity and the draft's own run id, and a draft
// that was not allowed, or that carries no script record, previews.
func TestRun_ExportsThroughTheWriterOnlyWhenAllowed(t *testing.T) {
	source := `out = platform.export(name="o", rows=[{"a": 1}], format="jsonl", destination="resources", key="s/o.jsonl")
print("preview", out["preview"])
`
	sc := &script.Script{ID: "s1", Name: "staging"}
	for _, tt := range []struct {
		name        string
		allow       bool
		script      *script.Script
		wantWritten bool
	}{
		{"allowed", true, sc, true},
		{"not allowed", false, sc, false},
		{"no script record", true, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer := &recordingExports{}
			outcome, err := New(server(t), nil).WithExports(writer.exports).Run(context.Background(), Request{
				Source: source, Name: "staging", Script: tt.script, Identity: jane, AllowWrites: tt.allow,
			})
			require.NoError(t, err)
			require.False(t, outcome.Failed(), "%v", outcome.Err)
			assert.Equal(t, tt.allow, outcome.AllowWrites)
			if !tt.wantWritten {
				assert.Empty(t, writer.targets)
				assert.Contains(t, outcome.Result.Log, "preview True")
				return
			}
			require.Len(t, writer.targets, 1)
			assert.Equal(t, sc, writer.targets[0].Script)
			assert.Equal(t, outcome.RunID, writer.targets[0].RunID)
			assert.Equal(t, jane, writer.targets[0].Identity)
			require.Len(t, writer.written, 1)
			assert.Contains(t, outcome.Result.Log, "preview False")
		})
	}
}

func TestWithExports_OnANilRunner(t *testing.T) {
	var runner *Runner
	assert.Nil(t, runner.WithExports(nil))
	assert.Nil(t, runner.WithMemoryBudget(1))
}

// TestRun_HoldsADraftToTheMemoryBudget holds #1861 on the draft path: a draft
// runs on a serving replica and meets the budget a platform run does, and
// reports the peak it reached.
func TestRun_HoldsADraftToTheMemoryBudget(t *testing.T) {
	outcome, err := New(server(t), nil).WithMemoryBudget(64<<10).Run(context.Background(), Request{
		Source: "held = [\"x\" * 1024 + str(i) for i in range(200)]\nplatform.call(\"echo\", {})\n",
		Name:   "big", Identity: jane,
	})
	require.NoError(t, err)
	require.True(t, outcome.Failed())
	assert.Contains(t, outcome.Err.Error(), "64 KiB memory budget")
	assert.Positive(t, outcome.Result.PeakMemory)
}
