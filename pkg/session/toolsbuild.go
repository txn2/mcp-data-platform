package session

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// A client keeps the tool list it read and validates results against it. After
// a restart onto another build it reconnects to the session it had, whose
// tools may have changed, and nothing tells it so: the SDK sends
// notifications/tools/list_changed only when a running server adds or removes
// a tool. The session records the build whose tool list it was last told
// about, and a session arriving on another build is told once (#1946).
const (
	// toolsBuildKey is the session state key holding that build.
	toolsBuildKey = "tools_build"
	// toolsPendingKey is set when the notification was published from a
	// request, with no stream of the session's known to be open to carry it:
	// the session's next stream sends it again.
	toolsPendingKey = "tools_pending"
	// toolsListChanged is the notification a changed tool list is announced by.
	toolsListChanged = "notifications/tools/list_changed"
)

// newState is a new session's state: it lists tools under the running build,
// so it has nothing to be told.
func (h *AwareHandler) newState() map[string]any {
	state := make(map[string]any)
	if h.build != "" {
		state[toolsBuildKey] = h.build
	}
	return state
}

// announceToolsChanged sends notifications/tools/list_changed to one session
// when the build it last heard about is not the running one, and records that
// it was told. A session that holds no build (one created before builds were
// recorded, or revived after its row expired) may hold a list from any build,
// so it is told too. streaming is whether the session's stream is open to
// carry the notification; a request with none leaves the notification pending
// for the stream the session opens next.
func (h *AwareHandler) announceToolsChanged(ctx context.Context, sess *Session, streaming bool) {
	if h.build == "" || h.broadcaster == nil || sess == nil {
		return
	}
	recorded, _ := sess.State[toolsBuildKey].(string)
	pending, _ := sess.State[toolsPendingKey].(bool)
	if recorded == h.build && (!pending || !streaming) {
		return
	}
	if err := h.store.UpdateState(ctx, sess.ID, map[string]any{toolsBuildKey: h.build, toolsPendingKey: !streaming}); err != nil {
		slog.Warn("session: recording the announced build failed",
			sessionIDKey, logsan.SanitizeForLog(sess.ID), slogKeyError, logsan.SanitizeForLog(err.Error()))
		return
	}
	if err := h.broadcaster.Publish(ctx, Event{Method: toolsListChanged, SessionID: sess.ID}); err != nil {
		slog.Warn("session: announcing a changed tool list failed",
			sessionIDKey, logsan.SanitizeForLog(sess.ID), slogKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// announceOnStream is announceToolsChanged for a session whose stream just
// opened, read fresh because the request that opened it carries no session.
func (h *AwareHandler) announceOnStream(ctx context.Context, sessionID string) {
	if h.build == "" {
		return
	}
	sess, err := h.store.Get(ctx, sessionID)
	if err != nil || sess == nil {
		return
	}
	h.announceToolsChanged(ctx, sess, true)
}

// deliverable reports whether the stream of sessionID carries ev: an event
// addressed to a session reaches that session's streams alone.
func deliverable(ev Event, sessionID string) bool {
	return ev.SessionID == "" || ev.SessionID == sessionID
}
