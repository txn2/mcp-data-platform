// Package listchanged provides a debounced, broadcaster-backed notifier that
// publishes a single MCP "*/list_changed" notification for a runtime-mutable
// entity set (prompts, managed resources) to every connected SSE long-poll
// subscriber, cross-replica via the session broadcaster's LISTEN/NOTIFY channel.
//
// The platform runs the MCP SDK server in stateless mode, so the SDK's native
// server-push channel never reaches the platform's SSE clients. The gateway
// toolkit already substitutes its own broadcaster-backed tools/list_changed
// signal (see pkg/toolkits/gateway); this package is the same pattern factored
// into one reusable owner so prompts and managed resources emit list_changed
// through the identical path without each subsystem re-implementing the debounce.
//
// A Notifier is bound to one JSON-RPC method (e.g.
// "notifications/prompts/list_changed") and one broadcaster. Notify schedules a
// debounced publish so a burst of writes (a bulk import) collapses into a single
// notification. All methods are safe for concurrent use and safe to call on a
// nil *Notifier, so a subsystem assembled before its broadcaster is wired can
// hold a nil notifier and every write path degrades to a no-op until SetBound.
package listchanged

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

const (
	// debounceWindow batches a flurry of writes (e.g. a bulk prompt import or a
	// multi-resource upload) into a single notification. Matches the gateway
	// toolkit's notifyDebounceWindow so every list_changed publisher on the
	// platform shares the same batching latency.
	debounceWindow = 50 * time.Millisecond

	// dispatchTimeout bounds the AfterFunc-spawned publish so a partitioned
	// downstream (postgres LISTEN connection hung, remote receiver blocked)
	// cannot leak a goroutine per write.
	dispatchTimeout = 5 * time.Second
)

// Broadcaster is the subset of session.Broadcaster the notifier needs: fan-out
// publish of a server-originated notification. *session.MemoryBroadcaster and
// the postgres broadcaster both satisfy it.
type Broadcaster interface {
	Publish(ctx context.Context, ev session.Event) error
}

// Notifier publishes a debounced list_changed notification for one method
// through one broadcaster. The zero value is not usable; construct with New.
type Notifier struct {
	b      Broadcaster
	method string

	mu     sync.Mutex
	timer  *time.Timer
	closed bool
}

// New builds a Notifier that publishes method (a fully-qualified JSON-RPC
// notification method such as "notifications/prompts/list_changed") through b.
// A nil broadcaster yields a Notifier whose Notify is a no-op, so callers need
// not special-case the no-broadcaster (e.g. no-database) deployment.
func New(b Broadcaster, method string) *Notifier {
	return &Notifier{b: b, method: method}
}

// Notify schedules a debounced publish. Multiple calls within debounceWindow
// collapse into a single fire. Safe for concurrent use and safe on a nil
// receiver or a nil broadcaster (both no-op). Never blocks on the broadcaster:
// the publish runs on a timer goroutine.
func (n *Notifier) Notify() {
	if n == nil || n.b == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	if n.timer == nil {
		n.timer = time.AfterFunc(debounceWindow, n.fire)
		return
	}
	// Reset reschedules the AfterFunc callback (Go Timer.Reset contract for
	// AfterFunc-created timers), extending the debounce window so the flurry
	// collapses to one dispatch.
	n.timer.Reset(debounceWindow)
}

// fire publishes the notification once the debounce window has elapsed. It
// clears the timer slot first so a Notify arriving during dispatch schedules a
// fresh window (bounding a burst to at most two dispatches, never zero).
func (n *Notifier) fire() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.timer = nil
	n.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
	defer cancel()
	// Best-effort: list_changed is a UX signal, not a correctness guarantee. A
	// publish on a closed broadcaster (shutdown race) returns an error that is
	// logged and swallowed rather than propagated.
	if err := n.b.Publish(ctx, session.Event{Method: n.method}); err != nil {
		slog.Warn("listchanged: publish failed",
			"method", n.method,
			"error", err)
	}
}

// Stop cancels any pending notification and makes future Notify calls no-ops.
// Idempotent and safe on a nil receiver. Wire it to the platform lifecycle so a
// pending debounce timer does not fire after shutdown.
func (n *Notifier) Stop() {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
}
