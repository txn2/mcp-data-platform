// Package scriptdraft executes a managed script's draft under the identity of
// the person asking for it.
//
// It introduces no authority. The run opens an in-memory MCP session carrying
// the caller's own identity, so every platform call it makes is authenticated,
// authorized, rate limited, and audited exactly as the same call typed by that
// person directly would be: there is nothing reachable through a draft run that
// its caller could not already reach by calling the tools themselves. What it
// adds is the loop — real interpreter errors, real rows, real shapes — so a
// script is finished before it is saved as the version that runs.
//
// It is deliberately NOT a way around the execution gate: it persists nothing
// (platform.export previews), it runs under tighter limits than a platform run
// will, and it persists nothing a run would.
//
// The package exists because there are two surfaces that ask for a draft run —
// the manage_script tool an agent calls and the editor its owner works in
// (#1364) — and exactly one of them may decide what a draft run is. The
// identity is the caller's in both cases; the difference between them is only
// where that identity was read from.
package scriptdraft

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// clientLabel names the draft's client in the MCP handshake, so a draft run and
// a platform one are distinguishable in logs.
const clientLabel = "script-draft"

// Concurrency bound.
//
// A run holds a Starlark heap the interpreter cannot cap, so the number of
// concurrent runs is the one lever that bounds the memory a pathological script
// can reach. The platform-run worker takes that lever by executing one run at a
// time per replica; a draft run has no queue in front of it, and since #1364 it
// is reachable from a form in a browser rather than only from a tool call.
//
// The bound here is therefore small rather than one: an author iterating is
// interactive work, and serializing every author in a deployment behind one
// interpreter would make the loop unusable for the second person to press the
// button. A request that cannot get a slot within the wait answers rather than
// queueing, because the person is waiting on it.
const (
	maxConcurrentDrafts = 4
	slotWait            = 5 * time.Second
)

// ErrBusy marks a draft refused because this replica is already running as many
// as it will. It is a sentinel so an HTTP surface can answer 503 with a retry
// rather than reporting the platform as broken.
var ErrBusy = errors.New("too many draft runs are already executing; try again in a moment")

// Identity is the person a draft executes as. It is copied from a caller the
// platform has already authenticated — never synthesized here — which is what
// makes the no-new-authority property structural rather than a promise.
type Identity struct {
	UserID   string
	Email    string
	Roles    []string
	AuthType string
	Claims   map[string]any
}

// Request is one draft execution: the code, what to call it, and the values it
// binds.
type Request struct {
	// Source is the Starlark to execute. It is passed explicitly rather than
	// read from a record because the editor's whole purpose is running an edit
	// that has not been saved.
	Source string
	// Name labels the script in tracebacks.
	Name string
	// Params is the already-bound parameter set. Binding is the domain's
	// (script.BindParams) and happens before a Runner is involved, so a draft
	// and a platform run bind by one rule.
	Params   map[string]any
	Identity Identity
}

// Outcome is what one draft execution did. Failure is a normal outcome and is
// carried here rather than returned as an error: a failed draft's log is the
// whole reason to have run it.
type Outcome struct {
	// RunID identifies the run, and is also the session id every audit row the
	// run produced carries.
	RunID string
	// Result is the engine's record of the execution, present even when the
	// script failed.
	Result *scriptrun.Result
	// Err is the script's own failure — a Starlark error, a refused host call,
	// or a limit — and nil when it succeeded.
	Err error
}

// Failed reports whether the script itself failed.
func (o *Outcome) Failed() bool { return o != nil && o.Err != nil }

// Runner executes drafts against an assembled MCP server.
type Runner struct {
	server *mcp.Server
	// destinations is the configured bucket destination set export names
	// resolve against.
	destinations []script.Destination
	// slots bounds concurrent executions on this replica. A buffered channel
	// rather than a mutex because a caller must be able to give up waiting.
	slots chan struct{}
	// now pins the fire time handed to the script. It is a field so a test can
	// assert on what a draft reads as run.fire_time.
	now func() time.Time
}

// New builds a Runner over the assembled server. destinations is the
// deployment's configured bucket destination set, resolved by a draft exactly
// as a platform run resolves it, so a destination a real run would refuse
// fails while the author is iterating. A nil server yields a Runner that
// refuses every request, which is the honest shape for a deployment with no
// server to run against.
func New(server *mcp.Server, destinations []script.Destination) *Runner {
	return &Runner{
		server:       server,
		destinations: destinations,
		slots:        make(chan struct{}, maxConcurrentDrafts),
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// ErrNoIdentity marks a draft request carrying nobody to run as. A draft has no
// identity of its own, so there is nothing to fall back to.
var ErrNoIdentity = errors.New("a draft run needs an authenticated caller to run as")

// Run executes one draft and returns its outcome.
//
// The returned error is the platform's — no server, no identity, a session that
// could not be opened. The script's own failure is in the outcome.
func (r *Runner) Run(ctx context.Context, req Request) (*Outcome, error) {
	if r == nil || r.server == nil {
		return nil, errors.New("script execution is unavailable on this deployment")
	}
	if req.Identity.UserID == "" {
		return nil, ErrNoIdentity
	}
	// The run id is minted BEFORE the session, because it IS the session: it is
	// threaded onto the session context so every platform call the run makes
	// records the same session id in audit. Without it a run issuing three
	// queries would write three unrelated ids and nothing would group them, and
	// the id handed back would appear in no audit row at all.
	runID, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		return nil, fmt.Errorf("minting a run id: %w", err)
	}
	release, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	caller, cleanup, err := r.connect(ctx, runID, req.Identity)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	result, runErr := scriptrun.Run(ctx, scriptrun.Options{
		Source: req.Source, Name: req.Name, RunID: runID,
		// The fire time is pinned here, once, and handed to the script as
		// run.fire_time: even a draft never reads a clock, so what an author
		// verifies in the loop is what a scheduled run will do.
		FireTime: r.now(), Params: req.Params, Caller: caller,
		Destinations: r.destinations,
	})
	return &Outcome{RunID: runID, Result: result, Err: runErr}, nil
}

// acquire takes one of this replica's execution slots, giving up rather than
// queueing indefinitely: the person who pressed the button is waiting on the
// answer, and a request held open past their patience is worse than one that
// says to try again.
func (r *Runner) acquire(ctx context.Context) (func(), error) {
	timer := time.NewTimer(slotWait)
	defer timer.Stop()
	select {
	case r.slots <- struct{}{}:
		return func() { <-r.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a draft execution slot: %w", ctx.Err())
	case <-timer.C:
		return nil, ErrBusy
	}
}

// connect opens the in-memory session the draft's platform calls travel over,
// carrying the caller's identity and tagged as a script run.
//
// The source tag buys exactly two things, neither of them authority: audit rows
// that say a script ran, and the per-run session identity that keeps the run
// out of the caller's own discovery and gate state.
//
// A platform run differs from this in exactly one respect — it authenticates
// as the script principal with the version author's captured roles — which is why the
// session plumbing itself lives in scriptrun and only the identity is decided
// here.
func (r *Runner) connect(ctx context.Context, runID string, id Identity) (*scriptrun.SessionCaller, func(), error) {
	serverCtx := middleware.WithSource(ctx, middleware.SourceScript)
	serverCtx = pkgsession.WithAwareSessionID(serverCtx, runID)
	serverCtx = middleware.WithPreAuthenticatedUser(serverCtx, &middleware.UserInfo{
		UserID:   id.UserID,
		Email:    id.Email,
		Claims:   id.Claims,
		Roles:    id.Roles,
		AuthType: id.AuthType,
	})
	caller, cleanup, err := scriptrun.Connect(serverCtx, r.server, clientLabel)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the draft's session: %w", err)
	}
	return caller, cleanup, nil
}
