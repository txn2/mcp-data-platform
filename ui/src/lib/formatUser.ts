// The platform mints three kinds of principal and the user id carries which:
// a person authenticates as their OIDC subject, an API key as apikey:<name>
// (pkg/auth/apikey.go), and a managed script run as script:<name>
// (pkg/script's PrincipalPrefix). The audit label map resolves every principal
// to the address it acts for, so a person's own id and each script they own
// arrive here carrying the same email and are indistinguishable on their label
// alone; the kind and the name are what separate them (#1523).

export type PrincipalKind = "user" | "script" | "apikey";

export interface Principal {
  kind: PrincipalKind;
  /** The script or key name. The raw id for a person. */
  name: string;
  /** The address the principal acts for, when one is known. */
  email?: string;
}

const KIND_PREFIXES: ReadonlyArray<readonly [string, PrincipalKind]> = [
  ["script:", "script"],
  ["apikey:", "apikey"],
];

// An API key that configured no address authenticates as <name>@apikey.local
// (pkg/auth's SyntheticEmail): an identity rather than a mailbox, and one that
// only repeats the name already on screen. A key configured with a real
// address keeps it, because that address is a person who is accountable for
// the key.
const SYNTHETIC_APIKEY_SUFFIX = "@apikey.local";

/** Split a user id into the principal it names. */
export function parsePrincipal(userId: string, email?: string): Principal {
  for (const [prefix, kind] of KIND_PREFIXES) {
    if (userId.startsWith(prefix)) {
      const name = userId.slice(prefix.length);
      return { kind, name, email: email?.endsWith(SYNTHETIC_APIKEY_SUFFIX) ? undefined : email };
    }
  }
  return { kind: "user", name: userId, email };
}

/**
 * Render a principal as one line: a person by their address, an automation by
 * its kind and name followed by whoever it acts for.
 */
export function formatUser(userId: string, email?: string): string {
  return labelOf(parsePrincipal(userId, email));
}

/**
 * The part of an automation's label that follows its kind: the name and the
 * address it acts for. Where the kind is drawn as a badge rather than written
 * out, this is the text beside it.
 */
export function principalDetail(principal: Principal): string {
  return principal.email ? `${principal.name} - ${principal.email}` : principal.name;
}

function labelOf(principal: Principal): string {
  if (principal.kind === "user") return principal.email || shortenId(principal.name);
  return `${principal.kind}: ${principalDetail(principal)}`;
}

/**
 * Render the distinct principals of a user facet as its options, people before
 * the automations and each group by what it reads as. The API sorts the facet
 * by id, which interleaves a person with the scripts they own and orders the
 * automations by a prefix nobody sees.
 */
export function principalOptions(
  users: readonly string[] | undefined,
  labels: Record<string, string> | undefined,
): { value: string; label: string }[] {
  const rank: Record<PrincipalKind, number> = { user: 0, script: 1, apikey: 2 };
  const options = (users ?? [])
    .map((id) => {
      const principal = parsePrincipal(id, labels?.[id]);
      return { value: id, label: labelOf(principal), kind: principal.kind };
    })
    .sort((a, b) => rank[a.kind] - rank[b.kind] || a.label.localeCompare(b.label));

  // Two people can still read alike: one address reaches the facet under two
  // subjects when someone is known to two issuers or was re-registered under a
  // new one. They are separate principals and each filters to different rows,
  // so the one that repeats says which subject it is rather than leaving the
  // reader to pick between identical lines.
  const seen = new Map<string, number>();
  for (const o of options) seen.set(o.label, (seen.get(o.label) ?? 0) + 1);
  return options.map(({ value, label }) => ({
    value,
    label: (seen.get(label) ?? 0) > 1 ? `${label} (${shortenId(value)})` : label,
  }));
}

// A person with no address on the record is left with their subject, which is
// a UUID. It is truncated rather than shown whole: it identifies the row
// against the facet without putting an opaque identifier across the column.
function shortenId(userId: string): string {
  if (/^[0-9a-f]{8}-/.test(userId)) return userId.slice(0, 8) + "…";
  return userId;
}
