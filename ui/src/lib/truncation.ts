// What an export cut at a row or page limit and written anyway records (#2057).
// The asset carries the reserved tag while its current version is such an
// export; the version, and a managed-resource version, carry the cut in their
// metadata. The keys are the ones internal/exporttrunc writes.

/** The reserved tag an asset carries while its current version is incomplete. */
export const TRUNCATED_TAG = "_sys-truncated";

/** A cut as a version's metadata records it. */
export interface Truncation {
  limit: number;
  unit: string;
  source: string;
}

/** isTruncated reports whether an asset's tags mark it incomplete. */
export function isTruncated(tags: readonly string[] | undefined): boolean {
  return (tags ?? []).includes(TRUNCATED_TAG);
}

/** truncationOf reads the cut a version's metadata records, or null. */
export function truncationOf(metadata: Record<string, unknown> | undefined): Truncation | null {
  if (!metadata || metadata.truncated !== true) {
    return null;
  }
  return {
    limit: typeof metadata.limit_applied === "number" ? metadata.limit_applied : 0,
    unit: typeof metadata.limit_unit === "string" ? metadata.limit_unit : "rows",
    source: typeof metadata.limit_source === "string" ? metadata.limit_source : "",
  };
}

/** truncationLabel is the sentence a cut is shown as. */
export function truncationLabel(t: Truncation | null): string {
  if (!t || t.limit <= 0) {
    return "Incomplete: the export that wrote this file was cut at a limit";
  }
  return `Incomplete: truncated at ${t.limit.toLocaleString("en-US")} ${t.unit}`;
}
