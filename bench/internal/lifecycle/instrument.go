package lifecycle

import (
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// Per-stage diagnosis instrumentation (issue #964). These helpers derive
// stage-level signals from the provider-agnostic transcript, so they work
// identically for the in-process loop and the claude-cli path (both produce the
// same []llm.Message). Keeping the derivation transcript-based (rather than
// hooking each execution path) means the loop and CLI paths cannot diverge on
// what "the teacher called capture" or "the fact surfaced" means.

// recordInstrumentation fills an episode's per-stage diagnosis fields from its
// transcript (issue #964): whether the capture tool was called, whether the
// budget was exhausted, and — when a surface target is set — whether the fact
// surfaced in a tool result. Both episode paths (loop and claude-cli) call this
// with the transcript they produced, so the two paths cannot diverge on the
// derivation.
func (rec *EpisodeRecord) recordInstrumentation(spec episodeSpec, transcript []llm.Message, budgetExhausted bool) {
	rec.BudgetExhausted = budgetExhausted
	rec.CaptureAttempted = captureToolCalled(transcript)
	if spec.surfaceFact != "" {
		s := factSurfaced(spec.surfaceFact, transcript)
		rec.FactSurfaced = &s
	}
}

// captureToolCalled reports whether the episode actually executed a knowledge
// capture call. A capture request the budget refused (emitted only after the
// tool-call budget was spent, so it never ran) does NOT count: it is a
// budget-starvation miss, not an "attempted capture that failed to land". This
// keeps the capture-budget diagnosis honest — a teacher that only reaches for
// capture after burning its budget on discovery is starved, not a landing
// failure. It distinguishes a capture miss caused by never reaching an executed
// capture (budget starvation) from one where capture ran but the insight did
// not land.
func captureToolCalled(msgs []llm.Message) bool {
	captureIDs := map[string]bool{}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if isCaptureTool(c.Name) {
				captureIDs[c.ID] = true
			}
		}
	}
	if len(captureIDs) == 0 {
		return false
	}
	for _, m := range msgs {
		for _, r := range m.ToolResults {
			// A capture call ran when its paired result is present and is not the
			// budget-refusal sentinel. A server-side capture error still counts as
			// executed (that is a landing failure, a different bucket).
			if captureIDs[r.CallID] && r.Text != agent.BudgetRefusalText {
				return true
			}
		}
	}
	return false
}

// isCaptureTool reports whether a tool name is the knowledge-capture tool. The
// suffix match tolerates a renamed or namespaced capture tool without silently
// classifying every teach episode as a capture miss.
func isCaptureTool(name string) bool {
	return name == "memory_capture" || strings.HasSuffix(name, "_capture")
}

// factSurfaced reports whether the promoted fact appeared in any tool result the
// episode saw. Combined with answer correctness it decomposes the transfer gap:
// a promoted fact that never surfaces is a delivery failure (enrichment/search
// did not carry it to the second identity), while one that surfaces but is not
// used is a reasoning failure (the agent had it and ignored it). Matching is on
// the normalized fact text; the datahub sink applies the fact verbatim as the
// entity description, so cross-enrichment returns it verbatim.
func factSurfaced(fact string, msgs []llm.Message) bool {
	needle := normalizeText(fact)
	if needle == "" {
		return false
	}
	for _, m := range msgs {
		for _, r := range m.ToolResults {
			if r.IsError {
				continue
			}
			if strings.Contains(normalizeText(r.Text), needle) {
				return true
			}
		}
	}
	return false
}

// normalizeText lowercases and collapses every run of non-alphanumeric bytes to
// a single space, so surfacing detection is robust to whitespace, casing, and
// punctuation differences between the stored fact and its rendering in a tool
// result. It intentionally does not stem or reorder tokens: the surfaced signal
// must stay a conservative "the fact text is present", not a fuzzy paraphrase
// match that could over-report delivery.
func normalizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // leading spaces are trimmed by never emitting them
	for _, r := range strings.ToLower(s) {
		if isAlphaNum(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// isAlphaNum reports whether a rune is an ASCII letter or digit.
func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// surfacedTarget returns the promoted content whose presence in a tool result
// marks the transfer fact as surfaced: the datahub sink applies the fact as the
// entity description, and the knowledge-page sink applies the page body. Both
// are what cross-enrichment / search deliver to the second identity.
func surfacedTarget(p protocol.Protocol) string {
	if p.Sink == protocol.SinkKnowledgePage && p.Page != nil {
		return p.Page.Body
	}
	return p.Fact
}
