import {
  Home,
  Wrench,
  Users,
  LayoutGrid,
  Activity,
  FileText,
  Bot,
  Clock,
  Cable,
  KeyRound,
  MessageSquare,
  MessageCircle,
  FileUp,
  FileCode2,
  BookOpen,
  Contact,
  Settings,
  type LucideIcon,
} from "lucide-react";

export interface NavItem {
  path: string;
  label: string;
  icon: LucideIcon;
}

// Fixed order (#661): Assets, Prompts, Resources, Feedback, Knowledge,
// Activity. Knowledge is the single home for the Memory -> Insight -> Knowledge
// lifecycle (the former Knowledge Pages, Knowledge & Memory surfaces). Activity
// is the audit/landing view; Settings (per-user preferences, #631) trails the
// content sections.
export const portalNavItems: NavItem[] = [
  { path: "/", label: "Assets", icon: LayoutGrid },
  { path: "/prompts", label: "Prompts", icon: MessageSquare },
  { path: "/scripts", label: "Scripts", icon: FileCode2 },
  { path: "/resources", label: "Resources", icon: FileUp },
  { path: "/feedback", label: "Feedback", icon: MessageCircle },
  { path: "/knowledge", label: "Knowledge", icon: BookOpen },
  { path: "/activity", label: "Activity", icon: Activity },
  { path: "/settings", label: "Settings", icon: Settings },
];

// Alphabetized by label (case-insensitive). Dashboard is pinned at
// the top because it's the admin landing view; everything else sorts.
export const adminNavItems: NavItem[] = [
  { path: "/admin", label: "Dashboard", icon: Home },
  { path: "/admin/agent-instructions", label: "Agent Instructions", icon: Bot },
  { path: "/admin/api-catalogs", label: "API Catalogs", icon: BookOpen },
  { path: "/admin/assets", label: "Assets", icon: LayoutGrid },
  { path: "/admin/changelog", label: "Change Log", icon: Clock },
  { path: "/admin/connections", label: "Connections", icon: Cable },
  { path: "/admin/description", label: "Description", icon: FileText },
  { path: "/admin/keys", label: "Keys", icon: KeyRound },
  { path: "/admin/personas", label: "Personas", icon: Users },
  { path: "/admin/prompts", label: "Prompts", icon: MessageSquare },
  { path: "/admin/resources", label: "Resources", icon: FileUp },
  { path: "/admin/scripts", label: "Scripts", icon: FileCode2 },
  { path: "/admin/settings", label: "Settings", icon: Settings },
  { path: "/admin/tools", label: "Tools", icon: Wrench },
  { path: "/admin/users", label: "Users", icon: Contact },
];

/**
 * Sections with no routes beneath them: a deeper path is a different section,
 * so these match the route exactly rather than by prefix.
 */
const EXACT_MATCH_ONLY = new Set(["/admin", "/activity", "/prompts"]);

/**
 * The Assets item covers more than its own path: Collections lives under
 * Assets, as do the asset and collection viewers, so all of them keep the
 * Assets item lit.
 */
function isAssetsSection(route: string): boolean {
  return (
    route === "/" ||
    route === "/collections" ||
    route.startsWith("/collections/") ||
    route.startsWith("/assets/") ||
    route.startsWith("/shared/assets/")
  );
}

/**
 * The admin Assets item covers the same ground on the admin side: the
 * cross-owner collection list and the collection it opens are the Collections
 * face of that one section, not a section of their own (#1292).
 */
function isAdminAssetsSection(route: string): boolean {
  return (
    route === "/admin/assets" ||
    route.startsWith("/admin/assets/") ||
    route === "/admin/collections" ||
    route.startsWith("/admin/collections/")
  );
}

/**
 * isNavActive says whether `itemPath` is the section the reader is in.
 *
 * `currentPath` carries the query string and hash; the bare pathname is what
 * a nav item matches. A search-param deep link (e.g.
 * /admin/tools?selected=x&tab=tryit) would otherwise match no item at all and
 * the Tools item would lose its highlight until the next refresh.
 */
export function isNavActive(itemPath: string, currentPath: string): boolean {
  // Hash-based sub-routes (e.g. /admin/settings#description) — compare against
  // the full path including hash.
  if (itemPath.includes("#")) return currentPath === itemPath;

  const route = currentPath.split(/[?#]/)[0] ?? "/";

  if (itemPath === "/") return isAssetsSection(route);
  if (itemPath === "/admin/assets") return isAdminAssetsSection(route);
  if (EXACT_MATCH_ONLY.has(itemPath)) return route === itemPath;
  // Knowledge keeps its nav item active across the URL-addressable page routes
  // (/knowledge/pages and /knowledge/pages/:id), so the wiki view stays anchored
  // to the Knowledge section (#709).
  return route === itemPath || route.startsWith(itemPath + "/");
}
