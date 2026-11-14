// Package callrun runs the tool-call handlers (#2436, #2634, #2635): a
// policy's decide(call) before a call, a transform's transform(call, result)
// on what it returns, and a watch's queued run after it succeeds. A policy and
// a transform run inline, read-only and time-limited, on the version an
// administrator approved for handlers, with that administrator's roles; their
// own calls are not evaluated by handlers.
package callrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdecide"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// The outcomes a handler's run is recorded under, on the audit row and the
// test panel.
const (
	OutcomeAllow       = "allow"
	OutcomeDeny        = "deny"
	OutcomeApprove     = "require_approval"
	OutcomeError       = "error"
	OutcomeTransformed = "transformed"
	OutcomeOriginal    = "original"
	OutcomeQueued      = "queued"
	OutcomeAllowance   = "allowance"
)

// The verdicts a call gets from its policies.
const (
	VerdictRuns   = "runs"
	VerdictDenied = "denied"
	VerdictHeld   = "held"
)

// targetAge is how long a handler's resolved script, approved version and
// compiled program are used before they are read again: an approval or a save
// is picked up within it.
const targetAge = 3 * time.Second

// maxSlots is how many handler runs a replica holds at once; a call past it
// is held rather than waiting, as a timed-out policy is.
const maxSlots = 64

// clientLabel names the session a handler's run opens.
const clientLabel = "call-handler"

// Scripts reads a handler's script and the version that runs.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Approvals is what a handler's version and a held call are approved
// through (approval.Service).
type Approvals interface {
	HandlerRunAs(ctx context.Context, scriptID string) (approval.RunAs, bool)
	SpendAllowance(ctx context.Context, requester, connection, operation string) (string, bool)
	SubmitCall(ctx context.Context, actor approval.Actor, in approval.CallRequest) (*approval.Request, error)
}

// Store is where counts and failures are kept (callhandler.Store).
type Store interface {
	Count(ctx context.Context, t callhandler.Tally) (int, error)
	Release(ctx context.Context, callID string) error
	RecordFailure(ctx context.Context, f callhandler.Failure) error
}

// Evaluator runs the tool-call handlers.
type Evaluator struct {
	Index     *callhandler.Index
	Store     Store
	Scripts   Scripts
	Approvals Approvals
	// Shapes reads an api call's operation, by toolkit kind; nil reads none.
	Shapes func(kind string) Shapes
	// Limits are the platform run limits a handler's run starts from.
	Limits scriptrun.PlatformLimits
	// Watches queues a watch's runs; nil queues none.
	Watches *WatchQueue
	// Protect checks a covered caller's SQL against the columns transforms
	// protect; nil refuses any statement on a connection a protecting
	// transform covers, which fails closed.
	Protect func(ctx context.Context, connection, sql string, protected map[string]string) error

	server *mcp.Server
	slots  chan struct{}
	now    func() time.Time

	mu      sync.Mutex
	targets map[string]target
}

// target is what one handler runs: its script, the approved version, who
// approved it, and the version's compiled program.
type target struct {
	script  *script.Script
	version *script.Version
	runAs   approval.RunAs
	program *scriptdialect.Program
	readAt  time.Time
}

// SetServer is the assembled MCP server a handler's run opens its session
// against; the server is built after the evaluator, so it is set late.
func (e *Evaluator) SetServer(s *mcp.Server) {
	if e != nil {
		e.server = s
	}
}

func (e *Evaluator) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// errNoApproved is a handler whose script has no version approved for
// handlers: it cannot run, which its policy reads as a failure.
var errNoApproved = errors.New("no version of its script is approved for handlers")

// maxTargets bounds the resolved-target cache; past it the cache starts over.
const maxTargets = 1024

// targetFor resolves what h runs, from the cache while it is fresh.
func (e *Evaluator) targetFor(ctx context.Context, h callhandler.Handler) (target, error) {
	e.mu.Lock()
	t, ok := e.targets[h.ID]
	e.mu.Unlock()
	if ok && e.clock().Sub(t.readAt) < targetAge {
		return t, nil
	}
	next, err := e.readTarget(ctx, h)
	if err != nil {
		return target{}, err
	}
	if ok && t.version != nil && t.version.ID == next.version.ID {
		next.program = t.program
	}
	e.mu.Lock()
	if e.targets == nil || len(e.targets) > maxTargets {
		e.targets = map[string]target{}
	}
	e.targets[h.ID] = next
	e.mu.Unlock()
	return next, nil
}

// readTarget reads h's script, the version approved for handlers and who
// approved it, with a fresh program to compile into.
func (e *Evaluator) readTarget(ctx context.Context, h callhandler.Handler) (target, error) {
	sc, err := e.Scripts.GetByID(ctx, h.ScriptID)
	if err != nil || sc == nil {
		return target{}, errors.New("its script could not be read")
	}
	runAs, approved := e.Approvals.HandlerRunAs(ctx, sc.ID)
	if !approved {
		return target{}, errNoApproved
	}
	v, err := e.Scripts.GetVersion(ctx, sc.ID, runAs.Version)
	if err != nil || v == nil {
		return target{}, fmt.Errorf("its approved version %d could not be read", runAs.Version)
	}
	return target{script: sc, version: v, runAs: runAs, readAt: e.clock(), program: &scriptdialect.Program{}}, nil
}

// run is one handler's run of entry with args: the value it returned and
// what it printed.
type run struct {
	value  starlark.Value
	prints string
	ms     int64
}

// entry is the function a handler's run calls: its name, its arguments, and
// the counter platform.count reads (nil for none).
type entry struct {
	name    string
	args    starlark.Tuple
	counter scriptdecide.Counter
}

// invoke runs h's entry on its approved version, read-only, under its time
// limit, with the approving administrator's roles, in a session whose calls
// no handler evaluates.
func (e *Evaluator) invoke(ctx context.Context, h callhandler.Handler, call map[string]any, en entry) (run, error) {
	started := e.clock()
	out := run{}
	release, err := e.acquire()
	if err != nil {
		return out, err
	}
	defer release()
	t, err := e.targetFor(ctx, h)
	if err != nil {
		return out, err
	}
	sessionID, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		return out, fmt.Errorf("naming the run: %w", err)
	}
	caller, cleanup, err := e.connect(ctx, t, sessionID)
	if err != nil {
		return out, err
	}
	defer cleanup()
	opts := scriptrun.RunLimits(e.Limits)
	opts.Timeout = time.Duration(h.TimeoutMS) * time.Millisecond
	opts.Source, opts.Name, opts.RunID = t.version.Source, t.script.Name, sessionID
	opts.FireTime = e.clock().UTC()
	opts.Caller, opts.Writes = caller, scriptrun.WritesRefused
	opts.Call, opts.Counter = call, en.counter
	var queued []map[string]any
	opts.Notices = &queued
	if opts.Params, err = script.BindParams(t.version.Params, h.Args); err != nil {
		return out, fmt.Errorf("its arguments do not fit the script's parameters: %w", err)
	}
	opts.Handler = &scriptdialect.Handler{
		Name: en.name, Args: en.args, Program: t.program,
		Returned: func(v starlark.Value) error { out.value = v; return nil },
	}
	res, err := scriptrun.Run(ctx, opts)
	if res != nil {
		out.prints = res.Log
	}
	if err == nil {
		e.queue(ctx, t, queued)
	}
	out.ms = e.clock().Sub(started).Milliseconds()
	return out, err //nolint:wrapcheck // the run's own words, which the failure record keeps
}

// acquire takes one of the replica's handler slots, or says why a run cannot
// start: a call past the slots is held rather than waiting.
func (e *Evaluator) acquire() (func(), error) {
	if e.server == nil {
		return nil, errors.New("the platform's server is not ready")
	}
	if e.slots == nil {
		return nil, errors.New("the evaluator is not started")
	}
	select {
	case e.slots <- struct{}{}:
		return func() { <-e.slots }, nil
	default:
		return nil, errors.New("this replica is running as many handlers as it may at once")
	}
}

// connect opens the run's in-memory session as the script principal,
// presenting the roles of the administrator who approved the version, and
// marked as a handler's own so its calls skip the handlers.
func (e *Evaluator) connect(ctx context.Context, t target, sessionID string) (scriptrun.Caller, func(), error) {
	serverCtx := callhandler.WithinHandler(ctx)
	serverCtx = middleware.WithSource(serverCtx, middleware.SourceScript)
	serverCtx = pkgsession.WithAwareSessionID(serverCtx, sessionID)
	serverCtx = producedby.With(serverCtx, producedby.Producer{Kind: producedby.KindScript, ID: t.script.ID, Label: t.script.Name})
	serverCtx = middleware.WithPreAuthenticatedUser(serverCtx, &middleware.UserInfo{
		UserID: t.script.Principal(), Email: t.runAs.Approver, Roles: t.runAs.Roles,
		AuthType: middleware.AuthTypeScript, OnBehalfOf: t.runAs.Approver,
	})
	caller, cleanup, err := scriptsession.Connect(serverCtx, e.server, clientLabel)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the handler's session: %w", err)
	}
	return caller, cleanup, nil
}

// Verdict is what a call's policies decided.
type Verdict struct {
	Outcome string
	// Reason and Handler are a refusal's: the deny's reason and the script
	// that said it.
	Reason  string
	Handler string
	// Approvers and Asks are a hold's: who may approve it, merged across the
	// policies that held it, and what each said.
	Approvers []string
	Asks      []approval.HandlerAsk
	// AllowanceID is the approved allowance a held call ran on.
	AllowanceID string
	Runs        []audit.HandlerRun
}

// Decide runs every policy that applies to c and combines what they decided:
// a deny beats a hold beats an allow. A held call an approved allowance
// covers runs, spending one call of it. A call that does not run counts
// against nobody. dry is the test panel's: nothing is counted, spent or
// recorded.
func (e *Evaluator) Decide(ctx context.Context, c *callhandler.Call, callID string, dry bool) Verdict {
	if dry {
		// A dry decision sends nothing: its messages go on a list never sent.
		ctx, _ = withNotices(ctx)
	}
	policies := e.Index.Match(c, callhandler.KindPolicy)
	if len(policies) == 0 {
		return Verdict{Outcome: VerdictRuns}
	}
	v := e.combine(ctx, policies, c, callID, dry)
	if v.Outcome == VerdictHeld && !dry {
		if id, ok := e.Approvals.SpendAllowance(ctx, c.User, c.Connection, c.Operation); ok {
			v.Outcome, v.AllowanceID = VerdictRuns, id
			v.Runs = append(v.Runs, audit.HandlerRun{ID: id, Kind: "allowance", Outcome: OutcomeAllowance})
		}
	}
	if v.Outcome != VerdictRuns && !dry && callID != "" {
		if err := e.Store.Release(ctx, callID); err != nil {
			slog.WarnContext(ctx, "call handlers: releasing a refused call's counts failed", "error", logsan.SanitizeForLog(err.Error()))
		}
	}
	return v
}

// combine runs each policy and folds what they decided: a deny beats a hold
// beats an allow.
func (e *Evaluator) combine(ctx context.Context, policies []callhandler.Handler, c *callhandler.Call, callID string, dry bool) Verdict {
	v := Verdict{Outcome: VerdictRuns}
	var denied, held bool
	for _, h := range policies {
		d, r := e.decideOne(ctx, h, c, callID, dry)
		v.Runs = append(v.Runs, r)
		switch d.kind {
		case scriptdecide.DecisionDeny:
			if !denied {
				denied, v.Reason, v.Handler = true, d.reason, r.Script
			}
		case scriptdecide.DecisionApprove:
			held = true
			v.Approvers = mergeApprovers(v.Approvers, d.approvers)
			v.Asks = append(v.Asks, approval.HandlerAsk{ID: h.ID, Script: r.Script, Summary: d.summary})
		}
	}
	switch {
	case denied:
		v.Outcome, v.Approvers, v.Asks = VerdictDenied, nil, nil
	case held:
		v.Outcome = VerdictHeld
	}
	return v
}

// decision is what one policy decided.
type decision struct {
	kind      string
	reason    string
	approvers []string
	summary   string
}

// decideOne runs one policy on c. A policy that fails or runs out of time
// holds the call for an administrator, or refuses it when it says so.
func (e *Evaluator) decideOne(ctx context.Context, h callhandler.Handler, c *callhandler.Call, callID string, dry bool) (decision, audit.HandlerRun) {
	callMap := CallMap(c, &h)
	counter := e.counter(ctx, h, callID, dry)
	callValue, err := starlarkconv.ToStarlark(callMap)
	rec := audit.HandlerRun{ID: h.ID, Kind: h.Kind, Script: e.scriptName(ctx, h)}
	var r run
	if err == nil {
		r, err = e.invoke(ctx, h, callMap, entry{name: callhandler.DecideName, args: starlark.Tuple{callValue}, counter: counter})
	}
	rec.MS = r.ms
	d := decision{}
	if err == nil {
		d, err = readDecision(r.value)
	}
	if err != nil {
		rec.Outcome = OutcomeError
		if !dry {
			e.recordFailure(ctx, h, c, r, err)
		}
		if h.OnFailure == callhandler.FailDeny {
			return decision{kind: scriptdecide.DecisionDeny, reason: fmt.Sprintf("the policy %s could not decide, and refuses the call when it cannot", rec.Script)}, rec
		}
		return decision{kind: scriptdecide.DecisionApprove, summary: fmt.Sprintf("the policy %s could not decide, so the call waits for an administrator", rec.Script)}, rec
	}
	rec.Outcome = d.kind
	return d, rec
}

// counter is platform.count for h's run on one call: a dry run reads the
// count and adds the call without storing it.
func (e *Evaluator) counter(ctx context.Context, h callhandler.Handler, callID string, dry bool) scriptdecide.Counter {
	return func(_ context.Context, key string, window time.Duration, amount int) (int, error) {
		t := callhandler.Tally{HandlerID: h.ID, Key: key, CallID: callID, Amount: amount, Window: window}
		if dry {
			t.Amount = 0
		}
		n, err := e.Store.Count(ctx, t)
		if err != nil {
			return 0, fmt.Errorf("counting %s: %w", key, err)
		}
		if dry {
			n += amount
		}
		return n, nil
	}
}

// readDecision reads what decide(call) returned: one of the three
// constructors' dicts.
func readDecision(v starlark.Value) (decision, error) {
	d, ok := v.(*starlark.Dict)
	if !ok {
		return decision{}, fmt.Errorf("decide(call) returned %s, not platform.allow(), platform.deny(reason) or platform.require_approval(...)", typeName(v))
	}
	raw, err := starlarkconv.FromStarlark(d)
	if err != nil {
		return decision{}, fmt.Errorf("decide(call)'s value could not be read: %w", err)
	}
	m, _ := raw.(map[string]any)
	out := decision{}
	out.kind, _ = m["decision"].(string)
	out.reason, _ = m["reason"].(string)
	out.summary, _ = m["summary"].(string)
	if list, ok := m["approvers"].([]any); ok {
		for _, a := range list {
			if s, ok := a.(string); ok {
				out.approvers = append(out.approvers, s)
			}
		}
	}
	switch out.kind {
	case scriptdecide.DecisionAllow, scriptdecide.DecisionDeny, scriptdecide.DecisionApprove:
		return out, nil
	}
	return decision{}, errors.New("decide(call) returned a dict that is not a decision; return platform.allow(), platform.deny(reason) or platform.require_approval(...)")
}

func typeName(v starlark.Value) string {
	if v == nil {
		return "nothing"
	}
	return v.Type()
}

// mergeApprovers adds the approvers one policy named, once each; none named
// is an administrator.
func mergeApprovers(have, add []string) []string {
	if len(add) == 0 {
		add = []string{approval.Administrators}
	}
	for _, a := range add {
		if !slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, a) }) {
			have = append(have, a)
		}
	}
	return have
}

// scriptName is h's script's name for the record, "" when it cannot be read.
func (e *Evaluator) scriptName(ctx context.Context, h callhandler.Handler) string {
	e.mu.Lock()
	t, ok := e.targets[h.ID]
	e.mu.Unlock()
	if ok && t.script != nil {
		return t.script.Name
	}
	if sc, err := e.Scripts.GetByID(ctx, h.ScriptID); err == nil && sc != nil {
		return sc.Name
	}
	return ""
}

// recordFailure keeps what a failed run was given and printed, for
// administrators.
func (e *Evaluator) recordFailure(ctx context.Context, h callhandler.Handler, c *callhandler.Call, r run, err error) {
	version := 0
	e.mu.Lock()
	if t, ok := e.targets[h.ID]; ok && t.version != nil {
		version = t.version.Version
	}
	e.mu.Unlock()
	f := callhandler.Failure{
		HandlerID: h.ID, Version: version, User: c.User, Call: CallMap(c, &h), Prints: r.prints,
		Error: err.Error(), Outcome: h.OnFailure, DurationMS: int(r.ms),
	}
	if werr := e.Store.RecordFailure(context.WithoutCancel(ctx), f); werr != nil {
		slog.WarnContext(ctx, "call handlers: recording a failure failed", "error", logsan.SanitizeForLog(werr.Error()))
	}
}

// CallMap is c as a handler's script is given it, with h's reading of its
// items and whether it writes.
func CallMap(c *callhandler.Call, h *callhandler.Handler) map[string]any {
	cp := *c
	if h != nil {
		cp.Items = callhandler.ItemCount(c.Body, h.ItemsPath)
		cp.Write = h.IsWrite(c)
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// Start readies the evaluator to run handlers.
func (e *Evaluator) Start() {
	if e != nil && e.slots == nil {
		e.slots = make(chan struct{}, maxSlots)
	}
}
