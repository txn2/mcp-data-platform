import type { LibraryItem } from "@/api/portal/hooks/library";

/**
 * changedWhen is when an item changed, as its row says it (#2734): relative
 * within a day ("3m ago"), its date after that ("Nov 19"), with the year when
 * it is not this one.
 */
export function changedWhen(iso: string, now: Date = new Date()): string {
  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return "";
  const mins = Math.round((now.getTime() - then.getTime()) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  if (mins < 24 * 60) return `${Math.round(mins / 60)}h ago`;
  const opts: Intl.DateTimeFormatOptions = { month: "short", day: "numeric" };
  if (then.getFullYear() !== now.getFullYear()) opts.year = "numeric";
  return then.toLocaleDateString(undefined, opts);
}

/**
 * itemDetail is the line under an item's name (#2734): whose it is and when it
 * last changed, "Yours" for the reader's own, "Collection" before either for a
 * collection.
 */
export function itemDetail(it: LibraryItem, me: string, now: Date = new Date()): string {
  const parts: string[] = [];
  if (it.kind === "collection") parts.push("Collection");
  const owner = it.owner_email ?? "";
  if (owner && me && owner.toLowerCase() === me.toLowerCase()) parts.push("Yours");
  else if (owner) parts.push(`Shared by ${owner}`);
  const when = it.updated_at ? changedWhen(it.updated_at, now) : "";
  if (when) parts.push(`Changed ${when}`);
  return parts.join(" · ");
}
