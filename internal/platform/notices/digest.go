// Package notices assembles the per-user session-start briefing that
// platform_info carries (#1278).
//
// The platform already tells a session about pending insights. Nothing told a
// person that feedback had been left on work they own, or that something new
// had been shared with them: the portal activity feed was the only surface that
// carried either, so a user who works through MCP sessions and never opens the
// portal discovered neither. This package answers one question for one caller —
// what is waiting for you that you have not been shown — and platform_info
// attaches the answer to the first call of the session.
//
// "Have not been shown" is a watermark, not a read receipt: the digest advances
// it as it is delivered, so what a session is told about is not repeated to the
// next one. That makes delivery a single shot, which is why the agent
// instructions that ship alongside tell the agent to relay the digest rather
// than act on it silently.
//
// Construction takes explicit inputs — the portal asset, share and thread
// stores plus a watermark store — so the digest is buildable and testable
// without a Platform. New returns nil when any of them is missing (no database,
// no portal), and every method on a nil Handle answers empty, so a deployment
// without a portal simply carries no notices.
package notices

import "time"

// Digest is one caller's session-start briefing: feedback left on work they own
// and artifacts newly shared with them, both bounded to what has arrived since
// they were last briefed. It is nil, rather than empty, when there is nothing
// to say, so platform_info omits the block entirely.
type Digest struct {
	// Since is the watermark this digest covers, RFC3339. Everything reported
	// arrived after it.
	Since string `json:"since"`
	// Feedback lists unresolved threads on assets the caller owns, newest
	// activity first, capped at maxFeedbackNotices.
	Feedback []FeedbackNotice `json:"feedback,omitempty"`
	// FeedbackTotal is how many such threads exist. It exceeds len(Feedback)
	// when the cap truncated the list, so the agent can say how many were not
	// named rather than implying the list is complete.
	FeedbackTotal int `json:"feedback_total,omitempty"`
	// NewShares lists artifacts shared with the caller since the watermark,
	// newest first, capped at maxShareNotices.
	NewShares []ShareNotice `json:"new_shares,omitempty"`
	// NewSharesTruncated records that more shares arrived than the list holds.
	// The share query is bounded rather than counted, so the digest reports
	// "at least this many" rather than presenting a truncated list as the whole
	// set -- an unknown remainder is its own state, not zero.
	NewSharesTruncated bool `json:"new_shares_truncated,omitempty"`
}

// FeedbackNotice is one unresolved feedback thread on an asset the caller owns,
// left by someone else. It carries enough to relay the thread without a further
// call, and the reference needed to read it in full.
type FeedbackNotice struct {
	ThreadID string `json:"thread_id"`
	// Kind is the thread's classification (comment, question, correction, ...).
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Title  string `json:"title,omitempty"`
	// AuthorEmail is who opened the thread; never the caller.
	AuthorEmail string `json:"author_email,omitempty"`
	AssetID     string `json:"asset_id"`
	AssetName   string `json:"asset_name,omitempty"`
	// AssetReference dereferences the asset the thread is on, for `fetch`.
	AssetReference string `json:"asset_reference"`
	// LastActivityAt is when the thread last moved, RFC3339.
	LastActivityAt string `json:"last_activity_at"`
}

// ShareNotice is one artifact newly shared with the caller.
type ShareNotice struct {
	// Kind is asset, collection, or prompt.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Reference dereferences the artifact, for `fetch`.
	Reference string `json:"reference"`
	// SharedBy is the person who made the grant, falling back to the artifact's
	// owner for a legacy row that recorded no creator.
	SharedBy string `json:"shared_by,omitempty"`
	// SharedAt is when the grant was made, RFC3339.
	SharedAt string `json:"shared_at"`
	// Permission is viewer or editor.
	Permission string `json:"permission,omitempty"`
}

// Counts returns how many feedback threads and new shares the digest reports.
// Feedback is the true total (the thread query counts what it did not return);
// shares is what the list holds, which NewSharesTruncated marks as a floor. It
// is what the agent instructions are sized from, and answers zero, zero for a
// nil digest so the caller needs no nil check.
func (d *Digest) Counts() (feedback, shares int) {
	if d == nil {
		return 0, 0
	}
	return d.FeedbackTotal, len(d.NewShares)
}

// empty reports whether the digest has nothing to say, in which case it is not
// delivered and the watermark does not move.
func (d *Digest) empty() bool {
	return len(d.Feedback) == 0 && len(d.NewShares) == 0
}

// stamp formats a time for the digest's RFC3339 fields.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
