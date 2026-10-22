/**
 * A persona's portal permissions (#2225): one level per area, enforced by the
 * server on every route of the area and reported on /api/v1/portal/me so the
 * navigation shows only what the persona has. The server reports every area
 * resolved; resolvePortal fills what an older server leaves out with the
 * portal as it was before the setting existed, which was every area.
 */

export type AssetsLevel = "shared" | "own";
export type ResourcesLevel = "none" | "shared" | "own" | "persona";
export type ReadWriteLevel = "none" | "read" | "write";
export type APIsLevel = "none" | "browse";

export interface PortalAccess {
  assets?: AssetsLevel;
  resources?: ResourcesLevel;
  knowledge?: ReadWriteLevel;
  catalog?: ReadWriteLevel;
  apis?: APIsLevel;
  feedback?: boolean;
  prompts?: boolean;
  automations?: boolean;
  search?: boolean;
  memory?: boolean;
  download?: boolean;
  /** The caller's persona is external (#2092): no people directory or keys. */
  external?: boolean;
}

export type ResolvedPortal = Required<PortalAccess>;

/** Every area at its highest level: what an administrator has. */
export const FULL_PORTAL: ResolvedPortal = {
  assets: "own",
  resources: "persona",
  knowledge: "write",
  catalog: "write",
  apis: "browse",
  feedback: true,
  prompts: true,
  automations: true,
  search: true,
  memory: true,
  download: true,
  external: false,
};

export function resolvePortal(p?: PortalAccess | null): ResolvedPortal {
  return { ...FULL_PORTAL, ...stripUndefined(p ?? {}) };
}

function stripUndefined(p: PortalAccess): PortalAccess {
  return Object.fromEntries(Object.entries(p).filter(([, v]) => v !== undefined)) as PortalAccess;
}

/**
 * Whether a portal navigation section is part of the persona's portal. A path
 * no area governs is always shown.
 */
export function portalSectionVisible(path: string, p: ResolvedPortal): boolean {
  switch (path) {
    case "/resources":
      return p.resources !== "none";
    case "/apis":
      return p.apis !== "none";
    case "/automations":
      return p.automations;
    case "/feedback":
      return p.feedback;
    case "/prompts":
      return p.prompts;
    case "/knowledge":
      return p.knowledge !== "none" || p.catalog !== "none" || p.memory || p.search;
    default:
      return true;
  }
}

/** The section a route belongs to, when an area governs it. */
export function portalSectionOf(route: string): string | null {
  for (const section of ["/resources", "/apis", "/automations", "/feedback", "/prompts", "/knowledge"]) {
    if (route === section || route.startsWith(section + "/")) return section;
  }
  return null;
}
