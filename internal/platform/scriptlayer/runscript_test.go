package scriptlayer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// stubRuns is a run store whose every method can be made to fail, for the tool
// paths that have nothing to do with a worker actually running anything.
type stubRuns struct {
	*memRuns
	enqueueErr error
	getErr     error
	listErr    error
}

func newStubRuns() *stubRuns { return &stubRuns{memRuns: newMemRuns()} }

func (s *stubRuns) Enqueue(ctx context.Context, r *script.Run) error {
	if s.enqueueErr != nil {
		return s.enqueueErr
	}
	return s.memRuns.Enqueue(ctx, r)
}

func (s *stubRuns) GetRun(ctx context.Context, id string) (*script.Run, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.memRuns.GetRun(ctx, id)
}

func (s *stubRuns) ListRuns(ctx context.Context, filter script.RunFilter) ([]script.Run, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.memRuns.ListRuns(ctx, filter)
}

// runnableHandle returns a handle over an in-memory store holding one personal
// script of jane's, approved with the full capability set.
func runnableHandle(t *testing.T) (*Handle, *memStore, *stubRuns) {
	t.Helper()
	store, runs := newMemStore(), newStubRuns()
	h := New(Config{Store: store, Runs: runs, AdminPersona: "admin"})

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "print(1)\n",
	})
	require.False(t, res.IsError, resultText(res))
	sc, err := store.GetPersonal(context.Background(), "jane@example.com", "daily")
	require.NoError(t, err)
	_, err = store.ApproveVersion(context.Background(), sc.ID, sc.Version, "admin@example.com",
		script.Grants{Capabilities: script.Capabilities, Destinations: []script.Destination{script.PortalDestination()}})
	require.NoError(t, err)
	return h, store, runs
}

// unapprovedHandle returns a handle holding one script nothing has approved.
//
// The script is GLOBAL and written by an administrator, because that is what an
// unapproved script now is: a personal script its own owner wrote is approved on
// save (#1367), so a personal fixture would no longer be testing the gate at
// all — it would be testing the path around it.
func unapprovedHandle(t *testing.T) *Handle {
	t.Helper()
	h := New(Config{Store: newMemStore(), Runs: newStubRuns(), AdminPersona: "admin"})

	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", Source: "print(1)\n", Scope: script.ScopeGlobal,
	})
	require.False(t, res.IsError, resultText(res))
	return h
}

// runScriptCall invokes the run_script handler directly.
func runScriptCall(t *testing.T, h *Handle, input runScriptInput) map[string]any {
	t.Helper()
	res, _, err := h.handleRunScript(authorCtx(), input)
	require.NoError(t, err)
	if res.IsError {
		return map[string]any{"error": resultText(res)}
	}
	return resultFields(t, res)
}

// TestRunScript_RefusesWhatMustNotExecute covers the states run_script must
// turn away before anything is queued.
func TestRunScript_RefusesWhatMustNotExecute(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*script.Script)
		wantErr string
	}{
		{"disabled", func(sc *script.Script) { sc.Enabled = false }, "disabled"},
		{"superseded", func(sc *script.Script) {
			sc.Status, sc.SupersededBy = script.StatusSuperseded, "daily-v2"
		}, "superseded"},
		{"deprecated", func(sc *script.Script) { sc.Status = script.StatusDeprecated }, "deprecated"},
		{
			"approval pointer dangles", func(sc *script.Script) { sc.ApprovedVersionID = "sver_missing" },
			"must be approved again",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store, _ := runnableHandle(t)
			sc, err := store.GetPersonal(context.Background(), "jane@example.com", "daily")
			require.NoError(t, err)
			tt.mutate(sc)
			require.NoError(t, store.Update(context.Background(), sc))

			out := runScriptCall(t, h, runScriptInput{Name: "daily"})
			assert.Contains(t, out["error"], tt.wantErr)
		})
	}
}

func TestRunScript_UnknownScript(t *testing.T) {
	h, _, _ := runnableHandle(t)
	out := runScriptCall(t, h, runScriptInput{Name: "nope"})
	assert.Contains(t, out["error"], "not found")
}

// TestRunScript_ParamsAreCheckedAgainstTheApprovedContract pins that binding
// uses the approved version's parameters, not the live row's.
func TestRunScript_ParamsAreCheckedAgainstTheApprovedContract(t *testing.T) {
	h, _, _ := runnableHandle(t)
	out := runScriptCall(t, h, runScriptInput{Name: "daily", Args: map[string]any{"nope": 1}})
	assert.Contains(t, out["error"], `has no parameter "nope"`)
}

func TestRunScript_EnqueueFailure(t *testing.T) {
	h, _, runs := runnableHandle(t)
	runs.enqueueErr = errors.New("boom")
	out := runScriptCall(t, h, runScriptInput{Name: "daily"})
	assert.Contains(t, out["error"], "failed to queue")
}

// TestRunScript_WaitEndsWithoutAWorker covers the bounded window with nothing
// draining the queue: the caller gets the run id and the way to follow it.
func TestRunScript_WaitEndsWithoutAWorker(t *testing.T) {
	h, _, _ := runnableHandle(t)
	out := runScriptCall(t, h, runScriptInput{Name: "daily", WaitSeconds: -1})
	assert.Equal(t, script.RunStatusPending, out[fieldStatus])
	assert.Contains(t, out["message"], "get_run")
}

// TestRunScript_WaitSurvivesAFailedRead pins that a read failing mid-wait says
// nothing about the run, which a worker elsewhere is still executing.
func TestRunScript_WaitSurvivesAFailedRead(t *testing.T) {
	h, _, runs := runnableHandle(t)
	runs.getErr = errors.New("boom")
	out := runScriptCall(t, h, runScriptInput{Name: "daily", WaitSeconds: 1})
	assert.Equal(t, script.RunStatusPending, out[fieldStatus])
	assert.NotEmpty(t, out["run_id"])
}

// TestRunScript_CancelledCallerLeavesTheRunGoing covers a client hanging up
// mid-wait.
func TestRunScript_CancelledCallerLeavesTheRunGoing(t *testing.T) {
	h, _, _ := runnableHandle(t)
	ctx, cancel := context.WithCancel(authorCtx())
	cancel()

	res, _, err := h.handleRunScript(ctx, runScriptInput{Name: "daily", WaitSeconds: 5})
	require.NoError(t, err)
	out := resultFields(t, res)
	assert.Equal(t, script.RunStatusPending, out[fieldStatus])
}

func TestWaitBudget(t *testing.T) {
	assert.Equal(t, time.Duration(0), waitBudget(-1))
	assert.Equal(t, time.Duration(DefaultWaitSeconds)*time.Second, waitBudget(0))
	assert.Equal(t, 5*time.Second, waitBudget(5))
	assert.Equal(t, time.Duration(MaxWaitSeconds)*time.Second, waitBudget(10_000))
}

// TestRuns_ListsAndReadsOneRun covers the history commands over a queued run.
func TestRuns_ListsAndReadsOneRun(t *testing.T) {
	h, _, runs := runnableHandle(t)
	queued := runScriptCall(t, h, runScriptInput{Name: "daily", WaitSeconds: -1})
	runID, ok := queued["run_id"].(string)
	require.True(t, ok)

	listed := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdRuns, Name: "daily"}))
	items, ok := listed["runs"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 1)

	single := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: runID}))
	assert.Equal(t, script.RunStatusPending, single[fieldStatus])
	assert.Equal(t, "jane@example.com", single["requested_by"])
	assert.NotNil(t, runs)
}

// TestGetRun_IsAuthorizedLikeTheScriptItBelongsTo pins that an unguessable run
// id is not an authorization rule: a caller who cannot see the script cannot
// read its runs, and cannot tell the difference from a run that does not exist.
func TestGetRun_IsAuthorizedLikeTheScriptItBelongsTo(t *testing.T) {
	h, _, _ := runnableHandle(t)
	queued := runScriptCall(t, h, runScriptInput{Name: "daily", WaitSeconds: -1})
	runID, ok := queued["run_id"].(string)
	require.True(t, ok)

	res := call(t, h, callerCtx("bob@example.com", "analyst"),
		manageScriptInput{Command: cmdGetRun, RunID: runID})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "run not found",
		"the same answer as a run that does not exist")
}

// TestRuns_FilterUsesTheRunStatusVocabulary pins that the runs listing filters
// on RUN statuses. The lifecycle `status` argument is a different vocabulary —
// a transition to apply to the script — and the tool's closed schema refuses
// a run status there, so a filter reading it would have been unreachable.
func TestRuns_FilterUsesTheRunStatusVocabulary(t *testing.T) {
	h, _, _ := runnableHandle(t)
	runScriptCall(t, h, runScriptInput{Name: "daily", WaitSeconds: -1})

	pending := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdRuns, Name: "daily", RunStatus: script.RunStatusPending,
	}))
	items, ok := pending["runs"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 1)

	failed := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdRuns, Name: "daily", RunStatus: script.RunStatusFailed,
	}))
	items, ok = failed["runs"].([]any)
	require.True(t, ok)
	assert.Empty(t, items)
}

func TestRunCommands_Failures(t *testing.T) {
	t.Run("runs listing fails", func(t *testing.T) {
		h, _, runs := runnableHandle(t)
		runs.listErr = errors.New("boom")
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdRuns, Name: "daily"})
		assert.True(t, res.IsError)
		assert.Contains(t, resultText(res), "failed to list runs")
	})

	t.Run("get_run needs a run id", func(t *testing.T) {
		h, _, _ := runnableHandle(t)
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun})
		assert.True(t, res.IsError)
		assert.Contains(t, resultText(res), "run_id is required")
	})

	t.Run("unknown run", func(t *testing.T) {
		h, _, _ := runnableHandle(t)
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: "dpx_nope"})
		assert.True(t, res.IsError)
		assert.Contains(t, resultText(res), "run not found")
	})

	t.Run("the script a run belongs to is gone", func(t *testing.T) {
		h, store, runs := runnableHandle(t)
		queued := runScriptCall(t, h, runScriptInput{Name: "daily", WaitSeconds: -1})
		runID, ok := queued["run_id"].(string)
		require.True(t, ok)
		sc, err := store.GetPersonal(context.Background(), "jane@example.com", "daily")
		require.NoError(t, err)
		require.NoError(t, store.Delete(context.Background(), sc.ID))
		assert.NotNil(t, runs)

		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: runID})
		assert.True(t, res.IsError)
		assert.Contains(t, resultText(res), "run not found")
	})

	t.Run("reading that script fails", func(t *testing.T) {
		store := &brokenScriptReads{memStore: newMemStore()}
		runs := newStubRuns()
		h := New(Config{Store: store, Runs: runs, AdminPersona: "admin"})
		require.NoError(t, runs.Enqueue(context.Background(), &script.Run{ID: "dpx_1", ScriptID: "script_1"}))

		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: "dpx_1"})
		assert.True(t, res.IsError)
		assert.Contains(t, resultText(res), "failed to read the run")
	})

	t.Run("run read fails", func(t *testing.T) {
		h, _, runs := runnableHandle(t)
		runs.getErr = errors.New("boom")
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: "dpx_1"})
		assert.True(t, res.IsError)
		assert.Contains(t, resultText(res), "failed to read the run")
	})
}

// TestRunCommands_UnavailableWithoutAQueue covers the deployment that keeps no
// runs: the commands say so rather than answering with an empty history.
func TestRunCommands_UnavailableWithoutAQueue(t *testing.T) {
	h, _ := newHandle()
	for _, command := range []string{cmdRuns, cmdGetRun} {
		res := call(t, h, authorCtx(), manageScriptInput{Command: command, Name: "daily", RunID: "dpx_1"})
		assert.True(t, res.IsError)
		assert.Contains(t, resultText(res), "keeps no script runs")
	}
}

// TestRunScript_UnapprovedScriptNamesTheWayForward pins the refusal an author
// is most likely to hit, and that it points at run_draft.
func TestRunScript_UnapprovedScriptNamesTheWayForward(t *testing.T) {
	h := unapprovedHandle(t)
	out := runScriptCall(t, h, runScriptInput{Name: "daily"})
	assert.Contains(t, out["error"], "no approved version")
	assert.Contains(t, out["error"], "run_draft")
}

// TestRunScript_WithoutAVersionStoreIsUnavailable covers a store that cannot
// answer for versions, where the approved code cannot be loaded at all.
func TestRunScript_WithoutAVersionStoreIsUnavailable(t *testing.T) {
	store := &unversionedStore{inner: newMemStore()}
	h := New(Config{Store: store, Runs: newStubRuns(), AdminPersona: "admin"})
	require.Nil(t, h.versions)

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "daily", Source: "print(1)\n"})
	require.False(t, res.IsError, resultText(res))
	sc, err := store.GetPersonal(context.Background(), "jane@example.com", "daily")
	require.NoError(t, err)
	sc.ApprovedVersionID = "sver_1"
	require.NoError(t, store.Update(context.Background(), sc))

	out := runScriptCall(t, h, runScriptInput{Name: "daily"})
	assert.Contains(t, out["error"], "cannot read script versions")
}

// brokenScriptReads fails the by-id read a run's authorization check makes.
type brokenScriptReads struct{ *memStore }

func (*brokenScriptReads) GetByID(context.Context, string) (*script.Script, error) {
	return nil, errors.New("boom")
}

// unversionedStore is a script store WITHOUT the versioning capability, the
// shape a degraded deployment holds. The inner store is a named field rather
// than an embedded one on purpose: embedding would promote its version methods
// and quietly turn this into the very thing it is standing in for.
type unversionedStore struct{ inner *memStore }

func (s *unversionedStore) Create(ctx context.Context, sc *script.Script, author script.Author) error {
	return s.inner.Create(ctx, sc, author)
}

func (s *unversionedStore) Get(ctx context.Context, name string) (*script.Script, error) {
	return s.inner.Get(ctx, name)
}

func (s *unversionedStore) GetPersonal(ctx context.Context, owner, name string) (*script.Script, error) {
	return s.inner.GetPersonal(ctx, owner, name)
}

func (s *unversionedStore) GetByID(ctx context.Context, id string) (*script.Script, error) {
	return s.inner.GetByID(ctx, id)
}

func (s *unversionedStore) Update(ctx context.Context, sc *script.Script) error {
	return s.inner.Update(ctx, sc)
}

func (s *unversionedStore) Delete(ctx context.Context, id string) error {
	return s.inner.Delete(ctx, id)
}

func (s *unversionedStore) List(ctx context.Context, filter script.ListFilter) ([]script.Script, error) {
	return s.inner.List(ctx, filter)
}

// TestRunScriptSchema_ClosedAndInSyncWithTheInputStruct holds run_script to the
// same #1057 contract manage_script is held to: a schema that publishes an
// argument the struct does not decode is input silently ignored, and the
// reverse is an argument no model will ever learn to send.
func TestRunScriptSchema_ClosedAndInSyncWithTheInputStruct(t *testing.T) {
	raw, err := json.Marshal(runScriptSchema())
	require.NoError(t, err)
	var obj struct {
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
		Required             []string       `json:"required"`
	}
	require.NoError(t, json.Unmarshal(raw, &obj))

	require.NotNil(t, obj.AdditionalProperties)
	assert.False(t, *obj.AdditionalProperties, `the schema must declare "additionalProperties": false`)
	assert.Equal(t, []string{fieldName}, obj.Required)

	fields := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeFor[runScriptInput]()) {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			fields[tag] = true
		}
	}
	for name := range obj.Properties {
		assert.True(t, fields[name], "the schema publishes %q but the input struct does not decode it", name)
	}
	for name := range fields {
		assert.NotNil(t, obj.Properties[name], "the input struct decodes %q but the closed schema does not publish it", name)
	}
}

// TestRunReads_AreTheOwnersAndTheAdmins pins who may read what a script did,
// which is a narrower entitlement than seeing that the script exists: a run
// carries the parameters it bound, the error it failed with, and free text the
// script printed while holding ITS grant.
func TestRunReads_AreTheOwnersAndTheAdmins(t *testing.T) {
	h, store, _ := runnableHandle(t)
	// Make the script visible to every analyst, so a colleague can see it and
	// still not be its owner.
	sc, err := store.GetPersonal(context.Background(), "jane@example.com", "daily")
	require.NoError(t, err)
	sc.Scope, sc.Personas = script.ScopePersona, []string{"analyst"}
	require.NoError(t, store.Update(context.Background(), sc))

	colleague := callerCtx("marcus@example.com", "analyst")
	res := call(t, h, colleague, manageScriptInput{Command: cmdGet, Name: "daily"})
	require.False(t, res.IsError, "a colleague can see the script itself")

	res = call(t, h, colleague, manageScriptInput{Command: cmdRuns, Name: "daily"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "only the owner of a script can read its runs")

	// The owner and an admin read them.
	assert.False(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdRuns, Name: "daily"}).IsError)
	assert.False(t, call(t, h, adminCtx(), manageScriptInput{Command: cmdRuns, Name: "daily"}).IsError)
}

// TestGetRun_ReadableByWhoeverAskedForIt covers the third entitlement: a
// caller who ran the script gets the result when they ask for it, so a run id
// they were handed must stay followable even though the script is not theirs.
func TestGetRun_ReadableByWhoeverAskedForIt(t *testing.T) {
	h, store, runs := runnableHandle(t)
	sc, err := store.GetPersonal(context.Background(), "jane@example.com", "daily")
	require.NoError(t, err)
	sc.Scope, sc.Personas = script.ScopePersona, []string{"analyst"}
	require.NoError(t, store.Update(context.Background(), sc))

	colleague := callerCtx("marcus@example.com", "analyst")
	res, _, err := h.handleRunScript(colleague, runScriptInput{Name: "daily", WaitSeconds: -1})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	runID, ok := resultFields(t, res)["run_id"].(string)
	require.True(t, ok)
	assert.NotNil(t, runs)

	own := call(t, h, colleague, manageScriptInput{Command: cmdGetRun, RunID: runID})
	assert.False(t, own.IsError, resultText(own))

	// Somebody else's run of the same script stays out of reach.
	stranger := callerCtx("dana@example.com", "analyst")
	other := call(t, h, stranger, manageScriptInput{Command: cmdGetRun, RunID: runID})
	assert.True(t, other.IsError)
	assert.Contains(t, resultText(other), "run not found")
}

// callerCtxWithoutEmail is a caller the identity provider named but issued no
// email for: an OIDC token carrying a subject and no email claim.
func callerCtxWithoutEmail(userID, persona string) context.Context {
	pc := middleware.NewPlatformContext("req_1")
	pc.UserID, pc.PersonaName = userID, persona
	pc.Roles = []string{persona}
	return middleware.WithPlatformContext(context.Background(), pc)
}

// TestEmaillessCallersAreDistinctOwners pins the identity a personal script is
// owned by. A token without an email claim leaves the email empty, and
// collapsing every such caller onto one sentinel would make their personal
// scripts a shared pool: distinct people reading, editing, and running each
// other's work.
func TestEmaillessCallersAreDistinctOwners(t *testing.T) {
	store := newMemStore()
	h := New(Config{Store: store, Runs: newStubRuns(), AdminPersona: "admin"})

	sarah := callerCtxWithoutEmail("oidc|sarah", "analyst")
	marcus := callerCtxWithoutEmail("oidc|marcus", "analyst")

	res := call(t, h, sarah, manageScriptInput{Command: cmdCreate, Name: "daily", Source: "print(1)\n"})
	require.False(t, res.IsError, resultText(res))

	sc, err := store.GetPersonal(context.Background(), "oidc|sarah", "daily")
	require.NoError(t, err)
	require.NotNil(t, sc, "the script is owned by the caller's own identity, not by a shared sentinel")

	// The other caller cannot see it, read its runs, or change it.
	for _, command := range []string{cmdGet, cmdRuns, cmdUpdate} {
		other := call(t, h, marcus, manageScriptInput{Command: command, Name: "daily", Source: "print(2)\n"})
		assert.True(t, other.IsError, command)
		assert.Contains(t, resultText(other), "not found", command)
	}

	// And the owner still reaches their own.
	assert.False(t, call(t, h, sarah, manageScriptInput{Command: cmdGet, Name: "daily"}).IsError)
}

// A deployment that presents no identity at all has exactly one caller, and
// that caller owns their scripts under the "anonymous" name.
func TestUnidentifiedCallerIsStillAnOwner(t *testing.T) {
	store := newMemStore()
	h := New(Config{Store: store, Runs: newStubRuns(), AdminPersona: "admin"})
	ctx := middleware.WithPlatformContext(context.Background(), middleware.NewPlatformContext("req_1"))

	res := call(t, h, ctx, manageScriptInput{Command: cmdCreate, Name: "daily", Source: "print(1)\n"})
	require.False(t, res.IsError, resultText(res))
	assert.False(t, call(t, h, ctx, manageScriptInput{Command: cmdGet, Name: "daily"}).IsError)
}
