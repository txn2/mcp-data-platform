import { useState, useEffect, useCallback, useRef } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { useAuthStore } from "@/stores/auth";

// Portal pages (everyone)
import { ActivityRoutes } from "@/pages/activity/ActivityRoutes";
import { MyAssetsPage } from "@/pages/assets/MyAssetsPage";
import { KnowledgeHub } from "@/pages/knowledge/KnowledgeHub";
import { MyPromptsPage } from "@/pages/prompts/MyPromptsPage";
import { PromptViewerPage } from "@/pages/prompts/PromptViewerPage";
import { PortalScriptRoutes } from "@/pages/scripts/ScriptRoutes";
import { AssetViewerPage } from "@/pages/viewer/AssetViewerPage";
import { CollectionsPage } from "@/pages/collections/CollectionsPage";
import { CollectionViewerPage } from "@/pages/collections/CollectionViewerPage";
import { CollectionEditorPage } from "@/pages/collections/CollectionEditorPage";
import { ResourcesPage } from "@/pages/resources/ResourcesPage";
import { FeedbackPage } from "@/pages/feedback/FeedbackPage";
import { UserSettingsPage } from "@/pages/settings/UserSettingsPage";

// Admin pages (admin only)
import { AdminAssetsPage } from "@/pages/assets/AdminAssetsPage";
import { AdminAssetViewerPage } from "@/pages/viewer/AdminAssetViewerPage";
import { AdminCollectionRoutes } from "@/pages/collections/AdminCollectionRoutes";
import { ToolsPage } from "@/pages/tools/ToolsPage";
import { AuditLogPage } from "@/pages/audit/AuditLogPage";
import { CallRoutes } from "@/pages/calls/CallRoutes";
import { SessionRoutes } from "@/pages/sessions/SessionRoutes";
import { ConfigEditorPage } from "@/pages/settings/ConfigEditorPage";
import { CatalogsPanel } from "@/pages/settings/CatalogsPanel";
import { ConnectionsPanel } from "@/pages/settings/ConnectionsPanel";
import { PersonasPanel } from "@/pages/settings/PersonasPanel";
import { AdminPromptsPage } from "@/pages/prompts/AdminPromptsPage";
import { AdminScriptsPage } from "@/pages/scripts/AdminScriptsPage";
import { KeysPage } from "@/pages/settings/KeysPage";
import { UsersPanel } from "@/pages/settings/UsersPanel";
import { ChangelogPage } from "@/pages/settings/ChangelogPage";
import { AdminSettingsPage } from "@/pages/settings/AdminSettingsPage";
import { AdminOnlyNotice, PageNotFound } from "./RouteFallbacks";
import { canonicalRoute, isAdminRoute, isKnownRoute } from "@/lib/portalRoutes";

const pageTitles: Record<string, string> = {
  "/activity": "Activity",
  "/activity/sessions": "Activity",
  "/activity/calls": "Activity",
  "/": "Assets",
  "/collections": "Collections",
  "/resources": "Resources",
  "/feedback": "Feedback",
  "/knowledge": "Knowledge",
  "/prompts": "Prompts",
  "/scripts": "Scripts",
  "/settings": "Settings",
  "/admin": "Dashboard",
  "/admin/assets": "Assets",
  "/admin/collections": "Collections",
  "/admin/tools": "Tools",
  "/admin/audit": "Dashboard",
  "/admin/description": "Description",
  "/admin/agent-instructions": "Agent Instructions",
  "/admin/api-catalogs": "API Catalogs",
  "/admin/connections": "Connections",
  "/admin/personas": "Personas",
  "/admin/prompts": "Prompts",
  "/admin/resources": "Resources",
  "/admin/scripts": "Scripts",
  "/admin/sessions": "Sessions",
  "/admin/calls": "Calls",
  "/admin/keys": "Keys",
  "/admin/users": "Users",
  "/admin/changelog": "Change Log",
  "/admin/settings": "Settings",
};

// pageTitleFor resolves the header title for a route with no detail view of
// its own in the chain below: a section's own detail routes answer here rather
// than adding a branch to it.
function pageTitleFor(route: string): string {
  if (route.startsWith("/scripts/")) return "Script";
  if (route.startsWith("/admin/collections/")) return "Collection";
  if (route.startsWith("/admin/sessions/")) return "Session";
  if (route.startsWith("/activity/sessions/")) return "Session";
  if (route.startsWith("/admin/calls/")) return "Call";
  if (route.startsWith("/activity/calls/")) return "Call";
  return pageTitles[route] ?? "Assets";
}

/**
 * resolveTitle decides the header title from what the main area is showing.
 *
 * The two refusals answer first, because falling through to a section name for
 * either of them is what made a missing page read as an empty one (#1359). The
 * rest are the detail views that carry a title of their own; anything left is
 * a plain route and answers from pageTitleFor.
 */
function resolveTitle(v: {
  route: string;
  notFound: boolean;
  adminBlocked: boolean;
  assetViewer: boolean;
  collectionEdit: boolean;
  collectionView: boolean;
  promptView: boolean;
  knowledge: boolean;
}): string {
  if (v.notFound) return "Not found";
  if (v.adminBlocked) return "Access denied";
  if (v.assetViewer) return "Asset Viewer";
  if (v.collectionEdit) return "Edit Collection";
  if (v.collectionView) return "Collection";
  if (v.promptView) return "Prompt";
  if (v.knowledge) return "Knowledge";
  return pageTitleFor(v.route);
}

const SIDEBAR_STORAGE_KEY = "sidebar-collapsed";

/** Vite base path — must match vite.config.ts `base`. */
const BASE = import.meta.env.BASE_URL.replace(/\/+$/, "");

/** Read the in-app path from the current URL. */
function readPath(): string {
  const { pathname, hash } = window.location;
  let route = pathname.startsWith(BASE)
    ? pathname.slice(BASE.length) || "/activity"
    : pathname;
  if (hash) route += hash;
  return route;
}

/** Routes that auto-collapse the sidebar (detail/viewer/editor views). */
function isAssetRoute(path: string): boolean {
  const route = path.split("#")[0] ?? "";
  return (
    /^\/assets\/.+$/.test(route) ||
    /^\/admin\/assets\/.+$/.test(route) ||
    /^\/collections\/.+\/assets\/.+$/.test(route) ||
    /^\/shared\/assets\/.+$/.test(route) ||
    /^\/prompts\/.+$/.test(route) ||
    /^\/admin\/personas$/.test(route)
  );
}

const MOBILE_BREAKPOINT = 768;

function useIsMobile() {
  const [isMobile, setIsMobile] = useState(
    () => typeof window !== "undefined" && window.innerWidth < MOBILE_BREAKPOINT,
  );
  useEffect(() => {
    const mq = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
    const handler = (e: MediaQueryListEvent) => setIsMobile(e.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);
  return isMobile;
}

export function AppShell() {
  const [currentPath, setCurrentPath] = useState(readPath);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const isMobile = useIsMobile();
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);

  // Sidebar collapsed state: auto-collapse on asset deep-link, otherwise restore from localStorage
  const initialPath = useRef(readPath()).current;
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    if (isAssetRoute(initialPath)) return true;
    return localStorage.getItem(SIDEBAR_STORAGE_KEY) === "true";
  });
  // Track whether we auto-collapsed so we can restore on navigation away
  const autoCollapsed = useRef(isAssetRoute(initialPath));

  const toggleSidebar = useCallback(() => {
    setSidebarCollapsed((prev) => {
      const next = !prev;
      localStorage.setItem(SIDEBAR_STORAGE_KEY, String(next));
      autoCollapsed.current = false; // user explicitly toggled
      return next;
    });
  }, []);

  // Auto-collapse when entering asset routes, restore when leaving
  const prevPath = useRef(currentPath);
  useEffect(() => {
    if (prevPath.current === currentPath) return;
    const wasOnAsset = isAssetRoute(prevPath.current);
    const onAsset = isAssetRoute(currentPath);
    prevPath.current = currentPath;

    if (onAsset && !wasOnAsset && !sidebarCollapsed) {
      setSidebarCollapsed(true);
      autoCollapsed.current = true;
    } else if (!onAsset && wasOnAsset && autoCollapsed.current) {
      const stored = localStorage.getItem(SIDEBAR_STORAGE_KEY) === "true";
      setSidebarCollapsed(stored);
      autoCollapsed.current = false;
    }
  }, [currentPath, sidebarCollapsed]);

  // replace: true rewrites the current entry rather than pushing one, which is
  // what a canonical redirect needs: pushing would leave the redirecting path
  // behind, so Back would land on it and redirect forward again (#1359).
  const navigate = useCallback((path: string, opts?: { replace?: boolean }) => {
    const hashIdx = path.indexOf("#");
    const pathname = hashIdx >= 0 ? path.slice(0, hashIdx) : path;
    const hash = hashIdx >= 0 ? path.slice(hashIdx) : "";
    const target = BASE + pathname + hash;
    // A navigation to the URL already in the bar (re-selecting the open page,
    // cancelling a form that never changed the path) must not push a duplicate
    // history entry, which would make browser Back appear to do nothing. Sync the
    // in-memory path and stop. (#709)
    //
    // The comparison MUST include window.location.search: `target` carries the
    // query string (navigate splits only on "#", so a "?selected=..." stays in
    // pathname), but window.location.pathname does not. Omitting search would
    // make a navigate("/admin/tools") land as a false no-op whenever a
    // "?selected=..." deep link is active, so the Tools nav link could never
    // clear a deep-linked selection (issue #859).
    if (
      target ===
      window.location.pathname + window.location.search + window.location.hash
    ) {
      setCurrentPath(path);
      return;
    }
    // Record the path we are leaving so a detail view can offer a wiki-style Back:
    // return to the previous knowledge page when that is where we came from, and
    // fall back to the section index otherwise (an asset viewer, a search result,
    // or a cold deep-link, where state is null on the initial entry). (#709)
    const from = readPath();
    setCurrentPath(path);
    if (opts?.replace) {
      window.history.replaceState({ appNav: true, from }, "", target);
      return;
    }
    window.history.pushState({ appNav: true, from }, "", target);
  }, []);

  useEffect(() => {
    const onPop = () => setCurrentPath(readPath());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const hashIdx = currentPath.indexOf("#");
  const initialTab = hashIdx >= 0 ? currentPath.slice(hashIdx + 1) : undefined;
  // route is the pathname only: strip the query string AND the hash (whichever
  // comes first). navigate() keeps a target's query string on currentPath so a
  // search-param deep link (e.g. /admin/tools?selected=x&tab=tryit) survives to
  // the page, but the page-selection matches below are exact (route === "/x"),
  // so an unstripped query string would fail every match. ToolsPage reads its
  // selection from window.location.search independently.
  const sepIdx = currentPath.search(/[?#]/);
  const route = sepIdx >= 0 ? currentPath.slice(0, sepIdx) : currentPath;

  // A path that names a surface which moved ("Shared With Me" folded into the
  // Assets scope filter; the three knowledge surfaces merged into one hub,
  // #661), or that carries a trailing slash, is sent to the one the reader
  // meant. That table lives in lib/portalRoutes so the redirects and the
  // recognition below cannot disagree about which paths are real (#1359).
  const redirectTo = canonicalRoute(route);
  useEffect(() => {
    if (redirectTo) navigate(redirectTo, { replace: true });
  }, [redirectTo, navigate]);

  // Asset viewer routes
  const collectionAssetMatch = route.match(/^\/collections\/([^/]+)\/assets\/(.+)$/);
  const sharedAssetMatch = route.match(/^\/shared\/assets\/(.+)$/);
  const assetMatch = route.match(/^\/assets\/(.+)$/);
  const adminAssetMatch = route.match(/^\/admin\/assets\/(.+)$/);
  const promptViewMatch = route.match(/^\/prompts\/(.+)$/);
  // Knowledge-page routes (#709): the page list and the URL-addressable page
  // detail. Both render the Knowledge hub focused on its Knowledge Pages sub-tab.
  const knowledgePageMatch = route.match(/^\/knowledge\/pages\/(.+)$/);
  const knowledgePagesList = route === "/knowledge/pages";
  // The Catalog route (#719), first-class like /knowledge/pages so it deep-links
  // and survives refresh. It is the one route for every DataHub-backed surface:
  // Tables, Context Docs, and Tags are inner tabs addressed in its hash (#1194),
  // so there is no /knowledge/tags or /knowledge/context-docs.
  const catalogRoute = route === "/knowledge/catalog";
  const knowledgeRouteSub = knowledgePagesList || knowledgePageMatch
    ? "pages"
    : catalogRoute
      ? "catalog"
      : undefined;
  // Collection routes: /collections/:id and /collections/:id/edit
  const collectionEditMatch = route.match(/^\/collections\/([^/]+)\/edit$/);
  const collectionViewMatch = !collectionEditMatch && !collectionAssetMatch
    ? route.match(/^\/collections\/([^/]+)$/)
    : null;

  const adminRoute = isAdminRoute(route);
  // A path the switch below renders nothing for gets a page saying so (#1359).
  // A path being redirected is not one of those, or the render before the
  // effect lands would flash a refusal at somebody whose link works; and an
  // unknown path under /admin is not singled out for a non-admin, who gets the
  // notice every path in that section gets rather than an enumeration of which
  // administrator routes are real.
  const adminBlocked = adminRoute && !isAdmin;
  const notFound = !redirectTo && !isKnownRoute(route) && !adminBlocked;

  const title = resolveTitle({
    route,
    notFound,
    adminBlocked,
    assetViewer: !!(collectionAssetMatch || sharedAssetMatch || assetMatch || adminAssetMatch),
    collectionEdit: !!collectionEditMatch,
    collectionView: !!collectionViewMatch,
    promptView: !!promptViewMatch,
    knowledge: knowledgePagesList || !!knowledgePageMatch || catalogRoute,
  });

  return (
    <div className="flex h-screen">
      {/* Desktop sidebar */}
      {!isMobile && (
        <Sidebar
          currentPath={currentPath}
          onNavigate={navigate}
          collapsed={sidebarCollapsed}
          onToggleCollapse={toggleSidebar}
        />
      )}

      {/* Mobile sidebar overlay */}
      {isMobile && mobileSidebarOpen && (
        <>
          <div
            className="fixed inset-0 z-40 bg-black/50"
            onClick={() => setMobileSidebarOpen(false)}
          />
          <div className="fixed inset-y-0 left-0 z-50">
            <Sidebar
              currentPath={currentPath}
              onNavigate={navigate}
              collapsed={false}
              onToggleCollapse={() => {}}
              mobile
              onClose={() => setMobileSidebarOpen(false)}
            />
          </div>
        </>
      )}

      <div className="flex flex-1 flex-col overflow-hidden">
        <Header
          title={title}
          onMenuClick={isMobile ? () => setMobileSidebarOpen(true) : undefined}
        />
        {/* The page surface is `--background` itself, not a wash of `--muted`
            over it: cards read as raised against the page only if the page is
            the ramp's own middle step, and a muted wash would land on top of
            the fills (tab tracks, code blocks) that also derive from muted. */}
        <main className="flex-1 overflow-auto bg-background p-3 sm:p-6">
          {notFound && <PageNotFound route={route} onNavigate={navigate} />}

          {/* Portal routes — everyone */}
          {!adminRoute && <ActivityRoutes route={route} onNavigate={navigate} />}
          {!adminRoute && route === "/" && (
            <MyAssetsPage onNavigate={navigate} />
          )}
          {!adminRoute && route === "/collections" && (
            <CollectionsPage onNavigate={navigate} />
          )}
          {collectionViewMatch && (
            <CollectionViewerPage
              collectionId={collectionViewMatch[1]!}
              onNavigate={navigate}
              onBack={() => navigate("/collections")}
            />
          )}
          {collectionEditMatch && (
            <CollectionEditorPage
              collectionId={collectionEditMatch[1]!}
              onBack={() => navigate(`/collections/${collectionEditMatch[1]!}`)}
              onNavigate={navigate}
            />
          )}
          {!adminRoute && route === "/resources" && (
            <ResourcesPage onNavigate={navigate} />
          )}
          {!adminRoute && route === "/feedback" && <FeedbackPage onNavigate={navigate} />}
          {!adminRoute && route === "/settings" && (
            <UserSettingsPage onNavigate={navigate} />
          )}
          {!adminRoute &&
            (route === "/knowledge" ||
              knowledgePagesList ||
              knowledgePageMatch ||
              catalogRoute) && (
              <KnowledgeHub
                key={currentPath}
                initialTab={initialTab}
                initialPageId={knowledgePageMatch?.[1]}
                routeSub={knowledgeRouteSub}
                onNavigate={navigate}
              />
            )}
          {!adminRoute && route === "/prompts" && <MyPromptsPage onNavigate={navigate} />}
          {!adminRoute && promptViewMatch && (
            <PromptViewerPage
              promptId={promptViewMatch[1]!}
              onNavigate={navigate}
              onBack={() => navigate("/prompts")}
            />
          )}
          {!adminRoute && <PortalScriptRoutes route={route} onNavigate={navigate} />}
          {collectionAssetMatch && (
            <AssetViewerPage assetId={collectionAssetMatch[2]!} onNavigate={navigate} onBack={() => navigate(`/collections/${collectionAssetMatch[1]!}`)} />
          )}
          {sharedAssetMatch && (
            <AssetViewerPage assetId={sharedAssetMatch[1]!} onNavigate={navigate} onBack={() => navigate("/")} />
          )}
          {assetMatch && (
            <AssetViewerPage assetId={assetMatch[1]!} onNavigate={navigate} onBack={() => navigate("/")} />
          )}

          {/* Admin routes — admin only (defense in depth) */}
          {adminRoute && !isAdmin && <AdminOnlyNotice />}
          {adminRoute && isAdmin && (
            <>
              {/* Dashboard now hosts the merged MCP / API Gateway / Events
                  activity views (was a separate Audit Log page). */}
              {(route === "/admin" || route === "/admin/audit") && (
                <AuditLogPage
                  key={currentPath}
                  initialTab={initialTab}
                  onNavigate={navigate}
                />
              )}
              {route === "/admin/assets" && <AdminAssetsPage onNavigate={navigate} />}
              {adminAssetMatch && (
                <AdminAssetViewerPage
                  assetId={adminAssetMatch[1]!}
                  onNavigate={navigate}
                />
              )}
              <AdminCollectionRoutes route={route} onNavigate={navigate} />
              {route === "/admin/tools" && (
                <ToolsPage key={currentPath} initialTab={initialTab} />
              )}
              {route === "/admin/description" && (
                <ConfigEditorPage configKey="server.description" label="Description" description="Platform identity visible to MCP clients" />
              )}
              {route === "/admin/agent-instructions" && (
                <ConfigEditorPage configKey="server.agent_instructions" label="Agent Instructions" description="Guidance for AI agents using this platform" showPlatformBaseline />
              )}
              {route === "/admin/api-catalogs" && <CatalogsPanel />}
              {route === "/admin/connections" && <ConnectionsPanel />}
              {route === "/admin/personas" && <PersonasPanel />}
              {route === "/admin/prompts" && (
                <AdminPromptsPage onNavigate={navigate} />
              )}
              {route === "/admin/resources" && (
                <ResourcesPage admin onNavigate={navigate} />
              )}
              {route === "/admin/scripts" && <AdminScriptsPage />}
              <SessionRoutes route={route} onNavigate={navigate} />
              <CallRoutes route={route} onNavigate={navigate} />
              {route === "/admin/keys" && <KeysPage />}
              {route === "/admin/users" && <UsersPanel />}
              {route === "/admin/changelog" && <ChangelogPage />}
              {route === "/admin/settings" && <AdminSettingsPage />}
            </>
          )}
        </main>
      </div>
    </div>
  );
}
