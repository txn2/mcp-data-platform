// Package personarules is what the admin API does to a persona's rules
// before it stores them: the API-route rules normalized to the form they are
// stored in (#1479), and the rules and portal levels the registry would
// refuse, refused before the write (#2225).
package personarules

import (
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// Validate refuses the API route rules and portal levels the registry would
// refuse, before the store write: a rule refused after it would already be
// persisted. It also refuses the account types the platform cannot honor
// (#2257): adminPersona is the configured administrator persona's name.
func Validate(p *persona.Persona, adminPersona string) error {
	if err := persona.ValidateAPIRoutes(p.APIRoutes); err != nil {
		return err //nolint:wrapcheck // the rule's own message is the 400 detail
	}
	if err := persona.ValidateAccount(p, adminPersona); err != nil {
		return err //nolint:wrapcheck // the refusal's own message is the 400 detail
	}
	return p.Portal.Validate() //nolint:wrapcheck // the level's own message is the 400 detail
}

// Normalize returns the rules as they are stored: whitespace trimmed,
// empty patterns dropped, methods uppercased, and an empty action written as
// the explicit "allow" it means.
//
// Methods are uppercased because the toolkit normalizes an inbound method to
// uppercase before the check runs and the patterns are matched case-sensitively
// — a rule typed as "delete" would otherwise be stored, displayed, and never
// match anything. Paths are left exactly as written: a path glob is the
// operator's, and rewriting one would stop a hand-written pattern round-tripping
// to the editor as the pattern it was typed as.
func Normalize(rules []persona.APIRouteRule) []persona.APIRouteRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]persona.APIRouteRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, persona.APIRouteRule{
			Connection: strings.TrimSpace(r.Connection),
			Methods:    cleanPatterns(r.Methods, strings.ToUpper),
			Paths:      cleanPatterns(r.Paths, nil),
			Action:     routeAction(r.Action),
		})
	}
	return out
}

// cleanPatterns trims each pattern, drops the empty ones, and applies an
// optional per-pattern transform. Returns nil for an all-empty list so the
// "empty means any" reading is preserved.
func cleanPatterns(patterns []string, transform func(string) string) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if transform != nil {
			p = transform(p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// routeAction maps a recognized action onto the value it is stored as,
// an empty one onto the "allow" it has always meant, and anything else onto
// itself.
//
// Passing an unrecognized action through is deliberate: validation runs on the
// normalized rules, so folding "audit" into "allow" here would store it as a
// grant rather than refusing the request that asked for it.
func routeAction(action string) string {
	trimmed := strings.TrimSpace(action)
	switch {
	case trimmed == "":
		return persona.ActionAllow
	case strings.EqualFold(trimmed, persona.ActionDeny):
		return persona.ActionDeny
	case strings.EqualFold(trimmed, persona.ActionAllow):
		return persona.ActionAllow
	default:
		return trimmed
	}
}
