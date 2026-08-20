import type { ScriptDestination, ScriptGrants, VersionReview } from "@/api/admin/types";

// The platform's closed capability vocabulary, mirroring pkg/script/grants.go.
// It is closed on purpose: a review is only meaningful while every capability a
// script can reach can be listed, so this surface offers the whole set rather
// than only what the code was seen to use. A capability the server does not
// implement is refused at approval, which is what keeps this list from drifting
// silently.
export const CAPABILITIES = [
  "platform.query",
  "platform.export",
  "platform.publish_data",
] as const;

// The portal is the destination the platform provides itself, and the one a
// script writes to when it names none. Every other destination is an address a
// reviewer supplies, so destinations are NOT a closed list the way capabilities
// are: see DestinationsEditor.
export const PORTAL_DESTINATION = "portal";
export const DESTINATION_KIND_PORTAL = "portal";
export const DESTINATION_KIND_S3 = "s3";

// portalDestination is the canonical portal grant entry.
export function portalDestination(): ScriptDestination {
  return { name: PORTAL_DESTINATION, kind: DESTINATION_KIND_PORTAL };
}

// isPortal reports whether a destination is the platform's own asset store.
export function isPortal(destination: ScriptDestination): boolean {
  return destination.kind === DESTINATION_KIND_PORTAL;
}

// EMPTY_GRANT is what an unapproved version carries: nothing granted anywhere.
export const EMPTY_GRANT: ScriptGrants = {
  roles: [],
  connections: [],
  capabilities: [],
  destinations: [],
};

// destinationKey renders one destination as the single string the diff is taken
// over.
//
// It includes the ADDRESS, not just the name, which is the whole point: a
// destination repointed at a different bucket is new authority even though its
// name did not change, and a diff taken over names alone would show a reviewer
// "no change" while the data started going somewhere else.
export function destinationKey(destination: ScriptDestination): string {
  if (isPortal(destination)) {
    return destination.name;
  }
  // Normalized the way the server normalizes it at approval (Destination.
  // Normalized), so a prefix retyped as "/weekly/" is the same grant as the
  // stored "weekly" rather than a removal and an addition — which would report
  // a widening of authority that is not happening.
  const prefix = normalizePrefix(destination.prefix);
  return `${destination.name} -> ${destination.kind} ${destination.connection?.trim() ?? ""} ${destination.bucket?.trim() ?? ""}${prefix ? `/${prefix}` : ""}`;
}

// normalizePrefix trims a typed prefix to the one form the server stores.
export function normalizePrefix(prefix: string | undefined): string {
  return (prefix ?? "").trim().replace(/^\/+/, "").replace(/\/+$/, "");
}

// destinationKeys renders a whole axis for the diff.
export function destinationKeys(destinations: ScriptDestination[] | undefined): string[] {
  return (destinations ?? []).map(destinationKey);
}

// proposedGrant is the grant the approve form opens on: what this version's
// code plainly reaches for, plus anything the version already carries from an
// earlier approval.
//
// It is a starting point and never a decision. The reviewer removes what the
// script should not have; the server refuses an approval that does not cover
// what the code calls, so the two ends agree on what "unreachable" means.
//
// A destination the code names but no approval has ever given an address is
// proposed with an EMPTY address, so it appears in the editor as a decision
// waiting to be made rather than being silently dropped or silently invented.
export function proposedGrant(review: VersionReview): ScriptGrants {
  const held = review.version.grants?.destinations ?? [];
  const destinations = [...held];
  for (const name of review.referenced.destinations ?? []) {
    if (destinations.some((d) => d.name === name)) continue;
    destinations.push(
      name === PORTAL_DESTINATION ? portalDestination() : { name, kind: DESTINATION_KIND_S3 },
    );
  }
  return {
    roles: review.version.author_roles ?? [],
    connections: union(review.referenced.connections, review.version.grants?.connections),
    capabilities: union(review.referenced.capabilities, review.version.grants?.capabilities),
    destinations,
  };
}

// incompleteDestinations names the granted destinations that do not yet say
// where they write. The server refuses them, and saying so here is what turns a
// rejected approval into a field left to fill in.
export function incompleteDestinations(grants: ScriptGrants): string[] {
  return (grants.destinations ?? [])
    .filter((d) => !isPortal(d) && !(d.connection && d.bucket))
    .map((d) => d.name);
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
    grantDelta(destinationKeys(base.destinations), destinationKeys(next.destinations)).added.length >
      0
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
