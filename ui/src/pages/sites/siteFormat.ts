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
  const any = site.address || Object.values(site.addresses ?? {}).find(Boolean) || "";
  const secure = !any || any.startsWith("https:");
  return `${secure ? "https" : "http"}://${host}`;
}

/** An address without its scheme, for reading. */
export function bareAddress(address: string): string {
  return address.replace(/^https?:\/\//, "");
}

// The names a release usually reaches last, so they are drawn at the end of the
// promote order.
const LAST = ["prod", "production", "live"];

/** dev first and prod last, the rest by name between: the order a release is promoted in. */
export function orderedEnvironments(envs: SiteEnvironment[]): SiteEnvironment[] {
  const rank = (n: string) => (n === DEV_ENVIRONMENT ? 0 : LAST.includes(n) ? 2 : 1);
  return [...envs].sort((a, b) => rank(a.name) - rank(b.name) || compareNames(a.name, b.name));
}

/** An environment's address on the pages domain: the main address for the one holding it, "" with no pages domain. */
export function envAddress(site: Site, env: string): string {
  return site.addresses?.[env] ?? "";
}

/** Where a release in env can be opened: its pages address, else its first domain. */
export function envOpenUrl(site: Site, env: string): string {
  const own = envAddress(site, env);
  if (own) return own;
  const host = hostsOf(site, env)[0];
  return host ? hostUrl(site, host.hostname) : "";
}
