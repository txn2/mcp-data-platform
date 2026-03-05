import { useState, useEffect, useCallback } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { useAuthStore } from "@/stores/auth";

// Portal pages (everyone)
import { MyAssetsPage } from "@/pages/assets/MyAssetsPage";
import { SharedWithMePage } from "@/pages/shared/SharedWithMePage";
import { AssetViewerPage } from "@/pages/viewer/AssetViewerPage";

// Admin pages (admin only)
import { HomePage } from "@/pages/home/HomePage";
import { ToolsPage } from "@/pages/tools/ToolsPage";
import { AuditLogPage } from "@/pages/audit/AuditLogPage";
import { KnowledgePage } from "@/pages/knowledge/KnowledgePage";
import { PersonasPage } from "@/pages/personas/PersonasPage";
import { ShieldAlert } from "lucide-react";

const pageTitles: Record<string, string> = {
  "/": "My Assets",
  "/shared": "Shared With Me",
  "/admin": "Dashboard",
  "/admin/tools": "Tools",
  "/admin/audit": "Audit Log",
  "/admin/knowledge": "Knowledge",
  "/admin/personas": "Personas",
};

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

function AccessDenied() {
  return (
    <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
      <ShieldAlert className="h-12 w-12 mb-2 opacity-30" />
      <p className="text-sm font-medium">Access Denied</p>
      <p className="text-xs mt-1">You need admin privileges to view this section.</p>
    </div>
  );
}

export function AppShell() {
  const [currentPath, setCurrentPath] = useState(readPath);
  const isAdmin = useAuthStore((s) => s.isAdmin());

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

  // Asset viewer route: /assets/:id
  const assetMatch = route.match(/^\/assets\/(.+)$/);

  const title = assetMatch
    ? "Asset Viewer"
    : (pageTitles[route] ?? "My Assets");

  // Admin routes start with /admin
  const isAdminRoute = route.startsWith("/admin");

  return (
    <div className="flex h-screen">
      <Sidebar currentPath={currentPath} onNavigate={navigate} />
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header title={title} />
        <main className="flex-1 overflow-auto bg-muted/40 p-6">
          {/* Portal routes — everyone */}
          {!isAdminRoute && !assetMatch && route !== "/shared" && (
            <MyAssetsPage onNavigate={navigate} />
          )}
          {!isAdminRoute && route === "/shared" && (
            <SharedWithMePage onNavigate={navigate} />
          )}
          {assetMatch && (
            <AssetViewerPage assetId={assetMatch[1]!} onNavigate={navigate} />
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
            </>
          )}
        </main>
      </div>
    </div>
  );
}
