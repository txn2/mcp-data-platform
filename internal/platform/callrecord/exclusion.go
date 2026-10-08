package callrecord

import (
	"context"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/configwarn"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
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
// persona is marked as machinery, and its calls are not cataloged. It is marked
// in one of two places: on the persona itself (service_account, editable in the
// portal's persona editor, #1980), or by name in calls.exclude_personas, the
// file-config form of the same rule. Either one excludes. An API key reaches a
// persona through its roles, so marking the persona covers every key mapped to
// it.
//
// The persona's own mark is read from the live registry on every call, never
// from a snapshot taken at startup: an administrator marks a database persona
// at run time, and the reload bus carries the change to every replica's
// registry, so the next call on any of them is judged by it.
//
// Declining to catalog costs nothing else. The recorder is a decorator over
// the audit store and writes the audit event first, so what an automated
// system did remains fully visible in the audit log, in its retention, and in
// the API gateway's metrics; only the catalog, which exists to answer "is this
// worth running again", is spared the answer "no, by construction".
//
// One caller needs no naming: a managed script run. The platform's own
// scheduler is the automated system, and a run is by construction the re-run --
// its statement is the script's source, its outputs are on the run record and
// in the produced asset's provenance, and nobody fetches one of its calls to
// run it again. It also cannot be named, because a run presents the persona of
// the person who wrote the script, so the calls a schedule makes every hour are
// indistinguishable by persona from that person's own (#1624). So a call
// arriving as SourceScript is excluded whatever persona it carries, with no
// knob: a deployment wanting a script's calls cataloged has no such case.
//
// The rule is read in three places and stated in one: the recorder, which never
// writes the row, the call reference, which hands back no citation to a record
// that will not exist, and the sweep, which removes the rows written before the
// deployment declared the persona or before this platform declined a run's
// calls. No surface filters at read time, because a declined call has no row to
// filter.

// Exclusion is the rule for which calls the catalog does not keep: those made
// under a persona the deployment declared to be machinery, and those a managed
// script run made.
//
// The zero value still excludes script runs, which is not configuration but
// construction. It excludes no persona, which is what a deployment that
// configures nothing gets: it catalogs exactly what it cataloged before.
type Exclusion struct {
	// personas are the normalized names, sorted and deduplicated so the sweep
	// binds the same array on every replica and a test reads a stable order.
	personas []string
	// accounts answers which personas are marked as service accounts now.
	// Nil marks none.
	accounts ServiceAccounts
}

// ServiceAccounts answers which personas are marked as service accounts, read
// at the moment it is asked. The persona registry is the implementation.
type ServiceAccounts interface {
	// IsServiceAccount reports whether the persona with this exact name is
	// marked.
	IsServiceAccount(persona string) bool
	// ServiceAccountNames lists every marked persona.
	ServiceAccountNames() []string
}

// NewExclusion reads the configured persona names into the rule.
//
// A name is matched case-insensitively after trimming. The persona registry
// itself matches exactly, but a persona may be defined in the database rather
// than beside this setting, so the operator writing it here is not necessarily
// reading the definition; a catalog knob that silently does nothing because of
// a capital letter is worse than one that is slightly forgiving.
//
// An empty name is dropped rather than kept. Kept, it would exclude every call
// made without a persona at all, which is the opposite of naming one.
func NewExclusion(names []string) Exclusion {
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		if n := normalizePersona(name); n != "" && !slices.Contains(normalized, n) {
			normalized = append(normalized, n)
		}
	}
	slices.Sort(normalized)
	return Exclusion{personas: normalized}
}

// WithServiceAccounts returns the rule extended with the personas marked as
// service accounts, which it reads through accounts whenever it is asked. A nil
// accounts adds nothing.
func (e Exclusion) WithServiceAccounts(accounts ServiceAccounts) Exclusion {
	e.accounts = accounts
	return e
}

// Excludes reports whether a call is machinery: it arrived from a managed
// script run, or it was made under a persona the deployment named in config or
// marked as a service account.
//
// The source is the audit event's own `source` field, not a guess from the
// principal: the field is set on the run's server context by the script runner
// and travels to the audit row, so a caller cannot present it and a run cannot
// omit it.
func (e Exclusion) Excludes(persona, source string) bool {
	if source == mcpcontext.SourceScript {
		return true
	}
	return e.Configured(persona) || e.ServiceAccount(persona)
}

// ServiceAccount reports whether the persona is marked as a service account
// right now. The configured names are not consulted: this is the persona's own
// setting, which is what the portal shows beside it. Names are compared without
// case, as the sweep compares them, so a call the recorder keeps is never one
// the sweep would remove.
func (e Exclusion) ServiceAccount(persona string) bool {
	if e.accounts == nil || persona == "" {
		return false
	}
	if e.accounts.IsServiceAccount(persona) {
		return true
	}
	return slices.ContainsFunc(e.accounts.ServiceAccountNames(), func(n string) bool {
		return strings.EqualFold(n, persona)
	})
}

// Configured reports whether the persona is named in calls.exclude_personas.
func (e Exclusion) Configured(persona string) bool {
	name := normalizePersona(persona)
	return name != "" && slices.Contains(e.personas, name)
}

// Personas returns the normalized names for the sweep to bind: the configured
// ones and the ones marked as service accounts at the moment it is asked,
// sorted and deduplicated. The slice is new on every call: it is handed to a
// database driver, and the rule is not the driver's to alter.
func (e Exclusion) Personas() []string {
	names := append(make([]string, 0, len(e.personas)), e.personas...)
	if e.accounts != nil {
		for _, name := range e.accounts.ServiceAccountNames() {
			names = append(names, normalizePersona(name))
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

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
			configwarn.Warn(context.Background(), configwarn.CodeExcludePersonaUnknown, "calls.exclude_personas names an unknown persona; its calls are still cataloged",
				"persona", logsan.SanitizeForLog(name))
		}
	}
}
