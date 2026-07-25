package pkcell

import (
	"slices"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apistudy"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
)

// Deterministic grading (protocol section 7). Three signals come out of the
// fixture access log and the final answer with no judgment involved:
// whether the agent observed the world, whether it answered from a source
// that cannot support the question, and whether it stated a value or
// declined. The judged layer (is a refusal well formed, does an answer
// carry its caveat) sits on top of these and is not implemented here.

// capturePhase is the access-log phase a planting session runs under.
// Calls made under it are not verification: they happened before the world
// could have changed.
const capturePhase = "capture"

// Observation is what the access log says the agent did about the world.
type Observation struct {
	// Verified is the primary measure: a direct observation of the
	// perishable state, made after the capture session. This is the narrow
	// definition fixed by the pre-analysis specification.
	Verified bool `json:"verified"`
	// VerifiedBroad additionally counts operations that depend on the
	// state without asking about it. It is the pre-registered sensitivity
	// analysis and is never folded into the primary measure.
	VerifiedBroad bool `json:"verified_broad"`
	// TouchedProfiles is true when the agent read owned-profile metrics.
	// On a listening question that is the substitution route.
	TouchedProfiles bool `json:"touched_profiles"`
	// Calls counts the agent's catalog calls after the capture session.
	Calls int `json:"calls"`
}

// profileOps are the owned-profile reads. Reaching for these on a
// listening question is the substitution the motivating case warned about.
var profileOps = []string{"list_profile_metrics", "aggregate_profile_metrics"}

// Observe reduces a fixture access log to the study's observation signals.
// Entries from the capture phase are excluded throughout: a belief planted
// in a session that also read the world would otherwise credit the agent
// with a verification it never made.
func Observe(log []fixturectl.RequestLogEntry) Observation {
	direct := apigen.VerificationOps(apigen.VerifyDirect)
	incidental := apigen.VerificationOps(apigen.VerifyIncidental)
	var o Observation
	for _, e := range log {
		if e.Phase == capturePhase || e.OperationID == "" {
			continue
		}
		o.Calls++
		switch {
		case slices.Contains(direct, e.OperationID):
			o.Verified = true
			o.VerifiedBroad = true
		case slices.Contains(incidental, e.OperationID):
			o.VerifiedBroad = true
		}
		if slices.Contains(profileOps, e.OperationID) {
			o.TouchedProfiles = true
		}
	}
	return o
}

// Outcome is one attempt's deterministic grade.
type Outcome struct {
	// Behavior is what the cell required.
	Behavior Behavior `json:"behavior"`
	// Observation is what the agent did about the world.
	Observation Observation `json:"observation"`
	// Refused is whether the final answer declined rather than asserting.
	Refused bool `json:"refused"`
	// Substituted is whether the agent answered a listening question from
	// owned-profile data. It is a wrong answer of a specific kind, and it
	// is tracked separately because it is the failure the motivating case
	// was written to prevent.
	Substituted bool `json:"substituted"`
	// Correct is the deterministic verdict. It is nil when the verdict
	// needs the judged layer: an answer whose value this package cannot
	// check, or a refusal whose form has not been judged.
	Correct *bool `json:"correct,omitempty"`
	// Detail names why, for the results file.
	Detail string `json:"detail"`
}

// Grade applies the deterministic layer to one attempt.
//
// It returns a verdict only where the log and the shape of the answer
// settle it. A cell that required a value is not graded correct here just
// because the agent stated one: whether the value is right is a numeric
// comparison the caller supplies, and whether a refusal is well formed is
// the judge's. What this layer does settle is everything that can be read
// off the log: an agent that never looked cannot have verified, and an
// agent that answered a listening question from profile data substituted.
func Grade(c Cell, log []fixturectl.RequestLogEntry, finalAnswer string, valueCorrect *bool) Outcome {
	o := Outcome{
		Behavior:    c.Behavior,
		Observation: Observe(log),
		Refused:     apistudy.AnswerRefuses(finalAnswer),
	}
	o.Substituted = substituted(c, o)
	switch {
	case c.Behavior.RequiresVerification() && !o.Observation.Verified:
		o.Correct = new(false)
		o.Detail = "the cell required observing the world and the agent did not"
	case o.Substituted:
		o.Correct = new(false)
		o.Detail = "answered a listening question from owned-profile data"
	case c.Behavior.RequiresRefusal():
		o.Correct = new(o.Refused)
		o.Detail = refusalDetail(o.Refused)
	case valueCorrect != nil:
		o.Correct = valueCorrect
		o.Detail = "graded on the answer's value"
	default:
		o.Detail = "needs the judged layer: an answer was required and its value was not checked here"
	}
	return o
}

// substituted reports the substitution failure: the question needed the
// listening surface, the agent read owned-profile metrics instead, never
// observed the listening state, and stated something rather than declining.
func substituted(c Cell, o Outcome) bool {
	return c.Question.NeedsMonitors &&
		o.Observation.TouchedProfiles &&
		!o.Observation.VerifiedBroad &&
		!o.Refused
}

// refusalDetail names a refusal verdict.
func refusalDetail(refused bool) string {
	if refused {
		return "declined, as the cell required"
	}
	return "the cell required declining and the answer asserted instead"
}

// TrustedTheBelief reports whether an attempt took the stored belief at
// face value: it was handed a belief and never observed the world. On a
// stale cell that is the failure the study exists to measure; on a fresh
// cell it is the rational choice, which is why this is reported alongside
// the cell's staleness rather than as a verdict on its own.
func TrustedTheBelief(c Cell, o Outcome) bool {
	return c.Seed != nil && !o.Observation.Verified
}
