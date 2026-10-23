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
export type ScratchTablesLevel = "none" | "list" | "register";

/** Who an external persona's uploads are shared with (#2258). */
export interface PortalUploads {
  share_personas?: string[];
  share_emails?: string[];
}

export interface PortalAccess {
  assets?: AssetsLevel;
  resources?: ResourcesLevel;
  knowledge?: ReadWriteLevel;
  catalog?: ReadWriteLevel;
  apis?: APIsLevel;
  /** The scratch-table routes and page (#2257). */
  scratch_tables?: ScratchTablesLevel;
  feedback?: boolean;
  prompts?: boolean;
  automations?: boolean;
  search?: boolean;
  memory?: boolean;
  /** Exporting an asset or a file as PDF; the editor names it PDF export (#2257). */
  download?: boolean;
  /** The caller's persona is external (#2092): no people directory or keys. */
  external?: boolean;
  /** An external persona's uploads, absent when it has none (#2258). */
  uploads?: PortalUploads | null;
}

export type ResolvedPortal = Required<Omit<PortalAccess, "uploads">> & { uploads: PortalUploads | null };

/** Every area at its highest level: what an administrator has. */
export const FULL_PORTAL: ResolvedPortal = {
  assets: "own",
  resources: "persona",
  knowledge: "write",
  catalog: "write",
  apis: "browse",
  scratch_tables: "register",
  feedback: true,
  prompts: true,
  automations: true,
  search: true,
  memory: true,
  download: true,
  external: false,
  uploads: null,
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
  const visible = SECTION_VISIBLE[path];
  return visible ? visible(p) : true;
}

// SECTION_VISIBLE is each governed navigation section's rule.
const SECTION_VISIBLE: Record<string, (p: ResolvedPortal) => boolean> = {
  "/resources": (p) => p.resources !== "none",
  "/apis": (p) => p.apis !== "none",
  "/scratch-tables": (p) => p.scratch_tables !== "none",
  "/automations": (p) => p.automations,
  "/feedback": (p) => p.feedback,
  "/prompts": (p) => p.prompts,
  "/knowledge": (p) => p.knowledge !== "none" || p.catalog !== "none" || p.memory || p.search,
};

/** The section a route belongs to, when an area governs it. */
export function portalSectionOf(route: string): string | null {
  for (const section of ["/resources", "/apis", "/scratch-tables", "/automations", "/feedback", "/prompts", "/knowledge"]) {
    if (route === section || route.startsWith(section + "/")) return section;
  }
  return null;
}
