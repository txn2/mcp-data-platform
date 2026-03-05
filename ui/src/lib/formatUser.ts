/** Show email/name if available, otherwise truncate UUIDs to 8 chars. */
export function formatUser(userId: string, email?: string): string {
  if (email) return email;
  // UUID pattern: truncate to first 8 chars
  if (/^[0-9a-f]{8}-/.test(userId)) return userId.slice(0, 8) + "\u2026";
  return userId;
}
