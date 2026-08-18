package instructions

import (
	"fmt"
	"strings"
)

// NoticesNote returns the runtime note that goes with a non-empty `notices`
// block on platform_info (#1278). feedback and shares are how many the digest
// reports, which is not always how many it lists: the note therefore describes
// the counts rather than claiming the block enumerates them.
//
// The notice digest is addressed to the person, not to the agent: it says that
// someone left feedback on their work, or gave them access to something. An
// agent that reads it and silently continues with the request has delivered
// nothing, so the note's job is to make relaying it the first thing that
// happens in the session. Delivery is single-shot — the digest advances the
// caller's watermark as it is issued — which is why the note says so rather
// than leaving the agent to assume it can come back to it later.
//
// Each list is capped, and the watermark advances past what did not fit, so the
// note also has to point at the portal as the surface that holds everything --
// otherwise a truncated list reads as the whole set.
//
// It returns the empty string when there is nothing to relay, so the caller can
// append it unconditionally, and it names `fetch` and `manage_feedback` only
// when the caller's persona can reach them.
func NoticesNote(accessibleTools []string, feedback, shares int) string {
	if feedback == 0 && shares == 0 {
		return ""
	}
	has := toolSet(accessibleTools)

	lines := []string{
		"Waiting for the person you are working for:",
		"This response carries a `notices` block. It is addressed to them, not to you. " +
			"Relay it before you start on their request, in your own words, and let them " +
			"decide what to do about it.",
	}
	if feedback > 0 {
		lines = append(lines, "- "+feedbackBullet(feedback, has))
	}
	if shares > 0 {
		lines = append(lines, "- "+shareBullet(shares, has[toolFetch]))
	}
	lines = append(lines,
		"- You are shown this once. The notices are cleared as this response is issued, "+
			"so anything you do not pass on now is not repeated to the next session.",
		"- This is a briefing, not an inbox. Each list is capped, so say that the portal "+
			"holds the full picture — its activity feed for feedback, Shared With Me for "+
			"shares — rather than presenting these entries as everything there is.")
	return strings.Join(lines, "\n")
}

// feedbackBullet describes the unaddressed-feedback half of the digest, naming
// the tools that read and answer a thread only when the caller can reach them.
func feedbackBullet(count int, has map[string]bool) string {
	b := fmt.Sprintf("`notices.feedback` — %s of still-unresolved feedback other people "+
		"left on assets they own, most recent first. Say who left it and what it asks, "+
		"then ask whether they want to deal with it now.", pluralize(count, "thread", "threads"))
	switch {
	case has[toolManageFeedback] && has[toolFetch]:
		return b + " Read the thread in full with `fetch` on the asset reference, and " +
			"answer or resolve it with `manage_feedback`."
	case has[toolManageFeedback]:
		return b + " Answer or resolve it with `manage_feedback`."
	case has[toolFetch]:
		return b + " Read the thread in full with `fetch` on the asset reference."
	default:
		return b + " Point them at the portal to answer it."
	}
}

// shareBullet describes the new-shares half of the digest.
func shareBullet(count int, hasFetch bool) string {
	b := fmt.Sprintf("`notices.new_shares` — %s someone gave them access to that they "+
		"have not been told about. Name each one and who shared it.", pluralize(count, "item", "items"))
	if hasFetch {
		b += " Read one with `fetch` on its `reference` when they ask for it."
	}
	return b
}

// pluralize renders a count with the right noun form, so the note reads as a
// sentence ("1 thread", "4 threads") rather than a template.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
