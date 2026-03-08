/** Extract the human-readable name from a DataHub URN. */
export function formatEntityUrn(urn: string): string {
  // urn:li:dataset:(urn:li:dataPlatform:X,<name>,ENV)
  const match = urn.match(/,([^,]+),\w+\)$/);
  if (match?.[1]) return match[1];
  // Fallback: last colon-separated segment
  const parts = urn.split(":");
  return parts[parts.length - 1] || urn;
}
