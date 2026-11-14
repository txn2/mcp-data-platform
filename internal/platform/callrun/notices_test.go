package callrun

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// notifyRecorder is the notify tool on the evaluator's server.
type notifyRecorder struct {
	mu  sync.Mutex
	got []map[string]any
	who []string
}

func (n *notifyRecorder) install(e *Evaluator) {
	mcp.AddTool(e.server, &mcp.Tool{Name: "notify"}, func(ctx context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.got = append(n.got, in)
		if u := middleware.GetPreAuthenticatedUser(ctx); u != nil {
			n.who = append(n.who, u.Email)
		}
		return nil, map[string]any{"sent": true}, nil
	})
}

func (n *notifyRecorder) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.got)
}

const notifySrc = `def decide(call):
    platform.notify("ops", "held call", body = call["operation"])
    return platform.require_approval([], "waits.")
`

// TestNotices: a policy's platform.notify is queued and sent after the call
// has been answered, as the handler's run identity; a dry decision (the test
// panel) sends nothing, and a run may queue at most five.
func TestNotices(t *testing.T) {
	w := newWorld()
	w.add("n", callhandler.KindPolicy, notifySrc, nil)
	f := newFixture(t, w)
	rec := &notifyRecorder{}
	rec.install(f.e)

	res, _ := call(t, f.connect(t, nil, ""), echoArgs)
	assert.True(t, res.IsError, "the call is held")
	require.Eventually(t, func() bool { return rec.count() == 1 }, 5*time.Second, 10*time.Millisecond)
	rec.mu.Lock()
	assert.Equal(t, "ops", rec.got[0]["channel"])
	assert.Equal(t, []string{"admin@example.com"}, rec.who, "sent as the administrator who approved the version")
	rec.mu.Unlock()

	ctx, list := withNotices(context.Background())
	v := f.e.Decide(ctx, sample(), "dry", true)
	assert.Equal(t, VerdictHeld, v.Outcome)
	assert.Empty(t, list.items, "a dry decision queues nothing on the call's list")
	assert.False(t, list.sent)

	w.add("many", callhandler.KindPolicy, "def decide(_call):\n    for i in range(6):\n        platform.notify(\"ops\", \"t%d\" % i)\n    return platform.allow()\n", nil)
	v = f.e.Decide(context.Background(), sample(), "c2", false)
	assert.Equal(t, VerdictHeld, v.Outcome, "the run past five messages fails, so its policy holds")
}

// TestNotices_QueuedLateAreSentAtOnce: a message queued on a list already sent
// (a transform over a background export) is sent rather than dropped; with no
// list at all it is sent at once.
func TestNotices_QueuedLateAreSentAtOnce(t *testing.T) {
	w := newWorld()
	h := w.add("n", callhandler.KindPolicy, allowSrc, nil)
	f := newFixture(t, w)
	rec := &notifyRecorder{}
	rec.install(f.e)
	tg, err := f.e.targetFor(context.Background(), h)
	require.NoError(t, err)

	ctx, n := withNotices(context.Background())
	f.e.flush(ctx, n)
	f.e.queue(ctx, tg, []map[string]any{{"channel": "ops", "title": "late"}})
	f.e.queue(context.Background(), tg, []map[string]any{{"channel": "ops", "title": "no list"}})
	f.e.queue(context.Background(), tg, nil)
	require.Eventually(t, func() bool { return rec.count() == 2 }, 5*time.Second, 10*time.Millisecond)
}
