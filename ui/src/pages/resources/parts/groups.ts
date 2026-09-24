import type { Resource } from "@/api/resources/types";

/**
 * The never-read flag a curator reads in the Last read column.
 */

/**
 * NEVER_READ_DAYS is how long a resource must have existed unread before the
 * library flags it. A file uploaded yesterday with no reads is not dead weight.
 */
const NEVER_READ_DAYS = 30;

/**
 * True when nothing has read this resource and it is old enough for that to
 * mean something. One rule, read by the table's Last-read column and by the
 * image tile, so an image does not lose the flag by being shown as an image.
 */
export function neverRead(r: Resource): boolean {
  if (r.last_read_at) return false;
  return (Date.now() - new Date(r.created_at).getTime()) / 86_400_000 >= NEVER_READ_DAYS;
}
