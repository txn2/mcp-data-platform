/**
 * parseEmailAddress reduces recipient input to the bare address it names,
 * lowercased, or returns null when no address can be extracted.
 *
 * It accepts the two forms people actually type into a share field: a plain
 * address, and the `Display Name <user@example.com>` form mail clients put on
 * the clipboard. The server applies the same rule (portaldomain.ParseEmail)
 * and is the authority; this exists so the field normalizes as you leave it
 * rather than letting a paste travel to the server only to come back as a
 * validation error.
 *
 * The domain must contain a dot, matching the server, so a single-label host
 * is rejected here too instead of being accepted and then refused.
 */
export function parseEmailAddress(input: string): string | null {
  const trimmed = input.trim();
  if (!trimmed || trimmed.length > 254) return null;

  // Prefer the angle-bracketed address when present; otherwise the whole
  // string is the candidate. One address only: a comma-separated list is not
  // a single recipient.
  const angled = /<([^<>]*)>\s*$/.exec(trimmed);
  const candidate = (angled?.[1] ?? trimmed).trim();
  if (!candidate || /[\s,;]/.test(candidate)) return null;

  const at = candidate.lastIndexOf("@");
  if (at <= 0 || at === candidate.length - 1) return null;
  return isRoutableDomain(candidate.slice(at + 1)) ? candidate.toLowerCase() : null;
}

// isRoutableDomain requires a dotted host, matching the server: a single-label
// domain names a machine on the local network, not a mailbox anyone can be
// reached at.
function isRoutableDomain(domain: string): boolean {
  return domain.includes(".") && !domain.startsWith(".") && !domain.endsWith(".");
}
