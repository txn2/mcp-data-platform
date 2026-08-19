// The set of paths the portal shell renders something for, in one module.
//
// The shell is a switch: each page is a condition on the path, and a path that
// matches no condition renders nothing. That is silent by construction, and
// what it produces is the worst of the three readings a person could take from
// it — /assets/ renders the chrome, the title "Assets", and an empty content
// area, which is indistinguishable from being told they own no assets. Nothing
// was even requested; the page never asked (#1359).
//
// So the shell asks this module first. A path with a canonical form redirects
// to it, a path with no page says so, and only a recognized path reaches the
// switch. Recognition lives here rather than as a final `else` inside the
// switch because the switch is spread across five section components that each
// own their own matching, and none of them can see whether another matched.

/** ADMIN_PREFIX is the segment that makes a route the administrator's. */
const ADMIN_PREFIX = "/admin";

// KNOWN_ROUTES are the paths the shell renders a page for exactly. A section
// with a detail view lists its index here and its detail pattern below.
const KNOWN_ROUTES: readonly string[] = [
  "/",
  "/activity",
  "/activity/sessions",
  "/activity/calls",
  "/collections",
  "/resources",
  "/feedback",
  "/settings",
  "/knowledge",
  "/knowledge/pages",
  "/knowledge/catalog",
  "/prompts",
  "/scripts",
  "/admin",
  "/admin/assets",
  "/admin/audit",
  "/admin/collections",
  "/admin/tools",
  "/admin/description",
  "/admin/agent-instructions",
  "/admin/api-catalogs",
  "/admin/connections",
  "/admin/personas",
  "/admin/prompts",
  "/admin/resources",
  "/admin/scripts",
  "/admin/sessions",
  "/admin/calls",
  "/admin/keys",
  "/admin/users",
  "/admin/changelog",
  "/admin/settings",
];

// KNOWN_PATTERNS are the detail routes: an index above plus an identifier. The
// identifier is matched as "anything non-empty" rather than as a uuid, because
// several of these carry an encoded session or call id rather than a uuid, and
// a page that loads and reports "no such record" is a better answer to a
// mistyped id than a not-found page that never asked.
const KNOWN_PATTERNS: readonly RegExp[] = [
  /^\/assets\/.+$/,
  /^\/shared\/assets\/.+$/,
  /^\/activity\/sessions\/.+$/,
  /^\/activity\/calls\/.+$/,
  /^\/collections\/[^/]+$/,
  /^\/collections\/[^/]+\/edit$/,
  /^\/collections\/[^/]+\/assets\/.+$/,
  /^\/knowledge\/pages\/.+$/,
  /^\/prompts\/.+$/,
  /^\/scripts\/.+$/,
  /^\/admin\/assets\/.+$/,
  /^\/admin\/collections\/.+$/,
  /^\/admin\/sessions\/.+$/,
  /^\/admin\/calls\/.+$/,
];

// ALIASES are paths that named a real surface and no longer do. Each redirects
// to where that surface lives now, so a bookmark, a link written before a route
// settled, and a guessed plural all land on the page the reader meant.
//
// The knowledge entries are the IA unification (#661): three surfaces became
// one hub with three tabs. The assets entries are the guess: the section is
// mounted at the portal root, so the name it is called everywhere else in the
// product is a path nobody had any reason to think was wrong.
// A Map rather than an object: this is looked up with a path off the address
// bar, and an object would answer "/constructor" or "/__proto__" out of its
// prototype if either ever became a route shape.
const ALIASES: ReadonlyMap<string, string> = new Map([
  ["/assets", "/"],
  ["/shared", "/"],
  ["/knowledge-pages", "/knowledge#knowledge"],
  ["/my-knowledge", "/knowledge#insights"],
  ["/admin/knowledge", "/knowledge#insights"],
]);

/** isAdminRoute reports whether a path belongs to the administrator's section,
 * which the shell answers for before it answers whether the path exists. */
export function isAdminRoute(route: string): boolean {
  return route === ADMIN_PREFIX || route.startsWith(`${ADMIN_PREFIX}/`);
}

/**
 * isInSection reports whether a path belongs to the section mounted at prefix
 * — the prefix itself or anything under it.
 *
 * The shell uses this to decide whether to mount a section at all. Five of the
 * pages are section components that own their own matching and render null for
 * a path outside them (ActivityRoutes, PortalScriptRoutes,
 * AdminCollectionRoutes, SessionRoutes, CallRoutes), so the shell used to
 * mount all five on every route and let four of them decline. That was free
 * when every page was in one bundle. It is not free now that each page is its
 * own chunk: mounting a section is what fetches it, so an unguarded section
 * would download five sections' code to render one (#1351).
 *
 * Each of the five owns exactly one prefix, which is what makes a prefix test
 * the right shape here rather than a second copy of their matching.
 */
export function isInSection(route: string, prefix: string): boolean {
  return route === prefix || route.startsWith(`${prefix}/`);
}

/** isKnownRoute reports whether the shell renders a page for this path. */
export function isKnownRoute(route: string): boolean {
  if (KNOWN_ROUTES.includes(route)) return true;
  return KNOWN_PATTERNS.some((pattern) => pattern.test(route));
}

/**
 * canonicalRoute is where a path should be sent instead of being rendered, or
 * null when the path is already the one to render.
 *
 * Two rules, in order. A retired or guessed name redirects to the surface it
 * meant. Otherwise a trailing slash is dropped when what is left is a real
 * route, because "/scripts/" is a typing artifact rather than a request for a
 * script whose id is the empty string.
 *
 * A path that is neither is left alone: an unknown path is a not-found page,
 * not a redirect to somewhere the reader did not ask for.
 */
export function canonicalRoute(route: string): string | null {
  const alias = ALIASES.get(route);
  if (alias) return alias;
  if (route.length > 1 && route.endsWith("/")) {
    const trimmed = route.replace(/\/+$/, "") || "/";
    const target = ALIASES.get(trimmed) ?? (isKnownRoute(trimmed) ? trimmed : null);
    if (target) return target;
  }
  return null;
}
