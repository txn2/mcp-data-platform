import type { ProducedTargetKind } from "@/api/portal/hooks/producers";
import { AdminScriptsPage } from "./AdminScriptsPage";
import { EndpointDetailPage } from "./endpoints/EndpointDetailPage";
import { ENDPOINTS_PATH } from "./endpoints/endpointLabels";
import { ScriptDetailPage } from "./ScriptDetailPage";
import { sectionOf } from "./producedPaths";

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
const RUN_ROUTE = /^\/admin\/automations\/([^/]+)\/runs\/([^/]+)$/;

// ENDPOINT_ROUTE is one endpoint's page (#2208).
const ENDPOINT_ROUTE = /^\/admin\/automations\/endpoints\/([^/]+)$/;

export function AdminScriptRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  // Keyed by address, so Back from the Endpoints tab lands on the tab the
  // address names rather than the one last shown.
  if (route === "/admin/automations") return <AdminScriptsPage key="automations" onNavigate={onNavigate} />;
  // The Endpoints tab and one endpoint (#2208), matched before an
  // automation's own address, which "endpoints" would otherwise read as.
  if (route === ENDPOINTS_PATH) return <AdminScriptsPage key="endpoints" onNavigate={onNavigate} tab="endpoints" />;
  const endpoint = route.match(ENDPOINT_ROUTE);
  if (endpoint) return <EndpointDetailPage name={decodeURIComponent(endpoint[1]!)} onNavigate={onNavigate} />;

  const run = route.match(RUN_ROUTE);
  if (run) {
    return (
      <ScriptDetailPage
        scriptId={run[1]!}
        openRunId={run[2]!}
        backLabel="All automations"
        onBack={() => onNavigate("/admin/automations")}
        onNavigate={onNavigate}
        filePath={adminFilePath}
      basePath="/admin/automations"
      />
    );
  }

  const detail = route.match(/^\/admin\/automations\/([^/]+)$/);
  if (!detail) return null;
  return (
    <ScriptDetailPage
      scriptId={detail[1]!}
      backLabel="All automations"
      onBack={() => onNavigate("/admin/automations")}
      onNavigate={onNavigate}
      filePath={adminFilePath}
      basePath="/admin/automations"
    />
  );
}

// adminFilePath is where a file this script produced opens for an operator: the
// console's own libraries, which hold every asset and every resource rather
// than the ones scoped to them.
function adminFilePath(kind: ProducedTargetKind, id: string): string {
  return `/admin/${sectionOf(kind)}/${encodeURIComponent(id)}`;
}
