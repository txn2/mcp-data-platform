package persona

import (
	"cmp"
	"fmt"
	"slices"
)

// Tool names the coherence rules pair. Kept as constants next to the rule
// table so a rename shows up here rather than as a silently dead rule.
const (
	toolSearch         = "search"
	toolFetch          = "fetch"
	toolMemoryCapture  = "memory_capture"
	toolApplyKnowledge = "apply_knowledge"
)

// coherenceRule states that a persona granting Grant while withholding
// Requires holds a capability it cannot complete. A rule describes an
// INCOHERENT tool set, not merely a narrow one: narrowing a persona is the
// point of personas, but granting the half of a pair that produces work and
// withholding the half that consumes it leaves the persona able to start
// something it can never finish.
//
// Adding a pair is a data change: append to coherenceRules. No control flow
// in CheckCoherence is keyed to a specific tool name.
type coherenceRule struct {
	// Grant is the tool whose presence arms the rule.
	Grant string
	// Requires is the tool that must accompany Grant.
	Requires string
	// Why states the capability that is lost, in the operator's terms. It is
	// logged verbatim, so it must read as a complete clause after the tool
	// names ("... without ...: <Why>").
	Why string
}

// coherenceRules is the complete rule table (#1174).
var coherenceRules = []coherenceRule{
	{
		Grant:    toolSearch,
		Requires: toolFetch,
		Why: "search returns navigational pointers carrying a reference and fetch is the only tool " +
			"that dereferences one, so this persona can discover that an answer exists and never read " +
			"it, and cannot follow a knowledge page's outbound references at all",
	},
	{
		Grant:    toolMemoryCapture,
		Requires: toolSearch,
		Why: "search is the retrieval front door for captured memory, so this persona writes knowledge " +
			"that nobody, including itself, can retrieve",
	},
	{
		Grant:    toolApplyKnowledge,
		Requires: toolSearch,
		Why: "the review workflow's own documented first step is to discover what is already known " +
			"before applying, which is unreachable without search",
	},
}

// CoherenceFinding reports one persona holding an incoherent tool set.
//
// It is a diagnostic, never a gate: an operator may have a real reason for a
// restricted persona, so callers log findings and continue.
type CoherenceFinding struct {
	// Persona is the name of the persona the finding is about.
	Persona string `json:"persona"`
	// Granted is the tool the persona holds that arms the rule.
	Granted string `json:"granted"`
	// Missing is the tool the persona lacks.
	Missing string `json:"missing"`
	// Why states the capability lost by the pairing being broken.
	Why string `json:"why"`
	// Remedy is the operator-facing fix.
	Remedy string `json:"remedy"`
}

// CheckCoherence evaluates the rule table for one persona against the set of
// tools actually registered on this deployment.
//
// registered scopes the check to what exists: a deployment that never
// registered fetch (no database, no search toolkit) is not misconfigured for
// withholding it, and warning about it would train operators to ignore the
// warning. A rule fires only when both of its tools are registered, the
// persona allows Grant, and the persona does not allow Requires.
//
// A nil persona yields no findings — it reaches nothing, so no pairing of
// capabilities is broken.
func CheckCoherence(p *Persona, registered []string) []CoherenceFinding {
	if p == nil {
		return nil
	}

	present := make(map[string]bool, len(registered))
	for _, t := range registered {
		present[t] = true
	}

	var findings []CoherenceFinding
	for _, rule := range coherenceRules {
		if !present[rule.Grant] || !present[rule.Requires] {
			continue
		}
		grantAllowed, _, _ := evaluateToolAccess(p, rule.Grant)
		if !grantAllowed {
			continue
		}
		if requiresAllowed, _, _ := evaluateToolAccess(p, rule.Requires); requiresAllowed {
			continue
		}
		findings = append(findings, CoherenceFinding{
			Persona: p.Name,
			Granted: rule.Grant,
			Missing: rule.Requires,
			Why:     rule.Why,
			Remedy: fmt.Sprintf("add %q to persona %q's tools.allow, or remove the tools.deny pattern that withholds it",
				rule.Requires, p.Name),
		})
	}
	return findings
}

// CheckRegistryCoherence evaluates every registered persona, returning the
// findings sorted by persona name then granted tool so a startup log is stable
// across restarts (Registry.All iterates a map).
func CheckRegistryCoherence(reg *Registry, registered []string) []CoherenceFinding {
	if reg == nil {
		return nil
	}
	var findings []CoherenceFinding
	for _, p := range reg.All() {
		findings = append(findings, CheckCoherence(p, registered)...)
	}
	sortFindings(findings)
	return findings
}

// sortFindings orders findings by persona name then granted tool.
func sortFindings(findings []CoherenceFinding) {
	slices.SortFunc(findings, func(a, b CoherenceFinding) int {
		if c := cmp.Compare(a.Persona, b.Persona); c != 0 {
			return c
		}
		return cmp.Compare(a.Granted, b.Granted)
	})
}
