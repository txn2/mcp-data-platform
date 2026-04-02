import { useState, useEffect, useCallback, useRef } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { useAuthStore } from "@/stores/auth";

// Portal pages (everyone)
import { ActivityPage } from "@/pages/activity/ActivityPage";
import { MyAssetsPage } from "@/pages/assets/MyAssetsPage";
import { SharedWithMePage } from "@/pages/shared/SharedWithMePage";
import { MyKnowledgePage } from "@/pages/knowledge/MyKnowledgePage";
import { AssetViewerPage } from "@/pages/viewer/AssetViewerPage";
import { CollectionsPage } from "@/pages/collections/CollectionsPage";
import { CollectionViewerPage } from "@/pages/collections/CollectionViewerPage";
import { CollectionEditorPage } from "@/pages/collections/CollectionEditorPage";

// Admin pages (admin only)
import { HomePage } from "@/pages/home/HomePage";
import { AdminAssetsPage } from "@/pages/assets/AdminAssetsPage";
import { AdminAssetViewerPage } from "@/pages/viewer/AdminAssetViewerPage";
import { ToolsPage } from "@/pages/tools/ToolsPage";
import { AuditLogPage } from "@/pages/audit/AuditLogPage";
import { KnowledgePage } from "@/pages/knowledge/KnowledgePage";
import { PersonasPage } from "@/pages/personas/PersonasPage";
import { SettingsPage } from "@/pages/settings/SettingsPage";
import { ShieldAlert } from "lucide-react";

const pageTitles: Record<string, string> = {
  "/activity": "Activity",
  "/": "My Assets",
  "/collections": "Collections",
  "/shared": "Shared With Me",
  "/my-knowledge": "My Knowledge",
  "/admin": "Dashboard",
  "/admin/assets": "Assets",
  "/admin/tools": "Tools",
  "/admin/audit": "Audit Log",
  "/admin/knowledge": "Knowledge",
  "/admin/personas": "Personas",
  "/admin/settings": "Settings",
};

const SIDEBAR_STORAGE_KEY = "sidebar-collapsed";

/** Vite base path — must match vite.config.ts `base`. */
const BASE = import.meta.env.BASE_URL.replace(/\/+$/, "");

/** Read the in-app path from the current URL. */
function readPath(): string {
  const { pathname, hash } = window.location;
  let route = pathname.startsWith(BASE)
    ? pathname.slice(BASE.length) || "/"
    : pathname;
  if (hash) route += hash;
  return route;
}

/** Routes that auto-collapse the sidebar (asset detail views). */
function isAssetRoute(path: string): boolean {
  const route = path.split("#")[0] ?? "";
  return (
    /^\/assets\/.+$/.test(route) ||
    /^\/admin\/assets\/.+$/.test(route) ||
    /^\/collections\/.+\/assets\/.+$/.test(route) ||
    /^\/shared\/assets\/.+$/.test(route)
  );
}

function AccessDenied() {
  return (
    <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
      <ShieldAlert className="h-12 w-12 mb-2 opacity-30" />
      <p className="text-sm font-medium">Access Denied</p>
      <p className="text-xs mt-1">You need admin privileges to view this section.</p>
    </div>
  );
}

/** Redirect bare /admin/settings to /admin/settings#description so the sidebar sub-item highlights. */
function SettingsRedirect({
  initialTab,
  navigate,
  children,
}: {
  initialTab?: string;
  navigate: (path: string) => void;
  children: React.ReactNode;
}) {
  useEffect(() => {
    if (!initialTab) {
      navigate("/admin/settings#description");
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps -- one-time redirect
  return <>{children}</>;
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

  const navigate = useCallback((path: string) => {
    setCurrentPath(path);
    const hashIdx = path.indexOf("#");
    const pathname = hashIdx >= 0 ? path.slice(0, hashIdx) : path;
    const hash = hashIdx >= 0 ? path.slice(hashIdx) : "";
    window.history.pushState(null, "", BASE + pathname + hash);
  }, []);

  useEffect(() => {
    const onPop = () => setCurrentPath(readPath());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const hashIdx = currentPath.indexOf("#");
  const route = hashIdx >= 0 ? currentPath.slice(0, hashIdx) : currentPath;
  const initialTab = hashIdx >= 0 ? currentPath.slice(hashIdx + 1) : undefined;

  // Asset viewer routes
  const collectionAssetMatch = route.match(/^\/collections\/([^/]+)\/assets\/(.+)$/);
  const sharedAssetMatch = route.match(/^\/shared\/assets\/(.+)$/);
  const assetMatch = route.match(/^\/assets\/(.+)$/);
  const adminAssetMatch = route.match(/^\/admin\/assets\/(.+)$/);
  // Collection routes: /collections/:id and /collections/:id/edit
  const collectionEditMatch = route.match(/^\/collections\/([^/]+)\/edit$/);
  const collectionViewMatch = !collectionEditMatch && !collectionAssetMatch
    ? route.match(/^\/collections\/([^/]+)$/)
    : null;

  const title = collectionAssetMatch || sharedAssetMatch || assetMatch
    ? "Asset Viewer"
    : adminAssetMatch
      ? "Asset Viewer"
      : collectionEditMatch
        ? "Edit Collection"
        : collectionViewMatch
          ? "Collection"
          : (pageTitles[route] ?? "My Assets");

  // Admin routes start with /admin
  const isAdminRoute = route.startsWith("/admin");

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
        <main className="flex-1 overflow-auto bg-muted/40 p-3 sm:p-6">
          {/* Portal routes — everyone */}
          {!isAdminRoute && route === "/activity" && <ActivityPage />}
          {!isAdminRoute && route === "/" && (
            <MyAssetsPage onNavigate={navigate} />
          )}
          {!isAdminRoute && route === "/collections" && (
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
          {!isAdminRoute && route === "/shared" && (
            <SharedWithMePage onNavigate={navigate} />
          )}
          {!isAdminRoute && route === "/my-knowledge" && <MyKnowledgePage />}
          {collectionAssetMatch && (
            <AssetViewerPage assetId={collectionAssetMatch[2]!} onNavigate={navigate} onBack={() => navigate(`/collections/${collectionAssetMatch[1]!}`)} />
          )}
          {sharedAssetMatch && (
            <AssetViewerPage assetId={sharedAssetMatch[1]!} onNavigate={navigate} onBack={() => navigate("/shared")} />
          )}
          {assetMatch && (
            <AssetViewerPage assetId={assetMatch[1]!} onNavigate={navigate} onBack={() => navigate("/")} />
          )}

          {/* Admin routes — admin only (defense in depth) */}
          {isAdminRoute && !isAdmin && <AccessDenied />}
          {isAdminRoute && isAdmin && (
            <>
              {route === "/admin" && (
                <HomePage
                  key={currentPath}
                  initialTab={initialTab}
                  onNavigate={navigate}
                />
              )}
              {route === "/admin/assets" && (
                <AdminAssetsPage onNavigate={navigate} />
              )}
              {adminAssetMatch && (
                <AdminAssetViewerPage
                  assetId={adminAssetMatch[1]!}
                  onNavigate={navigate}
                />
              )}
              {route === "/admin/tools" && (
                <ToolsPage key={currentPath} initialTab={initialTab} />
              )}
              {route === "/admin/audit" && (
                <AuditLogPage
                  key={currentPath}
                  initialTab={initialTab}
                  onNavigate={navigate}
                />
              )}
              {route === "/admin/knowledge" && (
                <KnowledgePage key={currentPath} initialTab={initialTab} />
              )}
              {route === "/admin/personas" && (
                <PersonasPage key={currentPath} initialTab={initialTab} />
              )}
              {route === "/admin/settings" && (
                <SettingsRedirect initialTab={initialTab} navigate={navigate}>
                  <SettingsPage key={currentPath} initialTab={initialTab} />
                </SettingsRedirect>
              )}
            </>
          )}
        </main>
      </div>
    </div>
  );
}
