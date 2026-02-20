import { useState, useEffect, useCallback } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { HomePage } from "@/pages/home/HomePage";
import { ToolsPage } from "@/pages/tools/ToolsPage";
import { AuditLogPage } from "@/pages/audit/AuditLogPage";
import { KnowledgePage } from "@/pages/knowledge/KnowledgePage";
import { PersonasPage } from "@/pages/personas/PersonasPage";
const pageTitles: Record<string, string> = {
  "/": "Home",
  "/tools": "Tools",
  "/audit": "Audit Log",
  "/knowledge": "Knowledge",
  "/personas": "Personas",
};

/** Vite base path — must match vite.config.ts `base`. */
const BASE = import.meta.env.BASE_URL.replace(/\/+$/, ""); // e.g. "/admin"

/** Read the in-app path from the current URL. */
function readPath(): string {
  const { pathname, hash } = window.location;
  // Strip the base prefix: "/admin/tools" → "/tools"
  let route = pathname.startsWith(BASE)
    ? pathname.slice(BASE.length) || "/"
    : pathname;
  // Append hash fragment for tab deep-links: "/tools" + "#help" → "/tools#help"
  if (hash) route += hash;
  return route;
}

export function AppShell() {
  const [currentPath, setCurrentPath] = useState(readPath);

  /** Navigate: update state + push a browser history entry. */
  const navigate = useCallback((path: string) => {
    setCurrentPath(path);
    // Split internal path "/tools#help" into pathname + hash
    const hashIdx = path.indexOf("#");
    const pathname = hashIdx >= 0 ? path.slice(0, hashIdx) : path;
    const hash = hashIdx >= 0 ? path.slice(hashIdx) : "";
    window.history.pushState(null, "", BASE + pathname + hash);
  }, []);

  /** Sync state when the user presses browser back / forward. */
  useEffect(() => {
    const onPop = () => setCurrentPath(readPath());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  // Support deep linking: "/tools#help" → route="/tools", initialTab="help"
  const hashIdx = currentPath.indexOf("#");
  const route = hashIdx >= 0 ? currentPath.slice(0, hashIdx) : currentPath;
  const initialTab = hashIdx >= 0 ? currentPath.slice(hashIdx + 1) : undefined;

  const title = pageTitles[route] ?? "Home";

  return (
    <div className="flex h-screen">
      <Sidebar currentPath={currentPath} onNavigate={navigate} />
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header title={title} />
        <main className="flex-1 overflow-auto bg-muted/40 p-6">
          {route === "/" && (
            <HomePage
              key={currentPath}
              initialTab={initialTab}
              onNavigate={navigate}
            />
          )}
          {route === "/tools" && (
            <ToolsPage key={currentPath} initialTab={initialTab} />
          )}
          {route === "/audit" && (
            <AuditLogPage key={currentPath} initialTab={initialTab} onNavigate={navigate} />
          )}
          {route === "/knowledge" && (
            <KnowledgePage key={currentPath} initialTab={initialTab} />
          )}
          {route === "/personas" && (
            <PersonasPage key={currentPath} initialTab={initialTab} />
          )}
        </main>
      </div>
    </div>
  );
}
