import type { Share, SharePermission } from "@/api/portal/types";
import { compareOrdinal } from "@/lib/sortByName";

/**
 * The share dialog's reading of an item's shares (#2291): who has access, the
 * one general link, the links older than it, and the posts to channels. Pure,
 * so the dialog's rules are tested without rendering it.
 */

/** GeneralMode is who can open an item without being added: nobody, anyone who signs in, anyone. */
export type GeneralMode = "none" | "authenticated" | "public";

/** named reports whether a share is addressed to a person or a group. */
export function named(s: Share): boolean {
  return Boolean(s.shared_with_user_id || s.shared_with_email || s.shared_with_persona);
}

/** posted reports whether a share is the link a post to a channel created. */
export function posted(s: Share): boolean {
  return s.origin === "channel_post" || Boolean(s.posted_to);
}

/** live reports whether a share is in force at now. */
export function live(s: Share, now: number): boolean {
  return !s.revoked && (!s.expires_at || new Date(s.expires_at).getTime() > now);
}

/**
 * isGeneralLink is the server's rule (portaldomain.IsGeneralLink): a link
 * addressed to nobody, not made by a post, still in force.
 */
export function isGeneralLink(s: Share, now: number): boolean {
  return live(s, now) && !named(s) && !posted(s);
}

/** The shares an item's dialog lists, in the groups it lists them under. */
export interface ShareGroups {
  /** People and groups, oldest grant first. */
  grants: Share[];
  /** The newest general link, which General access reads and changes. */
  link: Share | undefined;
  /** General links older than it, from before an item had one. */
  otherLinks: Share[];
  /** Links posts to channels made, newest first. */
  posts: Share[];
}

/** groupShares sorts an item's shares into what the dialog shows. */
export function groupShares(shares: Share[], now: number): ShareGroups {
  const active = shares.filter((s) => live(s, now));
  const byCreated = (a: Share, b: Share) => compareOrdinal(a.created_at, b.created_at);
  const links = active.filter((s) => isGeneralLink(s, now)).sort(byCreated);
  return {
    grants: active.filter(named).sort(byCreated),
    link: links[links.length - 1],
    otherLinks: links.slice(0, -1).reverse(),
    posts: active.filter((s) => !named(s) && posted(s)).sort((a, b) => byCreated(b, a)),
  };
}

/** generalMode is the mode General access reads. */
export function generalMode(link: Share | undefined): GeneralMode {
  if (!link) return "none";
  return link.access_mode === "public" ? "public" : "authenticated";
}

/** The three modes, as General access names and explains them. */
export const MODES: Record<GeneralMode, { label: string; sentence: string; option: string }> = {
  none: {
    label: "Only people added",
    sentence: "Only the people above can open it.",
    option: "Only the people listed above can open it.",
  },
  authenticated: {
    label: "Anyone who can sign in",
    sentence: "Anyone with an account here can open it with the link.",
    option: "Anyone with an account here can open it with the link.",
  },
  public: {
    label: "Anyone with the link",
    sentence: "No sign-in needed. Anyone it’s forwarded to can open it too.",
    option: "No sign-in needed. The link stops working after a time you pick.",
  },
};

/** The lifetimes a link anyone can open is given. */
export const LIFETIMES = [
  { value: "1h", label: "In 1 hour" },
  { value: "24h", label: "In 24 hours" },
  { value: "168h", label: "In 7 days" },
  { value: "720h", label: "In 30 days" },
] as const;

/** The lifetime a link is given when none was chosen. */
export const DEFAULT_LIFETIME = "24h";

/** The banner a new link carries, as the server writes it. */
export const DEFAULT_BANNER = "Proprietary & Confidential. Only share with authorized viewers.";

/** The most a banner may hold, as the server allows it. */
export const MAX_BANNER = 500;

/** The most a note to recipients may hold, as the server allows it. */
export const MAX_MESSAGE = 500;

/** roleLabel names a permission by what the person can do. */
export function roleLabel(p: SharePermission): string {
  return p === "editor" ? "Can edit" : "Can view";
}

/** recipientOf is who a grant names: the group, or the address or id. */
export function recipientOf(s: Share): string {
  return s.shared_with_persona || s.shared_with_email || s.shared_with_user_id || "";
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** shortDate is "Oct 3". */
export function shortDate(iso: string): string {
  const d = new Date(iso);
  return `${MONTHS[d.getMonth()]} ${d.getDate()}`;
}

/** longDate is "Oct 31, 2026". */
export function longDate(iso: string): string {
  const d = new Date(iso);
  return `${MONTHS[d.getMonth()]} ${d.getDate()}, ${d.getFullYear()}`;
}

/** opened is how many times a link was opened, or "" for never. */
export function opened(n: number): string {
  if (n <= 0) return "";
  return n === 1 ? "opened once" : `opened ${n} times`;
}

/**
 * accessEnds is the countdown a grant made before #2291 may still carry:
 * "Access ends in 3 days". A grant with no end says nothing.
 */
export function accessEnds(expiresAt: string | undefined, now: number): string {
  if (!expiresAt) return "";
  const ms = new Date(expiresAt).getTime() - now;
  if (ms <= 0) return "Access has ended";
  const hours = Math.floor(ms / 3_600_000);
  if (hours < 1) return "Access ends within the hour";
  if (hours < 24) return `Access ends in ${hours} ${hours === 1 ? "hour" : "hours"}`;
  const days = Math.floor(hours / 24);
  return `Access ends in ${days} ${days === 1 ? "day" : "days"}`;
}

/** sharedWith is the toast after a share: "Shared with Ann" or "Shared with Ann and 2 others". */
export function sharedWith(names: string[]): string {
  if (names.length === 0) return "";
  const [first, ...rest] = names;
  if (rest.length === 0) return `Shared with ${first}`;
  return `Shared with ${first} and ${rest.length} ${rest.length === 1 ? "other" : "others"}`;
}

/** guestNote is what an Adding view says when an address has no account. */
export function guestNote(guests: string[], isFile: boolean): string {
  const opens = isFile
    ? "They’ll get an email, and can open it once they have an account."
    : "They’ll get an email with a link to view it.";
  const who = guests.length === 1 ? `${guests[0]} doesn’t have an account.` : `${guests.length} people don’t have an account.`;
  return `${who} ${opens} Guests can view but not edit.`;
}

/**
 * initials is a person's avatar text: the first letter of the first and last
 * name, or the first letter of the address.
 */
export function initials(name: string, email: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length >= 2) return (words[0]![0]! + words[words.length - 1]![0]!).toUpperCase();
  if (words.length === 1) return words[0]![0]!.toUpperCase();
  return (email.trim()[0] ?? "?").toUpperCase();
}

/**
 * TINTS are the avatar colours, each a background and foreground pair that
 * reads at 4.5:1 in light and dark.
 */
export const TINTS = [
  "bg-blue-100 text-blue-800 dark:bg-blue-500/20 dark:text-blue-200",
  "bg-emerald-100 text-emerald-800 dark:bg-emerald-500/20 dark:text-emerald-200",
  "bg-violet-100 text-violet-800 dark:bg-violet-500/20 dark:text-violet-200",
  "bg-rose-100 text-rose-800 dark:bg-rose-500/20 dark:text-rose-200",
  "bg-teal-100 text-teal-800 dark:bg-teal-500/20 dark:text-teal-200",
  "bg-orange-100 text-orange-900 dark:bg-orange-500/20 dark:text-orange-200",
] as const;

/** The tint a guest is shown in. */
export const GUEST_TINT = "bg-amber-100 text-amber-900 dark:bg-amber-500/20 dark:text-amber-200";

/** tintFor picks a name's tint, the same one every time. */
export function tintFor(key: string): string {
  let h = 0;
  for (const ch of key) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return TINTS[h % TINTS.length]!;
}

/** highlight splits text around the first match of q, for the matched part to be drawn bold. */
export function highlight(text: string, q: string): [string, string, string] {
  const at = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1;
  if (at < 0) return [text, "", ""];
  return [text.slice(0, at), text.slice(at, at + q.length), text.slice(at + q.length)];
}

/** pageLink is what Copy link copies with no general link: the item's own page. */
export function pageLink(origin: string, type: "asset" | "collection" | "prompt" | "resource", id: string): string {
  const section = { asset: "assets", collection: "collections", prompt: "prompts", resource: "resources" }[type];
  return `${origin}/portal/${section}/${id}`;
}

/** linkURL is what a general link opens at. */
export function linkURL(origin: string, s: Share): string {
  return `${origin}/portal/view/${s.token}`;
}
