package callrun

import (
	"context"
	"log/slog"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptnotify"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// notices are the messages a call's handlers queued with platform.notify
// (#2436), sent once the call has been answered, denied or held.
type notices struct {
	mu    sync.Mutex
	items []notice
	// sent marks a list already sent: what a handler queues on it later
	// (a transform over a background export's pages) is sent at once.
	sent bool
}

type notice struct {
	t       target
	payload map[string]any
}

type noticesKey struct{}

// withNotices carries a fresh list of a call's queued messages.
func withNotices(ctx context.Context) (context.Context, *notices) {
	n := &notices{}
	return context.WithValue(ctx, noticesKey{}, n), n
}

// queue keeps a handler run's messages on the call's list, or sends them at
// once when the call carries none.
func (e *Evaluator) queue(ctx context.Context, t target, payloads []map[string]any) {
	if len(payloads) == 0 {
		return
	}
	n, _ := ctx.Value(noticesKey{}).(*notices)
	if n == nil {
		n = &notices{}
	}
	n.mu.Lock()
	for _, p := range payloads {
		n.items = append(n.items, notice{t: t, payload: p})
	}
	late := n.sent
	n.mu.Unlock()
	if late || ctx.Value(noticesKey{}) == nil {
		e.flush(ctx, n)
	}
}

// flush sends a call's queued messages, each through notify in a session of
// the handler's own run identity, after the call: on its own goroutine, on a
// context the call's end does not cancel. A message that fails is logged.
func (e *Evaluator) flush(ctx context.Context, n *notices) {
	n.mu.Lock()
	items := n.items
	n.items, n.sent = nil, true
	n.mu.Unlock()
	if len(items) == 0 || e.server == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		for _, it := range items {
			e.send(detached, it)
		}
	}()
}

// send sends one queued message as the handler that queued it.
func (e *Evaluator) send(ctx context.Context, it notice) {
	sessionID, err := pkgsession.GenerateScriptSessionID()
	if err == nil {
		var caller scriptrun.Caller
		var cleanup func()
		if caller, cleanup, err = e.connect(ctx, it.t, sessionID); err == nil {
			_, err = caller.CallTool(ctx, scriptnotify.Tool, it.payload)
			cleanup()
		}
	}
	if err != nil {
		slog.WarnContext(ctx, "handlers: sending a message a handler queued failed", "script", it.t.script.Name,
			"error", logsan.SanitizeForLog(err.Error()))
	}
}
