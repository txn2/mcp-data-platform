package feedbackapi

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"

	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// resolveMentions returns the eligible mentions in a thread event's body.
func (h *Handler) resolveMentions(ctx context.Context, t *threads.Thread, body, author string) []string {
	if h.cfg.Mentions == nil || body == "" {
		return nil
	}
	return h.cfg.Mentions.ResolveMentions(ctx, t.TargetType, t.TargetID(), body, author)
}

// stampMentions records mentions on the event about to be written, so they are
// stored in the same insert as the body that names them rather than by a
// follow-up update that could fail on its own. A metadata document that cannot
// be re-encoded leaves the event unchanged: the comment must still post.
func stampMentions(e threads.ThreadEvent, mentions []string) threads.ThreadEvent {
	merged, err := mention.WithMentions(e.Metadata, mentions)
	if err != nil {
		slog.Warn("mention: stamping event metadata failed; posting without mentions", // #nosec G706 -- structured slog call; error sanitized
			"error", logsan.SanitizeForLog(err.Error()))
		return e
	}
	e.Metadata = merged
	return e
}
