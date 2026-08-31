import { MyScriptsPage } from "./MyScriptsPage";
import { ScriptDetailPage } from "./ScriptDetailPage";

// PortalScriptRoutes is the portal's script section: the listing, one script,
// and one run of one script. It owns its own route matching so the shell
// composes the section rather than each of its pages, which is what keeps the
// shell's route switch from growing a line per page a section gains.

// RUN_ROUTE matches one run of one script, which is the address the
// cross-script Runs listing links to (#1405). A run has no page of its own —
// it is read in the history on its script's page — so this address opens that
// page with the run it named already open.
const RUN_ROUTE = /^\/scripts\/([^/]+)\/runs\/([^/]+)$/;

export function PortalScriptRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/scripts") return <MyScriptsPage onNavigate={onNavigate} />;

  const run = route.match(RUN_ROUTE);
  if (run) {
    return (
      <ScriptDetailPage
        scriptId={run[1]!}
        openRunId={run[2]!}
        onNavigate={onNavigate}
        onBack={() => onNavigate("/scripts")}
        filePath={portalFilePath}
      />
    );
  }

  const detail = route.match(/^\/scripts\/([^/]+)$/);
  if (!detail) return null;
  return (
    <ScriptDetailPage
      scriptId={detail[1]!}
      onNavigate={onNavigate}
      onBack={() => onNavigate("/scripts")}
      filePath={portalFilePath}
    />
  );
}

// portalFilePath is where a file this script produced opens for its owner: the
// reader's own asset and resource libraries, which is where they can open it.
function portalFilePath(kind: "asset" | "resource", id: string): string {
  const section = kind === "asset" ? "assets" : "resources";
  return `/${section}/${encodeURIComponent(id)}`;
}
