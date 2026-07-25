package pkcell

import (
	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// The questions cells put to an agent. Each names what answering it
// requires from the world, so whether it is answerable in a given world is
// computed rather than asserted, and each records whether trusting a stale
// belief about it costs accuracy or only effort.

// Separation is what a question's cells can distinguish. It is recorded
// per question rather than assumed, because two of these questions cannot
// separate on accuracy and saying so here is cheaper than discovering it
// in the analysis.
type Separation string

const (
	// SeparatesAccuracy means trusting a stale belief yields a
	// mechanically wrong final answer: a refusal where an answer exists, a
	// value where none does, or the wrong number.
	SeparatesAccuracy Separation = "accuracy"
	// SeparatesVerificationOnly means trusting a stale belief still
	// reaches the right answer, by a longer route. The question is a
	// control for the verification rate and contributes nothing to an
	// accuracy contrast.
	SeparatesVerificationOnly Separation = "verification_only"
)

// Question is one thing an agent is asked.
type Question struct {
	// ID is unique across the set.
	ID string `json:"id"`
	// BeliefID is the belief this question is about. A cell pairs them.
	BeliefID string `json:"belief_id"`
	// Prompt is what the agent is asked.
	Prompt string `json:"prompt"`
	// Separates records what this question's cells can distinguish.
	Separates Separation `json:"separates"`
	// NeedsMonitors is true when answering requires at least one
	// provisioned monitor and access to the listening area. It is what
	// makes answerability a function of the world rather than a label.
	NeedsMonitors bool `json:"needs_monitors"`
	// Budget caps tool calls for the attempt.
	Budget int `json:"budget"`
}

// cellBudget is generous enough that an agent which decides to verify can
// afford to: a budget that made verification expensive would confound the
// study's own cost term.
const cellBudget = 25

// Questions returns the committed question set.
func Questions() []Question {
	return []Question{
		{
			ID: "trend-volume", BeliefID: "perishable-absent",
			Prompt: "What was the total mention volume across ACME's listening monitors " +
				"between 1 June and 28 June 2026?",
			Separates: SeparatesAccuracy, NeedsMonitors: true, Budget: cellBudget,
		},
		{
			ID: "trend-sentiment", BeliefID: "perishable-absent",
			Prompt: "What was the average sentiment score across ACME's listening monitors " +
				"between 1 June and 28 June 2026?",
			Separates: SeparatesAccuracy, NeedsMonitors: true, Budget: cellBudget,
		},
		{
			ID: "monitor-count", BeliefID: "perishable-present",
			Prompt: "How many listening monitors does ACME have provisioned?",
			// Answerable in every world: zero is an answer. Trusting a
			// stale belief here yields the wrong number rather than a
			// wrong refusal, which is the other direction the study needs.
			Separates: SeparatesAccuracy, NeedsMonitors: false, Budget: cellBudget,
		},
		{
			ID: "weekly-impressions", BeliefID: "durable-granularity",
			Prompt: "What were the impressions for ACME's main owned profile in the first " +
				"week of June 2026 (1 June to 7 June)?",
			// The durable control. Both contract versions expose the same
			// underlying days, so an agent trusting a stale belief about
			// the granularity parameter still reaches the right total, by
			// summing rather than by asking. Its cells measure whether a
			// treatment raises verification where staleness is low, which
			// is the discriminant half of H3, and they are excluded from
			// accuracy contrasts.
			Separates: SeparatesVerificationOnly, NeedsMonitors: false, Budget: cellBudget,
		},
		{
			ID: "unique-reach", BeliefID: "eternal-unique-reach",
			Prompt: "How many distinct accounts did ACME's main owned profile reach in total " +
				"between 1 June and 28 June 2026?",
			// The eternal control. The belief is true in every world, so
			// verification is never rational here and a treatment that
			// raises it is adding noise rather than calibrating. The
			// question still carries a real trap (summing daily uniques),
			// so a wrong answer is possible without staleness.
			Separates: SeparatesVerificationOnly, NeedsMonitors: false, Budget: cellBudget,
		},
	}
}

// AnswerableIn reports whether the question can be answered from the world
// as it stands. It is computed from the world, never labeled on the cell,
// so a cell's correct behavior cannot drift from what the fixture actually
// serves.
func (q Question) AnswerableIn(w apigen.World) bool {
	if !q.NeedsMonitors {
		return true
	}
	return w.Listening == apigen.AccessGranted && w.Monitors > 0
}
