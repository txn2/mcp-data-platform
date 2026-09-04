package callrecord

import (
	"log/slog"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// A deployment's automated machinery calls the same tools its people do. An
// ingestion job fetching one upstream resource at a time through
// api_invoke_endpoint produces a record per fetch, none of which any person
// will re-run: on one deployment 99% of a 476,749-row catalog was a single
// service principal, every row of it embedded, against 43 records that had
// answered something (#1614).
//
// What separates the two is not the tool but who called it, and the layer a
// deployment already uses to say what a caller is for is the persona. So a
// deployment names the personas that are machinery, and their calls are not
// cataloged.
//
// Declining to catalog costs nothing else. The recorder is a decorator over
// the audit store and writes the audit event first, so what an automated
// system did remains fully visible in the audit log, in its retention, and in
// the API gateway's metrics; only the catalog, which exists to answer "is this
// worth running again", is spared the answer "no, by construction".
//
// The rule is read in two places and stated in one: the recorder, which never
// writes the row, and the sweep, which removes the rows written before the
// deployment declared the persona. No surface filters at read time, because a
// declined call has no row to filter.

// PersonaExclusion is the set of personas whose calls are machinery.
//
// The zero value excludes nothing, which is what a deployment that configures
// nothing gets: it catalogs exactly what it cataloged before.
type PersonaExclusion struct {
	// personas are the normalized names, sorted and deduplicated so the sweep
	// binds the same array on every replica and a test reads a stable order.
	personas []string
}

// NewPersonaExclusion reads the configured persona names into the rule.
//
// A name is matched case-insensitively after trimming. The persona registry
// itself matches exactly, but a persona may be defined in the database rather
// than beside this setting, so the operator writing it here is not necessarily
// reading the definition; a catalog knob that silently does nothing because of
// a capital letter is worse than one that is slightly forgiving.
//
// An empty name is dropped rather than kept. Kept, it would exclude every call
// made without a persona at all, which is the opposite of naming one.
func NewPersonaExclusion(names []string) PersonaExclusion {
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		if n := normalizePersona(name); n != "" && !slices.Contains(normalized, n) {
			normalized = append(normalized, n)
		}
	}
	slices.Sort(normalized)
	return PersonaExclusion{personas: normalized}
}

// Excludes reports whether a call made under this persona is machinery.
func (e PersonaExclusion) Excludes(persona string) bool {
	if len(e.personas) == 0 {
		return false
	}
	name := normalizePersona(persona)
	return name != "" && slices.Contains(e.personas, name)
}

// Personas returns the normalized names for the sweep to bind. The slice is
// copied: it is handed to a database driver, and the rule is not the driver's
// to alter.
func (e PersonaExclusion) Personas() []string { return slices.Clone(e.personas) }

// normalizePersona is the one spelling of how a persona name is compared for
// exclusion, used by the rule, by the startup check below, and mirrored in SQL
// by the sweep (lower(r.persona)). A name written one way in the config and
// another on the record therefore matches in every one of them or in none.
func normalizePersona(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// WarnUnknownExcluded logs each configured name that matches no persona in
// known, which is a name that excludes nothing and says so nowhere: the catalog
// goes on filling with an automated system's traffic while the operator
// believes it is not. Both sides are compared through normalizePersona, so a
// name the rule accepts is never reported as one it does not.
func WarnUnknownExcluded(configured, known []string) {
	if len(configured) == 0 {
		return
	}
	defined := make(map[string]bool, len(known))
	for _, name := range known {
		defined[normalizePersona(name)] = true
	}
	for _, name := range configured {
		if !defined[normalizePersona(name)] {
			slog.Warn("calls.exclude_personas names an unknown persona; its calls are still cataloged",
				"persona", logsan.SanitizeForLog(name))
		}
	}
}
