// parseTags splits a comma-separated string into a normalized tag list:
// trimmed, lowercased, empties removed, and de-duplicated (order preserved).
// Shared by every tag input so the normalization rule lives in one place.
export function parseTags(input: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of input.split(",")) {
    const t = raw.trim().toLowerCase();
    if (t && !seen.has(t)) {
      seen.add(t);
      out.push(t);
    }
  }
  return out;
}
