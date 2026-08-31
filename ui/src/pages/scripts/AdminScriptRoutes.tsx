import { AdminScriptsPage } from "./AdminScriptsPage";
import { ScriptDetailPage } from "./ScriptDetailPage";

// AdminScriptRoutes is the administrator's script section: every script, what
// has been running, one script in full, and one run of one script.
//
// The detail is the SAME page an owner opens. An administrator runs, edits,
// dry-runs, schedules and reads the history of every script exactly as its
// owner does. A second detail page would have meant the two surfaces drifting
// apart, one feature at a time.

// RUN_ROUTE matches one run of one script, which is the address the
// cross-script Runs listing links to (#1407). A run has no page of its own —
// it is read in the history on its script's page — so this address opens that
// page with the run it named already open.
const RUN_ROUTE = /^\/admin\/scripts\/([^/]+)\/runs\/([^/]+)$/;

export function AdminScriptRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/admin/scripts") return <AdminScriptsPage onNavigate={onNavigate} />;

  const run = route.match(RUN_ROUTE);
  if (run) {
    return (
      <ScriptDetailPage
        scriptId={run[1]!}
        openRunId={run[2]!}
        backLabel="All scripts"
        onBack={() => onNavigate("/admin/scripts")}
        onNavigate={onNavigate}
        filePath={adminFilePath}
      />
    );
  }

  const detail = route.match(/^\/admin\/scripts\/([^/]+)$/);
  if (!detail) return null;
  return (
    <ScriptDetailPage
      scriptId={detail[1]!}
      backLabel="All scripts"
      onBack={() => onNavigate("/admin/scripts")}
      onNavigate={onNavigate}
      filePath={adminFilePath}
    />
  );
}

// adminFilePath is where a file this script produced opens for an operator: the
// console's own libraries, which hold every asset and every resource rather
// than the ones scoped to them.
function adminFilePath(kind: "asset" | "resource", id: string): string {
  const section = kind === "asset" ? "assets" : "resources";
  return `/admin/${section}/${encodeURIComponent(id)}`;
}
