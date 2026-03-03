import { useState, useEffect, useCallback } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { MyAssetsPage } from "@/pages/assets/MyAssetsPage";
import { SharedWithMePage } from "@/pages/shared/SharedWithMePage";
import { AssetViewerPage } from "@/pages/viewer/AssetViewerPage";

const pageTitles: Record<string, string> = {
  "/": "My Assets",
  "/shared": "Shared With Me",
};

const BASE = import.meta.env.BASE_URL.replace(/\/+$/, "");

function readPath(): string {
  const { pathname, hash } = window.location;
  let route = pathname.startsWith(BASE)
    ? pathname.slice(BASE.length) || "/"
    : pathname;
  if (hash) route += hash;
  return route;
}

export function AppShell() {
  const [currentPath, setCurrentPath] = useState(readPath);

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

  // Asset viewer route: /assets/:id
  const assetMatch = route.match(/^\/assets\/(.+)$/);

  const title = assetMatch ? "Asset Viewer" : (pageTitles[route] ?? "My Assets");

  return (
    <div className="flex h-screen">
      <Sidebar currentPath={currentPath} onNavigate={navigate} />
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header title={title} />
        <main className="flex-1 overflow-auto bg-muted/40 p-6">
          {assetMatch ? (
            <AssetViewerPage assetId={assetMatch[1]!} onNavigate={navigate} />
          ) : route === "/shared" ? (
            <SharedWithMePage onNavigate={navigate} />
          ) : (
            <MyAssetsPage onNavigate={navigate} />
          )}
        </main>
      </div>
    </div>
  );
}
