// Package verify enforces project-level structural invariants.
//
// This file adds the stability-policy gate (issue #1076). The module is
// published at major version 1 with no /vN suffix, which in Go semantics
// promises compatibility across everything importable. docs/library/stability.md
// and CONTRIBUTING.md narrow that promise to a named supported surface. Until
// now nothing enforced the narrowing, so every package added under pkg/ silently
// took on a semver commitment it was never meant to make — and twenty-two of
// them turned out to be implementation detail with a single composition-root
// importer.
//
// The rule: a package under pkg/ must either be part of the documented
// supported surface, or have more than one distinct first-party importer. One
// importer means the package exists to serve exactly one caller, which is the
// signature of an implementation seam rather than an integration point; such a
// package belongs under internal/, where Go's own rule makes the boundary
// unforgeable.
//
// Two importers is the threshold rather than one because a package genuinely
// shared between subsystems is doing integration work even when no external
// consumer has appeared yet, and forcing it into internal/ would be the wrong
// call. The allowlist carries the deliberate exceptions with a written
// justification each, and a stale entry fails just as a violation does.
//
// Run: go test -count=1 -run TestPublicSurfacePolicy .
package mcp_data_platform_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// supportedSurface is the integration surface published in
// docs/library/stability.md and CONTRIBUTING.md. Breaking changes to these
// packages' exported identifiers are made only in a major release. Keep this
// list and the documentation in step: the doc is the promise, this is the gate.
var supportedSurface = map[string]bool{
	"pkg/platform":   true,
	"pkg/toolkit":    true,
	"pkg/registry":   true,
	"pkg/semantic":   true,
	"pkg/query":      true,
	"pkg/middleware": true,
}

// inSupportedSurface reports whether a module-relative package path is part of
// the published surface. The toolkit adapters are included by pattern: the
// policy names "pkg/toolkits/*" — the adapters themselves, whose exported
// config types a consumer writes — which is one segment below pkg/toolkits, not
// the helper packages an adapter splits itself into at greater depth.
func inSupportedSurface(rel string) bool {
	if supportedSurface[rel] {
		return true
	}
	const toolkits = "pkg/toolkits/"
	if rest, ok := strings.CutPrefix(rel, toolkits); ok {
		return !strings.Contains(rest, "/")
	}
	return false
}

// stabilityExemption records why a single-importer package under pkg/ is
// deliberately public despite the policy, and what would have to change for the
// entry to be retired.
type stabilityExemption struct {
	why  string
	exit string
}

// stabilityAllowlist is the set of packages (module-relative) that sit outside
// the supported surface with a single first-party importer and are nevertheless
// kept public on purpose. Every entry is a reference implementation of an
// interface the supported surface exposes: pkg/platform's Option surface accepts
// an injected store or provider (WithConnectionStore, WithSessionStore,
// WithQueryProvider, WithStorageProvider and their siblings), so a consumer
// running the platform as a library reads these to see what a real
// implementation looks like, or constructs one directly before swapping in
// their own. That is an integration use even though the composition root is the
// only in-module caller.
//
// The set is meant to shrink, never to grow. TestPublicSurfacePolicy fails on a
// stale entry that no longer violates the rule, so an entry cannot outlive the
// condition that justified it.
func stabilityAllowlist() map[string]stabilityExemption {
	return map[string]stabilityExemption{
		"pkg/admin": {
			why:  "a complete mountable HTTP subsystem rather than a seam: it builds the /api/v1/admin router, and a consumer embedding the platform mounts that router on their own server exactly as internal/httpserver does. The single importer reflects there being one composition root in this module, not a narrow contract.",
			exit: "retire if the admin API stops being independently mountable. #1078 decomposes the package but keeps it at pkg/admin.",
		},
		"pkg/database/migrate": {
			why:  "the schema itself: it carries the embedded SQL migrations and the runner a consumer invokes to bring their own database up to the platform's schema before starting it.",
			exit: "retire if migrations move behind a platform lifecycle call that a consumer never invokes directly.",
		},
		"pkg/audit/postgres": {
			why:  "the reference Postgres implementation of the audit store passed to platform.WithAuditLogger. Its one first-party importer is now internal/platform/auditwiring, which composes the store with the delivery writer and the readers derived from it (#1321).",
			exit: "as for pkg/configstore/postgres.",
		},
		"pkg/query/trino": {
			why:  "the reference query.Provider: the Trino adapter a consumer passes to platform.WithQueryProvider, or reads before writing their own engine adapter.",
			exit: "retire when the provider adapters move behind a registry that constructs them by name, so a consumer never names the package.",
		},
		"pkg/storage/s3": {
			why:  "the reference storage provider passed to platform.WithStorageProvider; same shape as pkg/query/trino.",
			exit: "as for pkg/query/trino.",
		},
		"pkg/configstore/postgres": {
			why:  "the reference Postgres implementation of the parent package's store contract; a consumer supplying their own backend implements the parent interface against this.",
			exit: "retire if the platform stops accepting an injected config store.",
		},
		"pkg/oauth/postgres": {
			why:  "the reference Postgres implementation of the OAuth store contract, as for pkg/configstore/postgres.",
			exit: "as for pkg/configstore/postgres.",
		},
		"pkg/prompt/postgres": {
			why:  "the reference Postgres implementation of the prompt store contract, as for pkg/configstore/postgres.",
			exit: "as for pkg/configstore/postgres.",
		},
		"pkg/searchgate/postgres": {
			why:  "the reference Postgres implementation of the search-gate store contract, as for pkg/configstore/postgres.",
			exit: "as for pkg/configstore/postgres.",
		},
		"pkg/session/postgres": {
			why:  "the reference Postgres implementation of the session store passed to platform.WithSessionStore.",
			exit: "as for pkg/configstore/postgres.",
		},
	}
}

// TestPublicSurfacePolicy fails when a package under pkg/ is outside the
// documented supported surface and has at most one distinct first-party
// importer, unless it carries an allowlist entry justifying the exception.
//
// The failure message names the policy and the remedy rather than only the
// offending package, because the fix is a relocation decision the author has to
// make deliberately: move it under internal/ (the usual answer), give the
// documentation a reason to promote it, or record why it is an exception.
func TestPublicSurfacePolicy(t *testing.T) {
	pkgs := firstPartyPackages(t)

	// Distinct first-party importers per package. Tests are excluded from the
	// load (see firstPartyPackages), so this counts production coupling only:
	// a package reached only from another package's tests is not an
	// integration point.
	importers := map[string]map[string]bool{}
	for _, p := range pkgs {
		from := relPath(p.PkgPath)
		for impPath := range p.Imports {
			if !isFirstParty(impPath) {
				continue
			}
			to := relPath(impPath)
			if to == from {
				continue
			}
			if importers[to] == nil {
				importers[to] = map[string]bool{}
			}
			importers[to][from] = true
		}
	}

	allow := stabilityAllowlist()
	var violations, stale []string
	seen := map[string]bool{}
	for _, p := range pkgs {
		rel := relPath(p.PkgPath)
		if !strings.HasPrefix(rel, "pkg/") {
			continue
		}
		seen[rel] = true
		offends := !inSupportedSurface(rel) && len(importers[rel]) <= 1
		_, exempt := allow[rel]
		switch {
		case offends && !exempt:
			violations = append(violations, fmt.Sprintf(
				"%s: outside the supported surface with %d first-party importer(s) (%s)",
				rel, len(importers[rel]), strings.Join(sortedKeys(importers[rel]), ", ")))
		case !offends && exempt:
			stale = append(stale, rel)
		}
	}
	// An entry naming a package that no longer exists is stale too. Without this
	// the loop above cannot see it — a deleted or relocated package simply stops
	// appearing in pkgs, and its exemption would outlive it silently, which is
	// the failure mode allowlists rot by.
	for rel := range allow {
		if !seen[rel] {
			stale = append(stale, rel+" (no such package)")
		}
	}
	sort.Strings(violations)
	sort.Strings(stale)

	require.Empty(t, violations, "stability policy violated:\n  %s\n\n"+
		"Every package under pkg/ carries a v1 semver promise. docs/library/stability.md "+
		"narrows that promise to the supported surface; a package outside it with a single "+
		"importer is an implementation seam, not an integration point.\n"+
		"Remedy, in order of preference: (1) move it under internal/ — internal/httpserver/... "+
		"for HTTP adapters the composition root mounts, internal/platform/... for facade seams, "+
		"following #894 and #1076; (2) if a library consumer could reasonably construct it "+
		"directly, promote it in docs/library/stability.md and say why in the PR; (3) if it is "+
		"a reference implementation of a supported-surface interface, add a stabilityAllowlist "+
		"entry with a written justification.", strings.Join(violations, "\n  "))

	require.Empty(t, stale, "stale stabilityAllowlist entr(ies): %s\n\n"+
		"These no longer violate the policy — they gained a second importer, moved into the "+
		"supported surface, or left pkg/. Delete the entry so the allowlist keeps shrinking.",
		strings.Join(stale, ", "))
}

// TestInSupportedSurfaceDepthRule pins the part of the policy that is easy to
// get wrong: "pkg/toolkits/*" admits the toolkit adapters one segment below
// pkg/toolkits, not everything beneath it. A prefix match would silently exempt
// every helper package an adapter splits itself into, which is the bulk of what
// the gate exists to catch.
func TestInSupportedSurfaceDepthRule(t *testing.T) {
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{"pkg/platform", true},
		{"pkg/middleware", true},
		{"pkg/toolkits/trino", true},
		{"pkg/toolkits/apigateway", true},
		{"pkg/toolkits/apigateway/catalog", false},
		{"pkg/toolkits/tools/toolsindex", false},
		{"pkg/toolkits", false},
		{"pkg/portal", false},
		{"pkg/platformextra", false},
		{"internal/platform", false},
	} {
		require.Equal(t, tc.want, inSupportedSurface(tc.rel), "inSupportedSurface(%q)", tc.rel)
	}
}

// sortedKeys returns a set's members in a deterministic order so a failure
// message reads the same on every run.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
