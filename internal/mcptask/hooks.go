package mcptask

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type claimKey struct{}

// Claim is how a tool that keeps its own durable record (run_script) turns a
// call into a task: it finds a Claim on its context when the client declared
// the extension, starts its work without waiting for it, and accepts the
// claim with the id of the record it created. A call whose tool never
// accepts, a refusal for instance, is answered with the tool's own result.
type Claim struct {
	mu sync.Mutex
	id string
}

// Requested returns the Claim a call to become a task carries, or nil when
// the call is to be answered with its result.
func Requested(ctx context.Context) *Claim {
	c, _ := ctx.Value(claimKey{}).(*Claim)
	return c
}

// Accept names the task the call became. The id must already resolve through
// its Source: the extension forbids handing out a task a tasks/get cannot
// find.
func (c *Claim) Accept(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.id = id
}

func (c *Claim) accepted() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.id
}

func withClaim(ctx context.Context) (context.Context, *Claim) {
	c := &Claim{}
	return context.WithValue(ctx, claimKey{}, c), c
}

type completionKey struct{}

// CompletionFunc receives the outcome of a tools/call that was answered with
// a task, once the work behind it ends. ctx carries the PlatformContext the
// work ran under.
type CompletionFunc func(ctx context.Context, res mcp.Result, err error)

// WithCompletion installs fn as the receiver of the outcome of a call that is
// answered with a task. The audit layer installs one: a task's answer is a
// handle, and the row it writes is for what the call did, which for a call
// executed apart from its request is known only when that work ends.
func WithCompletion(ctx context.Context, fn CompletionFunc) context.Context {
	return context.WithValue(ctx, completionKey{}, fn)
}

// Complete hands the outcome of a task's work to the receiver installed on
// ctx, if any.
func Complete(ctx context.Context, res mcp.Result, err error) {
	if fn, ok := ctx.Value(completionKey{}).(CompletionFunc); ok && fn != nil {
		fn(ctx, res, err)
	}
}
