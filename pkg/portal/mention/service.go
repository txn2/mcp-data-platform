package mention

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// MetadataKey is the thread-event metadata key holding the eligible mentions
// of that event, as an array of lower-cased addresses. It is a key inside the
// existing metadata column rather than a table of its own: a mention has no
// state beyond the event that carries it.
const MetadataKey = "mentions"

// Service resolves the mentions a comment body may deliver on a thread target.
// It is the write path's single entry point: both the portal REST handlers and
// the MCP feedback tool run a body through it, so a mention behaves the same
// whichever surface authored it.
type Service struct {
	audience *Audience
}

// NewService builds the resolver over an audience.
func NewService(audience *Audience) *Service {
	return &Service{audience: audience}
}

// ResolveMentions returns the addresses named in body that may actually be
// mentioned on this target: parsed from the token grammar, minus the author's
// own address, then filtered to the target's audience. A named address outside
// the audience is dropped here, which leaves it ordinary text in the stored
// body -- it is rendered plain and notifies nobody, so a comment can never
// carry an item title and an excerpt to someone who cannot open the item.
//
// The author is dropped because the notification path already refuses to mail
// someone their own comment; recording the mention anyway would put the thread
// in their own mentions inbox and render a chip claiming a delivery that never
// happened.
//
// It never fails the write it serves: an audience lookup error is logged and
// yields no mentions, exactly like a body that named nobody. A nil Service
// (mentions unavailable, e.g. no database) resolves nothing.
func (s *Service) ResolveMentions(ctx context.Context, targetType, targetID, body, author string) []string {
	if s == nil || s.audience == nil {
		return nil
	}
	named := withoutAuthor(Emails(Scan(body)), author)
	if len(named) == 0 {
		return nil
	}
	eligible, err := s.audience.Eligible(ctx, Target{Type: targetType, ID: targetID}, named)
	if err != nil {
		slog.Warn("mention: audience lookup failed; treating mentions as plain text", // #nosec G706 -- structured slog call; values sanitized
			"target_type", logsan.SanitizeForLog(targetType),
			"error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return eligible
}

// withoutAuthor drops the author's own address from the named set.
func withoutAuthor(named []string, author string) []string {
	author = normalize(author)
	if author == "" {
		return named
	}
	out := make([]string, 0, len(named))
	for _, email := range named {
		if email != author {
			out = append(out, email)
		}
	}
	return out
}

// WithMentions returns metadata carrying emails under MetadataKey, preserving
// every other key already stored. Passing no addresses returns metadata
// unchanged, so an event that mentions nobody keeps exactly the metadata its
// writer built.
func WithMentions(metadata json.RawMessage, emails []string) (json.RawMessage, error) {
	if len(emails) == 0 {
		return metadata, nil
	}
	fields := map[string]json.RawMessage{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &fields); err != nil {
			return nil, fmt.Errorf("reading event metadata: %w", err)
		}
	}
	encoded, err := json.Marshal(emails)
	if err != nil {
		return nil, fmt.Errorf("encoding mentions: %w", err)
	}
	fields[MetadataKey] = encoded
	merged, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encoding event metadata: %w", err)
	}
	return merged, nil
}

// FromMetadata returns the mentions recorded on an event, or nil when it
// carries none. Malformed metadata reads as no mentions: the timeline must
// render regardless.
func FromMetadata(metadata json.RawMessage) []string {
	if len(metadata) == 0 {
		return nil
	}
	var fields struct {
		Mentions []string `json:"mentions"`
	}
	if err := json.Unmarshal(metadata, &fields); err != nil {
		return nil
	}
	return fields.Mentions
}

// ContainmentFilter returns the JSON document matching events that mention
// email, for a jsonb containment test against the metadata mentions array
// (metadata -> 'mentions' @> filter). It is the query counterpart of
// WithMentions, kept beside it so the stored shape and the query that finds it
// cannot drift apart.
func ContainmentFilter(email string) string {
	encoded, err := json.Marshal([]string{normalize(email)})
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
