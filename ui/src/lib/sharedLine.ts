/**
 * sharedLine says who shared an item with the reader, and through which
 * persona when it was shared with a persona rather than with them (#2089).
 */
export function sharedLine(share: { shared_by: string; via_persona?: string }): string {
  return share.via_persona
    ? `Shared by ${share.shared_by} with ${share.via_persona}`
    : `Shared by ${share.shared_by}`;
}
