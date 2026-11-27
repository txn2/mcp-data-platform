import type { Plugin } from "vite";

/**
 * The third-party addresses ReDoc 3 fetches from on its own, each with what it
 * is replaced by (#2769).
 *
 * The API reference is one of the portal's pages, read by a deployment's
 * administrators, and nothing about that reading leaves the deployment: an
 * air-gapped one has no route out, and any other would be telling Redocly
 * which hosts run the platform and when their administrators read its admin
 * surface (an image request carries the page's origin as its Referer).
 *
 * - The "API docs by Redocly" logo, drawn unconditionally in the menu's footer
 *   from Redocly's CDN. Replaced with an empty SVG, so nothing is fetched and
 *   nothing broken is drawn; redoc.css hides the footer link it sits in.
 * - The telemetry collector, the default of a tracer that ships every page view
 *   to Redocly. `disableTelemetry` (redocTheme.ts) turns it off; with no
 *   address the tracer is never built even if that option were lost.
 * - The two icon sets ReDoc can draw from Redocly's CDN. An icon it would
 *   fetch resolves against the portal's own origin instead, where it is a 404.
 */
export const REDOC_THIRD_PARTY: ReadonlyArray<readonly [string, string]> = [
  [
    "https://cdn.redoc.ly/redoc/logo-mini-full.svg",
    "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg'/%3E",
  ],
  [
    "https://cdn.redoc.ly/redoc/logo-mini.svg",
    "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg'/%3E",
  ],
  ["https://otel.cloud.redocly.com/v1/traces", ""],
  ["https://cdn.redocly.com/icons/fa/7.0.0", ""],
  ["https://cdn.redocly.com/icons/code/5.24.0", ""],
];

/** FETCHED_HOSTS are the Redocly hosts ReDoc fetches from rather than links to. */
export const FETCHED_HOSTS = ["cdn.redoc.ly", "cdn.redocly.com", "otel.cloud.redocly.com"];

const REDOC_MODULE = /[\\/]node_modules[\\/]redoc[\\/]bundles[\\/]redoc(\.standalone)?\.js$/;

/**
 * stripRedocThirdParty is the rewrite: every address above replaced, and an
 * error if ReDoc still names one of its fetch hosts afterwards. A new ReDoc
 * that adds a host or moves a version in an icon URL fails the build here,
 * rather than shipping a page that calls out.
 */
export function stripRedocThirdParty(code: string): string {
  let out = code;
  for (const [from, to] of REDOC_THIRD_PARTY) {
    out = out.split(from).join(to);
  }
  const left = FETCHED_HOSTS.filter((h) => out.includes(h));
  if (left.length > 0) {
    throw new Error(
      `redoc-offline: ReDoc still fetches from ${left.join(", ")}; add the address to REDOC_THIRD_PARTY (ui/redocOfflinePlugin.ts)`,
    );
  }
  return out;
}

/** isRedocModule reports whether a module id is ReDoc's own bundle. */
export function isRedocModule(id: string): boolean {
  return REDOC_MODULE.test(id.split("?")[0] ?? "");
}

/**
 * redocOffline applies the rewrite to ReDoc's bundle. The same plugin is given
 * to the dependency optimizer (vite.config.ts), which pre-bundles ReDoc for the
 * dev server before any ordinary plugin sees it, so dev and the build serve the
 * same bytes.
 */
export function redocOffline(): Plugin {
  return {
    name: "redoc-offline",
    transform(code, id) {
      if (!isRedocModule(id)) return null;
      return { code: stripRedocThirdParty(code), map: null };
    },
  };
}
