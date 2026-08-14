import type { ScriptGrants, VersionReview } from "@/api/admin/types";

// The platform's closed capability and destination vocabularies, mirroring
// pkg/script/grants.go. They are closed on purpose: a review is only
// meaningful while every capability a script can reach can be listed, so this
// surface offers the whole set rather than only what the code was seen to use.
// A capability the server does not implement is refused at approval, which is
// what keeps this list from drifting silently.
export const CAPABILITIES = ["platform.query", "platform.export"] as const;
export const DESTINATIONS = ["portal"] as const;

// EMPTY_GRANT is what an unapproved version carries: nothing granted anywhere.
export const EMPTY_GRANT: ScriptGrants = {
  roles: [],
  connections: [],
  capabilities: [],
  destinations: [],
};

// proposedGrant is the grant the approve form opens on: what this version's
// code plainly reaches for, plus anything the version already carries from an
// earlier approval.
//
// It is a starting point and never a decision. The reviewer removes what the
// script should not have; the server refuses an approval that does not cover
// what the code calls, so the two ends agree on what "unreachable" means.
export function proposedGrant(review: VersionReview): ScriptGrants {
  const capabilities = union(
    review.referenced.capabilities,
    review.version.grants?.capabilities,
  );
  return {
    roles: review.version.author_roles ?? [],
    connections: union(review.referenced.connections, review.version.grants?.connections),
    capabilities,
    // A script that exports has to be allowed to land its output somewhere, and
    // the portal is the only destination the platform implements today.
    destinations: union(
      capabilities.includes("platform.export") ? DESTINATIONS.slice() : [],
      review.version.grants?.destinations,
    ),
  };
}

// GrantDelta is one axis of the authority diff: what approving would add to,
// and remove from, what the script holds today.
export interface GrantDelta {
  added: string[];
  removed: string[];
  unchanged: string[];
}

// grantDelta compares a proposed grant axis against the approved one. It is the
// question the reviewer is actually answering — not "what does this script
// use", but "what would it be able to do that it cannot do now".
export function grantDelta(previous: string[] | undefined, next: string[] | undefined): GrantDelta {
  const before = new Set(previous ?? []);
  const after = new Set(next ?? []);
  return {
    added: [...after].filter((v) => !before.has(v)).sort(),
    removed: [...before].filter((v) => !after.has(v)).sort(),
    unchanged: [...after].filter((v) => before.has(v)).sort(),
  };
}

// widensAuthority reports whether a proposed grant reaches anywhere the
// approved one does not. A widening is the case a reviewer must not miss, so
// the surface states it rather than leaving three lists to be compared by eye.
export function widensAuthority(previous: ScriptGrants | undefined, next: ScriptGrants): boolean {
  const base = previous ?? EMPTY_GRANT;
  return (
    grantDelta(base.connections, next.connections).added.length > 0 ||
    grantDelta(base.capabilities, next.capabilities).added.length > 0 ||
    grantDelta(base.destinations, next.destinations).added.length > 0
  );
}

// union merges two lists, de-duplicated and sorted, dropping empties.
function union(a: string[] | undefined, b: string[] | undefined): string[] {
  return [...new Set([...(a ?? []), ...(b ?? [])])].filter(Boolean).sort();
}

// toggle adds or removes one value from a grant axis, keeping it sorted so the
// rendered order does not depend on click order.
export function toggle(values: string[], value: string): string[] {
  return values.includes(value)
    ? values.filter((v) => v !== value)
    : [...values, value].sort();
}
