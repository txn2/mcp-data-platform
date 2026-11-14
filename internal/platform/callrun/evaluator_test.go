package callrun

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// world is what an evaluator reads, in memory.
type world struct {
	mu       sync.Mutex
	scripts  map[string]*script.Script
	versions map[string]*script.Version
	// unapproved scripts have no version approved for handlers.
	unapproved map[string]bool
	allowance  string
	counts     map[string]int
	released   []string
	failures   []callhandler.Failure
	filed      []approval.CallRequest
	handlers   []callhandler.Handler
}

func newWorld() *world {
	return &world{scripts: map[string]*script.Script{}, versions: map[string]*script.Version{}, unapproved: map[string]bool{}, counts: map[string]int{}}
}

// add saves source as a script and binds it as a handler of kind.
func (w *world) add(id, kind, source string, edit func(*callhandler.Handler)) callhandler.Handler {
	w.scripts[id] = &script.Script{ID: id, Name: "script-" + id, OwnerEmail: "kim@example.com", Version: 1}
	w.versions[id] = &script.Version{ID: "v-" + id, ScriptID: id, Version: 1, Source: source}
	h := callhandler.Handler{ID: "h-" + id, ScriptID: id, Kind: kind, Connection: "billing-api", Enabled: true, CreatedAt: time.Now()}
	if edit != nil {
		edit(&h)
	}
	h.Normalize()
	w.handlers = append(w.handlers, h)
	return h
}

func (w *world) GetByID(_ context.Context, id string) (*script.Script, error) {
	sc, ok := w.scripts[id]
	if !ok {
		return nil, errors.New("no script")
	}
	return sc, nil
}

func (w *world) GetVersion(_ context.Context, id string, _ int) (*script.Version, error) {
	return w.versions[id], nil
}

func (w *world) HandlerRunAs(_ context.Context, id string) (approval.RunAs, bool) {
	if w.unapproved[id] {
		return approval.RunAs{}, false
	}
	return approval.RunAs{Version: 1, Approver: "admin@example.com", Roles: []string{"admin"}}, true
}

func (w *world) SpendAllowance(context.Context, string, string, string) (string, bool) {
	return w.allowance, w.allowance != ""
}

func (w *world) SubmitCall(_ context.Context, _ approval.Actor, in approval.CallRequest) (*approval.Request, error) {
	w.filed = append(w.filed, in)
	return &approval.Request{ID: "apr_1"}, nil
}

func (w *world) Count(_ context.Context, t callhandler.Tally) (int, error) {
	handlerID, key, amount := t.HandlerID, t.Key, t.Amount
	w.mu.Lock()
	defer w.mu.Unlock()
	w.counts[handlerID+key] += amount
	return w.counts[handlerID+key], nil
}

func (w *world) Release(_ context.Context, callID string) error {
	w.released = append(w.released, callID)
	return nil
}

func (w *world) RecordFailure(_ context.Context, f callhandler.Failure) error {
	w.failures = append(w.failures, f)
	return nil
}

func (w *world) Enabled(context.Context) ([]callhandler.Handler, error) { return w.handlers, nil }

// evaluator is an evaluator over w with a real MCP server to run scripts in.
func (w *world) evaluator(t *testing.T) *Evaluator {
	t.Helper()
	e := &Evaluator{Index: &callhandler.Index{Reader: w}, Store: w, Scripts: w, Approvals: w, Limits: scriptrun.PlatformLimits{}}
	e.SetServer(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil))
	e.Start()
	require.NoError(t, e.Index.Load(context.Background()))
	return e
}

func sample() *callhandler.Call {
	return &callhandler.Call{
		Tool: "api_invoke_endpoint", Connection: "billing-api", Operation: "addSubscribers", Method: "POST", Path: "/lists/7",
		Arguments: map[string]any{"connection": "billing-api"}, Body: map[string]any{"subscribers": []any{1, 2, 3, 4, 5}},
		User: "jo@example.com", Persona: "marketing", PathParams: map[string]string{}, Query: map[string]any{},
	}
}

const (
	allowSrc   = "def decide(_call):\n    return platform.allow()\n"
	denySrc    = "def decide(_call):\n    return platform.deny(\"not on Fridays\")\n"
	holdSrc    = "def decide(_call):\n    return platform.require_approval([\"ops-leads\"], \"waits.\")\n"
	raiseSrc   = "def decide(call):\n    print(call[\"user\"])\n    fail(\"cannot decide\")\n"
	notDictSrc = "def decide(_call):\n    return 1\n"
	countSrc   = "def decide(call):\n    n = platform.count(call[\"user\"], \"1h\", call[\"items\"])\n    if n > 3:\n        return platform.require_approval([], \"%d items\" % n)\n    return platform.allow()\n"
)

// TestDecide combines what the policies say: a deny beats a hold beats an
// allow, and a call that does not run counts against nobody.
func TestDecide(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("a", callhandler.KindPolicy, allowSrc, nil)
	e := w.evaluator(t)
	v := e.Decide(ctx, sample(), "c1", false)
	assert.Equal(t, VerdictRuns, v.Outcome)
	require.Len(t, v.Runs, 1)
	assert.Equal(t, OutcomeAllow, v.Runs[0].Outcome)
	assert.Equal(t, "script-a", v.Runs[0].Script)
	assert.Empty(t, w.released)

	w.add("h", callhandler.KindPolicy, holdSrc, nil)
	e = w.evaluator(t)
	v = e.Decide(ctx, sample(), "c2", false)
	assert.Equal(t, VerdictHeld, v.Outcome)
	assert.Equal(t, []string{"ops-leads"}, v.Approvers)
	require.Len(t, v.Asks, 1)
	assert.Equal(t, "waits.", v.Asks[0].Summary)
	assert.Equal(t, []string{"c2"}, w.released, "a held call counts against nobody")

	w.add("d", callhandler.KindPolicy, denySrc, nil)
	e = w.evaluator(t)
	v = e.Decide(ctx, sample(), "c3", false)
	assert.Equal(t, VerdictDenied, v.Outcome)
	assert.Equal(t, "not on Fridays", v.Reason)
	assert.Equal(t, "script-d", v.Handler)
	assert.Empty(t, v.Approvers, "a refusal names no approvers")
}

// TestDecide_AnAllowanceLetsAHeldCallRun spends one call of an approved
// allowance; a dry run spends none.
func TestDecide_AnAllowanceLetsAHeldCallRun(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("h", callhandler.KindPolicy, holdSrc, nil)
	w.allowance = "apr_9"
	e := w.evaluator(t)
	v := e.Decide(ctx, sample(), "c1", false)
	assert.Equal(t, VerdictRuns, v.Outcome)
	assert.Equal(t, "apr_9", v.AllowanceID)
	assert.Equal(t, OutcomeAllowance, v.Runs[len(v.Runs)-1].Outcome)
	assert.Equal(t, VerdictHeld, e.Decide(ctx, sample(), "c2", true).Outcome, "the test panel spends no allowance")
}

// TestDecide_AFailedPolicyHoldsOrRefuses records what it was given and
// printed, and treats the call as it says.
func TestDecide_AFailedPolicyHoldsOrRefuses(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("r", callhandler.KindPolicy, raiseSrc, nil)
	e := w.evaluator(t)
	v := e.Decide(ctx, sample(), "c1", false)
	assert.Equal(t, VerdictHeld, v.Outcome)
	assert.Equal(t, []string{approval.Administrators}, v.Approvers)
	assert.Equal(t, OutcomeError, v.Runs[0].Outcome)
	require.Len(t, w.failures, 1)
	assert.Contains(t, w.failures[0].Prints, "jo@example.com")
	assert.Contains(t, w.failures[0].Error, "cannot decide")
	assert.Equal(t, "billing-api", w.failures[0].Call["connection"])

	w.handlers[0].OnFailure = callhandler.FailDeny
	e = w.evaluator(t)
	v = e.Decide(ctx, sample(), "c2", false)
	assert.Equal(t, VerdictDenied, v.Outcome)
	assert.Contains(t, v.Reason, "could not decide")

	e.Decide(ctx, sample(), "c3", true)
	assert.Len(t, w.failures, 2, "a dry run records no failure")
}

// TestDecide_WhatAPolicyCannotRun: a script returning no decision, a script
// with no approved version.
func TestDecide_WhatAPolicyCannotRun(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("n", callhandler.KindPolicy, notDictSrc, nil)
	e := w.evaluator(t)
	assert.Equal(t, VerdictHeld, e.Decide(ctx, sample(), "c1", false).Outcome)
	assert.Contains(t, w.failures[0].Error, "returned int")

	w = newWorld()
	w.add("u", callhandler.KindPolicy, allowSrc, nil)
	w.unapproved["u"] = true
	e = w.evaluator(t)
	assert.Equal(t, VerdictHeld, e.Decide(ctx, sample(), "c1", false).Outcome)
	assert.Contains(t, w.failures[0].Error, "approved")
}

// TestDecide_ACountIsShared counts the call's items under the script's key.
func TestDecide_ACountIsShared(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("c", callhandler.KindPolicy, countSrc, func(h *callhandler.Handler) { h.ItemsPath = "subscribers" })
	e := w.evaluator(t)
	v := e.Decide(ctx, sample(), "c1", false)
	assert.Equal(t, VerdictHeld, v.Outcome, "5 items over a limit of 3")
	assert.Equal(t, "5 items", v.Asks[0].Summary)
	c := sample()
	c.Body = map[string]any{"subscribers": []any{1}}
	w.counts = map[string]int{}
	assert.Equal(t, VerdictRuns, e.Decide(ctx, c, "c2", false).Outcome)
	v = e.Decide(ctx, c, "c3", true)
	assert.Equal(t, VerdictRuns, v.Outcome, "a dry run reads the count and adds this call without storing it")
	assert.Equal(t, 1, w.counts["h-c"+"jo@example.com"], "nothing was stored by the dry run")
}

// TestTransform runs transforms in order, and fails or returns the original
// as each says.
func TestTransform(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	tag := func(letter string) string {
		return "def transform(_call, result):\n    result[\"order\"] = result.get(\"order\", \"\") + \"" + letter + "\"\n    return result\n"
	}
	b := w.add("b", callhandler.KindTransform, tag("B"), func(h *callhandler.Handler) { h.Order = 2 })
	w.add("a", callhandler.KindTransform, tag("A"), func(h *callhandler.Handler) { h.Order = 1 })
	e := w.evaluator(t)
	transforms := e.Index.Match(sample(), callhandler.KindTransform)
	out, runs, err := e.Transform(ctx, sample(), transforms, map[string]any{"body": 1}, false)
	require.NoError(t, err)
	assert.Equal(t, "AB", out["order"])
	assert.Len(t, runs, 2)
	assert.Equal(t, OutcomeTransformed, runs[0].Outcome)
	_ = b

	w = newWorld()
	w.add("r", callhandler.KindTransform, "def transform(_call, _result):\n    fail(\"boom\")\n", nil)
	e = w.evaluator(t)
	transforms = e.Index.Match(sample(), callhandler.KindTransform)
	_, _, err = e.Transform(ctx, sample(), transforms, map[string]any{"v": 1}, false)
	var te *TransformError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "script-r", te.Script)
	assert.Contains(t, err.Error(), "the transform script-r failed")
	assert.ErrorContains(t, errors.Unwrap(err), "boom")

	w.handlers[0].OnFailure = callhandler.FailOriginal
	e = w.evaluator(t)
	transforms = e.Index.Match(sample(), callhandler.KindTransform)
	out, runs, err = e.Transform(ctx, sample(), transforms, map[string]any{"v": 1}, false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"v": 1}, out)
	assert.Equal(t, OutcomeOriginal, runs[0].Outcome)

	w = newWorld()
	w.add("s", callhandler.KindTransform, "def transform(_call, _result):\n    return 1\n", nil)
	e = w.evaluator(t)
	_, _, err = e.Transform(ctx, sample(), e.Index.Match(sample(), callhandler.KindTransform), map[string]any{}, false)
	require.ErrorContains(t, err, "return the result, a dict")
}

// TestInvokeRefuses a run with no server, before Start, and past the slots.
func TestInvokeRefuses(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	h := w.add("a", callhandler.KindPolicy, allowSrc, nil)
	e := &Evaluator{Index: &callhandler.Index{Reader: w}, Store: w, Scripts: w, Approvals: w}
	_, err := e.invoke(ctx, h, nil, entry{name: callhandler.DecideName})
	require.ErrorContains(t, err, "not ready")
	e.SetServer(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil))
	_, err = e.invoke(ctx, h, nil, entry{name: callhandler.DecideName})
	require.ErrorContains(t, err, "not started")
	e.slots = make(chan struct{})
	_, err = e.invoke(ctx, h, nil, entry{name: callhandler.DecideName})
	require.ErrorContains(t, err, "as many handlers")
	var nilEval *Evaluator
	nilEval.SetServer(nil)
	nilEval.Start()
	h.Args = map[string]any{"nope": 1}
	e.Start()
	e.slots = make(chan struct{}, 1)
	_, err = e.invoke(ctx, h, nil, entry{name: callhandler.DecideName})
	require.ErrorContains(t, err, "parameters")
}

// TestCallMapAndMergeApprovers are what a script is given and who approves.
func TestCallMapAndMergeApprovers(t *testing.T) {
	h := callhandler.Handler{ItemsPath: "subscribers", Writes: map[string]bool{}}
	m := CallMap(sample(), &h)
	assert.InDelta(t, 5, m["items"], 0)
	assert.Equal(t, true, m["write"])
	assert.Equal(t, []string{"administrators", "Jo"}, mergeApprovers(mergeApprovers(nil, nil), []string{"Jo", "administrators"}))
	assert.Equal(t, "nothing", typeName(nil))
	assert.True(t, strings.HasPrefix(heldMessage(sample(), Verdict{Asks: []approval.HandlerAsk{{Script: "s", Summary: "waits."}}, Approvers: []string{"ops"}}),
		"billing-api addSubscribers is held by s: waits."))
}
