package lifecycle

import (
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/capture"
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
	rec.CaptureAttempted = capture.Attempted(transcript)
	if spec.surfaceFact != "" {
		s := factSurfaced(spec.surfaceFact, transcript)
		rec.FactSurfaced = &s
	}
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
// entity description (returned verbatim by cross-enrichment), and the
// knowledge-page sink is matched on the page SUMMARY — search renders a page
// hit as title plus summary, and the a3 tool surface has no page-body fetch
// tool, so the body can never appear in a tool result there; a body needle
// would report ~100% "not surfaced" even when delivery worked.
func surfacedTarget(p protocol.Protocol) string {
	if p.Sink == protocol.SinkKnowledgePage && p.Page != nil {
		return p.Page.Summary
	}
	return p.Fact
}
