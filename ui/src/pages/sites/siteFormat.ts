import { compareNames } from "@/lib/sortByName";
import { formatBytes } from "@/pages/scripts/runFormat";
import type { Site, SiteDomain, SiteEnvironment } from "@/api/portal/hooks/sites";
import { DEV_ENVIRONMENT } from "@/api/portal/hooks/sites";

export { formatBytes };

const dateTime = new Intl.DateTimeFormat("en-US", {
  year: "numeric", month: "2-digit", day: "2-digit", hour: "numeric", minute: "2-digit",
});
const dateOnly = new Intl.DateTimeFormat("en-US", { year: "numeric", month: "2-digit", day: "2-digit" });

export function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : dateTime.format(d).replace(",", "");
}

export function day(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : dateOnly.format(d);
}

export function errorText(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback;
}

/** The hostnames added to one environment. */
export function hostsOf(site: Site, env: string): SiteDomain[] {
  return site.domains.filter((d) => d.environment === env);
}

/** A hostname as a link target, on the scheme the site's pages addresses use. */
export function hostUrl(site: Site, host: string): string {
  const any = site.address || Object.values(site.pages_addresses ?? {}).find(Boolean) || "";
  const secure = !any || any.startsWith("https:");
  return `${secure ? "https" : "http"}://${host}`;
}

/** An address without its scheme, for reading. */
export function bareAddress(address: string): string {
  return address.replace(/^https?:\/\//, "");
}

/** Two letters for a person from their address: the first letters of the first two parts of its name, else its first two letters. */
export function initials(email: string): string {
  const local = email.split("@")[0] ?? "";
  const parts = local.split(/[._+-]+/).filter(Boolean);
  const letters = parts.length >= 2 ? `${parts[0]![0]}${parts[1]![0]}` : local.slice(0, 2);
  return letters.toUpperCase() || "?";
}

/** An environment as the Sites list badges it (#2612): its name and the release it serves. */
export function envBadgeText(env: Pick<SiteEnvironment, "name" | "release">): string {
  return env.release > 0 ? `${env.name} ${env.release}` : `${env.name} · no release`;
}

/** What an environment's badge says on hover: the release, who can open it, and since when. */
export function envTip(env: Pick<SiteEnvironment, "name" | "release" | "public" | "updated_at">): string {
  const access = env.public ? "public" : "private";
  if (env.release <= 0) return `${env.name} has no release yet, ${access}`;
  const since = day(env.updated_at);
  return `${env.name} serves release ${env.release}, ${access}${since ? `, since ${since}` : ""}`;
}

// The names a release usually reaches last, so they are drawn at the end of the
// promote order.
const LAST = ["prod", "production", "live"];

/** dev first and prod last, the rest by name between: the order a release is promoted in. */
export function orderedEnvironments(envs: SiteEnvironment[]): SiteEnvironment[] {
  const rank = (n: string) => (n === DEV_ENVIRONMENT ? 0 : LAST.includes(n) ? 2 : 1);
  return [...envs].sort((a, b) => rank(a.name) - rank(b.name) || compareNames(a.name, b.name));
}

/** An environment's address on the pages domain: the main address for the one holding it, "" with none (#2611). */
export function envAddress(site: Site, env: string): string {
  return site.pages_addresses?.[env] ?? "";
}

/** Where people reach env (#2607): its first domain, else its pages address, as manage_site get answers. */
export function envOpenUrl(site: Site, env: string): string {
  return site.addresses?.[env] ?? "";
}

/** One address an environment card lists, in the order it lists them. */
export interface CardAddress {
  href: string;
  text: string;
  tag: "Domain" | "Main address" | "Pages address";
}

/** An environment's addresses as its card lists them (#2607): its domains first, then its pages address. */
export function cardAddresses(site: Site, env: string): CardAddress[] {
  const out: CardAddress[] = hostsOf(site, env).map((d) => ({ href: hostUrl(site, d.hostname), text: d.hostname, tag: "Domain" }));
  const own = envAddress(site, env);
  if (own) out.push({ href: own, text: bareAddress(own), tag: site.main_environment === env ? "Main address" : "Pages address" });
  return out;
}
