import { useState } from "react";
import { Sidebar } from "./Sidebar";
import { Header } from "./Header";
import { DashboardPage } from "@/pages/dashboard/DashboardPage";
import { AuditLogPage } from "@/pages/audit/AuditLogPage";

const pageTitles: Record<string, string> = {
  "/": "Dashboard",
  "/audit": "Audit Log",
};

export function AppShell() {
  const [currentPath, setCurrentPath] = useState("/");

  const title = pageTitles[currentPath] ?? "Dashboard";

  return (
    <div className="flex h-screen">
      <Sidebar currentPath={currentPath} onNavigate={setCurrentPath} />
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header title={title} />
        <main className="flex-1 overflow-auto bg-muted/40 p-6">
          {currentPath === "/" && <DashboardPage />}
          {currentPath === "/audit" && <AuditLogPage />}
        </main>
      </div>
    </div>
  );
}
