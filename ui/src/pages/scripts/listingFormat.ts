// The short forms the Automations listing prints (#2601). Each cell has a
// fixed width, so a time is relative within a day ("5h ago"), a short date
// after it ("Nov 8"), and the exact time is on hover. None of them prints
// toLocaleString() with seconds, which was the widest value in the row and was
// clipped at the card's edge.

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function parse(iso?: string): Date | null {
  if (!iso) return null;
  const at = new Date(iso);
  return Number.isNaN(at.getTime()) ? null : at;
}

/** shortDate is "Nov 8", with the year when it is not this one. */
function shortDate(at: Date, now: Date): string {
  return at.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: at.getFullYear() === now.getFullYear() ? undefined : "numeric",
  });
}

/**
 * agoText is how long ago a past moment was: "just now", "12m ago", "5h ago"
 * within a day, a short date after it, and a dash for none.
 */
export function agoText(iso?: string, now: Date = new Date()): string {
  const at = parse(iso);
  if (!at) return "—";
  const elapsed = now.getTime() - at.getTime();
  if (elapsed < MINUTE) return "just now";
  if (elapsed < HOUR) return `${Math.floor(elapsed / MINUTE)}m ago`;
  if (elapsed < DAY) return `${Math.floor(elapsed / HOUR)}h ago`;
  return shortDate(at, now);
}

/**
 * nextFireText is when a schedule fires next: the time alone today ("7:30
 * AM"), the date and time otherwise ("Nov 11, 7:30 AM").
 */
export function nextFireText(iso?: string, now: Date = new Date()): string {
  const at = parse(iso);
  if (!at) return "—";
  const time = at.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (at.toDateString() === now.toDateString()) return time;
  return `${shortDate(at, now)}, ${time}`;
}

/** exactWhen is the full moment, for a hover title: date and minute, no seconds. */
export function exactWhen(iso?: string): string {
  const at = parse(iso);
  if (!at) return "";
  return at.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** ENDPOINT_PREFIX is where every endpoint is served, which the column says already. */
const ENDPOINT_PREFIX = "/endpoints/";

/**
 * endpointTail is an endpoint's path without the prefix every one of them
 * shares, so the part that tells two endpoints apart is what the cell shows.
 */
export function endpointTail(path: string): string {
  return path.startsWith(ENDPOINT_PREFIX) ? path.slice(ENDPOINT_PREFIX.length) : path.replace(/^\/+/, "");
}

/** endpointURL is the full address an endpoint answers at, for a hover title. */
export function endpointURL(path: string, origin: string = typeof window === "undefined" ? "" : window.location.origin): string {
  return `${origin}${path.startsWith("/") ? path : `/${path}`}`;
}
