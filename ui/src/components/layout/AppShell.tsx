import { Suspense, lazy, useState, useEffect, useCallback, useRef } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { useAuthStore } from "@/stores/auth";

// Every page below is its own chunk. The shell used to import all of them
// statically, which put the whole portal — CodeMirror, the chart library, the
// diagram engine and thirty pages — into one 3.1 MB script that a cold cache
// had to download and evaluate before it could paint anything, on every route
// including the login screen (#1351). Now a visit fetches the shell and the
// one page it landed on.
//
// Each page keeps its named export, so it stays importable by name from its
// tests; lazy() is given the {default} shape it wants at this call site, which
// also preserves each page's prop types.

// Portal pages (everyone)
const ActivityRoutes = lazy(() =>
  import("@/pages/activity/ActivityRoutes").then((m) => ({ default: m.ActivityRoutes })),
);
const MyAssetsPage = lazy(() =>
  import("@/pages/assets/MyAssetsPage").then((m) => ({ default: m.MyAssetsPage })),
);
const KnowledgeHub = lazy(() =>
  import("@/pages/knowledge/KnowledgeHub").then((m) => ({ default: m.KnowledgeHub })),
);
const MyPromptsPage = lazy(() =>
  import("@/pages/prompts/MyPromptsPage").then((m) => ({ default: m.MyPromptsPage })),
);
const PromptViewerPage = lazy(() =>
  import("@/pages/prompts/PromptViewerPage").then((m) => ({ default: m.PromptViewerPage })),
);
const PortalScriptRoutes = lazy(() =>
  import("@/pages/scripts/ScriptRoutes").then((m) => ({ default: m.PortalScriptRoutes })),
);
const AssetViewerPage = lazy(() =>
  import("@/pages/viewer/AssetViewerPage").then((m) => ({ default: m.AssetViewerPage })),
);
const CollectionsPage = lazy(() =>
  import("@/pages/collections/CollectionsPage").then((m) => ({ default: m.CollectionsPage })),
);
const CollectionViewerPage = lazy(() =>
  import("@/pages/collections/CollectionViewerPage").then((m) => ({ default: m.CollectionViewerPage })),
);
const CollectionEditorPage = lazy(() =>
  import("@/pages/collections/CollectionEditorPage").then((m) => ({ default: m.CollectionEditorPage })),
);
const ResourcesPage = lazy(() =>
  import("@/pages/resources/ResourcesPage").then((m) => ({ default: m.ResourcesPage })),
);
const FeedbackPage = lazy(() =>
  import("@/pages/feedback/FeedbackPage").then((m) => ({ default: m.FeedbackPage })),
);
const UserSettingsPage = lazy(() =>
  import("@/pages/settings/UserSettingsPage").then((m) => ({ default: m.UserSettingsPage })),
);


import { ErrorBoundary } from "@/components/ErrorBoundary";
import { AdminPages } from "./AdminPages";
import { AdminOnlyNotice, PageNotFound } from "./RouteFallbacks";
import { canonicalRoute, isAdminRoute, isInSection, isKnownRoute } from "@/lib/portalRoutes";
import { LoadingIndicator } from "@/components/LoadingIndicator";

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

/** Placeholder held in the page area while a route's chunk loads. */
function PageLoading() {
  return (
    <div className="flex h-full items-center justify-center py-16">
      <LoadingIndicator />
    </div>
  );
}

/**
 * Shown when a page fails to render — in practice, when its chunk does not
 * arrive. Reloading is the fix when the cause is a deploy that moved the
 * chunk names out from under an open tab, so that is what it offers.
 */
function PageFailed() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 py-16 text-center">
      <p className="text-sm font-medium">This page could not be loaded.</p>
      <p className="max-w-md text-sm text-muted-foreground">
        If the platform was updated while this tab was open, reloading will pick up the new version.
        Everything else in the portal still works.
      </p>
      <button
        type="button"
        onClick={() => window.location.reload()}
        className="rounded-md border px-3 py-1.5 text-sm font-medium transition-colors hover:bg-accent"
      >
        Reload
      </button>
    </div>
  );
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
          {/* Two boundaries around the page area, both scoped to it so the
              sidebar and header stay painted and navigation still works. The
              Suspense one holds the space while the route's chunk arrives; the
              error one catches the chunk that never does, which would
              otherwise unmount the whole portal. It is reset by the route, so
              navigating away from a broken page clears it. */}
          <ErrorBoundary resetKey={route} fallback={<PageFailed />}>
          <Suspense fallback={<PageLoading />}>
          {notFound && <PageNotFound route={route} onNavigate={navigate} />}

          {/* Portal routes — everyone */}
          {!adminRoute && isInSection(route, "/activity") && (
            <ActivityRoutes route={route} onNavigate={navigate} />
          )}
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
          {!adminRoute && isInSection(route, "/scripts") && (
            <PortalScriptRoutes route={route} onNavigate={navigate} />
          )}
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
            <AdminPages
              route={route}
              currentPath={currentPath}
              initialTab={initialTab}
              adminAssetId={adminAssetMatch?.[1]}
              onNavigate={navigate}
            />
          )}
          </Suspense>
          </ErrorBoundary>
        </main>
      </div>
    </div>
  );
}
