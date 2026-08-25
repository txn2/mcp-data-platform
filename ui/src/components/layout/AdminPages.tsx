import { lazy, type ReactNode } from "react";

// The pages only an administrator reaches. Split out of AppShell so the shell
// stays a shell: it resolves the path and paints the chrome, and each section
// owns the switch over its own routes.
//
// Every page is its own chunk, so this switch is also what decides which
// chunks an administrator's visit downloads — see the note on the shell's
// imports (#1351).
import { isInSection } from "@/lib/portalRoutes";

// Admin pages (admin only)
// The resources page serves both sections; `admin` is what changes its scope.
const ResourcesPage = lazy(() =>
  import("@/pages/resources/ResourcesPage").then((m) => ({ default: m.ResourcesPage })),
);
const ResourceViewerPage = lazy(() =>
  import("@/pages/resources/ResourceViewerPage").then((m) => ({ default: m.ResourceViewerPage })),
);
const AdminAssetsPage = lazy(() =>
  import("@/pages/assets/AdminAssetsPage").then((m) => ({ default: m.AdminAssetsPage })),
);
const AdminAssetViewerPage = lazy(() =>
  import("@/pages/viewer/AdminAssetViewerPage").then((m) => ({ default: m.AdminAssetViewerPage })),
);
const AdminCollectionRoutes = lazy(() =>
  import("@/pages/collections/AdminCollectionRoutes").then((m) => ({ default: m.AdminCollectionRoutes })),
);
const ToolsPage = lazy(() =>
  import("@/pages/tools/ToolsPage").then((m) => ({ default: m.ToolsPage })),
);
const AuditLogPage = lazy(() =>
  import("@/pages/audit/AuditLogPage").then((m) => ({ default: m.AuditLogPage })),
);
const CallRoutes = lazy(() =>
  import("@/pages/calls/CallRoutes").then((m) => ({ default: m.CallRoutes })),
);
const SessionRoutes = lazy(() =>
  import("@/pages/sessions/SessionRoutes").then((m) => ({ default: m.SessionRoutes })),
);
const ConfigEditorPage = lazy(() =>
  import("@/pages/settings/ConfigEditorPage").then((m) => ({ default: m.ConfigEditorPage })),
);
const CatalogsPanel = lazy(() =>
  import("@/pages/settings/CatalogsPanel").then((m) => ({ default: m.CatalogsPanel })),
);
// The operation browser serves both sections; `scope` is what changes its
// source, from the connections a caller reaches to the catalogs that exist.
const ApisPage = lazy(() =>
  import("@/pages/apis/ApisPage").then((m) => ({ default: m.ApisPage })),
);
const ConnectionsPanel = lazy(() =>
  import("@/pages/settings/ConnectionsPanel").then((m) => ({ default: m.ConnectionsPanel })),
);
const PersonasPanel = lazy(() =>
  import("@/pages/settings/PersonasPanel").then((m) => ({ default: m.PersonasPanel })),
);
const AdminPromptsPage = lazy(() =>
  import("@/pages/prompts/AdminPromptsPage").then((m) => ({ default: m.AdminPromptsPage })),
);
const AdminScriptRoutes = lazy(() =>
  import("@/pages/scripts/AdminScriptRoutes").then((m) => ({ default: m.AdminScriptRoutes })),
);
const KeysPage = lazy(() =>
  import("@/pages/settings/KeysPage").then((m) => ({ default: m.KeysPage })),
);
const UsersPanel = lazy(() =>
  import("@/pages/settings/UsersPanel").then((m) => ({ default: m.UsersPanel })),
);
const ChangelogPage = lazy(() =>
  import("@/pages/settings/ChangelogPage").then((m) => ({ default: m.ChangelogPage })),
);
const AdminSettingsPage = lazy(() =>
  import("@/pages/settings/AdminSettingsPage").then((m) => ({ default: m.AdminSettingsPage })),
);

export interface AdminPagesProps {
  route: string;
  /** The full in-app path, used to remount a tabbed page on navigation. */
  currentPath: string;
  /** Tab named in the path's hash, for the pages that have tabs. */
  initialTab?: string;
  /** Asset id when the path addresses one, else undefined. */
  adminAssetId?: string;
  onNavigate: (path: string, opts?: { replace?: boolean }) => void;
  /** The shell's way back from a detail page: the entry the reader came from,
   * or the named section when this page was opened cold. */
  onBack: (fallback: string) => void;
}

/**
 * The pages an administrator route resolves to exactly, keyed by path.
 *
 * A table rather than a run of conditions: the sections below already carry
 * the two shapes a condition is needed for — a detail route with an id in it,
 * and a section that owns a whole subtree — and everything else is a single
 * path naming a single page. A Map rather than an object because the key comes
 * off the address bar, and an object would answer "/constructor" out of its
 * prototype.
 */
const EXACT_PAGES: ReadonlyMap<string, (p: PageContext) => ReactNode> = new Map([
  // The dashboard hosts the merged MCP / API Gateway / Events activity views
  // (it was a separate Audit Log page), and answers at both names.
  ["/admin", auditPage],
  ["/admin/audit", auditPage],
  ["/admin/assets", (p: PageContext) => <AdminAssetsPage onNavigate={p.navigate} />],
  ["/admin/tools", (p: PageContext) => <ToolsPage key={p.currentPath} initialTab={p.initialTab} />],
  ["/admin/description", () => (
    <ConfigEditorPage
      configKey="server.description"
      label="Description"
      description="Platform identity visible to MCP clients"
    />
  )],
  ["/admin/agent-instructions", () => (
    <ConfigEditorPage
      configKey="server.agent_instructions"
      label="Agent Instructions"
      description="Guidance for AI agents using this platform"
      showPlatformBaseline
    />
  )],
  ["/admin/api-catalogs", () => <CatalogsPanel />],
  ["/admin/apis", () => <ApisPage scope="admin" />],
  ["/admin/connections", () => <ConnectionsPanel />],
  ["/admin/personas", () => <PersonasPanel />],
  ["/admin/prompts", (p: PageContext) => <AdminPromptsPage onNavigate={p.navigate} />],
  ["/admin/resources", (p: PageContext) => <ResourcesPage admin onNavigate={p.navigate} />],
  ["/admin/keys", () => <KeysPage />],
  ["/admin/users", () => <UsersPanel />],
  ["/admin/changelog", () => <ChangelogPage />],
  ["/admin/settings", () => <AdminSettingsPage />],
]);

/** What an entry in EXACT_PAGES may read. */
interface PageContext {
  currentPath: string;
  initialTab?: string;
  navigate: (path: string, opts?: { replace?: boolean }) => void;
}

function auditPage(p: PageContext) {
  return <AuditLogPage key={p.currentPath} initialTab={p.initialTab} onNavigate={p.navigate} />;
}

export function AdminPages({
  route,
  currentPath,
  initialTab,
  adminAssetId,
  onNavigate: navigate,
  onBack,
}: AdminPagesProps) {
  const ctx: PageContext = { currentPath, initialTab, navigate };
  // One resource, in the section that carries authority over every uploader's.
  const resourceViewMatch = route.match(/^\/admin\/resources\/([^/]+)$/);
  return (
    <>
      {EXACT_PAGES.get(route)?.(ctx)}
      {adminAssetId && <AdminAssetViewerPage assetId={adminAssetId} onNavigate={navigate} />}
      {resourceViewMatch && (
        <ResourceViewerPage
          resourceId={resourceViewMatch[1]!}
          admin
          onBack={() => onBack("/admin/resources")}
        />
      )}
      {/* The three sections that own a subtree and match inside it. Guarded
          here so mounting one does not fetch the other two (#1351). */}
      {isInSection(route, "/admin/collections") && (
        <AdminCollectionRoutes route={route} onNavigate={navigate} />
      )}
      {isInSection(route, "/admin/sessions") && (
        <SessionRoutes route={route} onNavigate={navigate} />
      )}
      {isInSection(route, "/admin/calls") && (
        <CallRoutes route={route} onNavigate={navigate} />
      )}
      {isInSection(route, "/admin/scripts") && (
        <AdminScriptRoutes route={route} onNavigate={navigate} />
      )}
    </>
  );
}
