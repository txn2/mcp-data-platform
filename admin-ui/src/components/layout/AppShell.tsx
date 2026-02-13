import { useState } from "react";
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

export function AppShell() {
  const [currentPath, setCurrentPath] = useState("/");

  // Support deep linking: "/tools#help" → route="/tools", initialTab="help"
  const hashIdx = currentPath.indexOf("#");
  const route = hashIdx >= 0 ? currentPath.slice(0, hashIdx) : currentPath;
  const initialTab = hashIdx >= 0 ? currentPath.slice(hashIdx + 1) : undefined;

  const title = pageTitles[route] ?? "Home";

  return (
    <div className="flex h-screen">
      <Sidebar currentPath={currentPath} onNavigate={setCurrentPath} />
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header title={title} />
        <main className="flex-1 overflow-auto bg-muted/40 p-6">
          {route === "/" && (
            <HomePage
              key={currentPath}
              initialTab={initialTab}
              onNavigate={setCurrentPath}
            />
          )}
          {route === "/tools" && (
            <ToolsPage key={currentPath} initialTab={initialTab} />
          )}
          {route === "/audit" && (
            <AuditLogPage key={currentPath} initialTab={initialTab} />
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
