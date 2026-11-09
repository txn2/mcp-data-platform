import { compareNames } from "@/lib/sortByName";
import { formatBytes } from "@/pages/scripts/runFormat";
import type { Site, SiteDomain, SiteEnvironment } from "@/api/portal/hooks/sites";
import { DEFAULT_ENVIRONMENT } from "@/api/portal/hooks/sites";

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

/** A hostname as a link target, on the scheme the site's own address uses. */
export function hostUrl(site: Site, host: string): string {
  const secure = !site.address || site.address.startsWith("https:");
  return `${secure ? "https" : "http"}://${host}`;
}

/** The site's own address without its scheme, for reading. */
export function bareAddress(address: string): string {
  return address.replace(/^https?:\/\//, "");
}

/** Default first, then by name, so a site reads the way it was built up. */
export function orderedEnvironments(envs: SiteEnvironment[]): SiteEnvironment[] {
  return [...envs].sort((a, b) => {
    if (a.name === DEFAULT_ENVIRONMENT) return -1;
    if (b.name === DEFAULT_ENVIRONMENT) return 1;
    return compareNames(a.name, b.name);
  });
}
