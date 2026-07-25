package portal

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
)

// MentionResolver returns the addresses a comment body delivers an @-mention
// to on a thread target: the names written in the body, minus the author's own
// address, filtered to the people who can open that target. Implemented by
// pkg/portal/mention, which the composition root injects; a nil resolver
// disables mentions (no database), leaving every token ordinary text.
type MentionResolver interface {
	ResolveMentions(ctx context.Context, targetType, targetID, body, author string) []string
}

// resolveMentions returns the eligible mentions in a thread event's body.
func (h *Handler) resolveMentions(ctx context.Context, t *Thread, body, author string) []string {
	if h.deps.MentionResolver == nil || body == "" {
		return nil
	}
	return h.deps.MentionResolver.ResolveMentions(ctx, t.TargetType, t.TargetID(), body, author)
}

// stampMentions records mentions on the event about to be written, so they are
// stored in the same insert as the body that names them rather than by a
// follow-up update that could fail on its own. A metadata document that cannot
// be re-encoded leaves the event unchanged: the comment must still post.
func stampMentions(e ThreadEvent, mentions []string) ThreadEvent {
	merged, err := mention.WithMentions(e.Metadata, mentions)
	if err != nil {
		slog.Warn("mention: stamping event metadata failed; posting without mentions", // #nosec G706 -- structured slog call; error sanitized
			"error", logsan.SanitizeForLog(err.Error()))
		return e
	}
	e.Metadata = merged
	return e
}
