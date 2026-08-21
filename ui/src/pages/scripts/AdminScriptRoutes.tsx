import { AdminScriptsPage } from "./AdminScriptsPage";
import { ScriptDetailPage } from "./ScriptDetailPage";

// AdminScriptRoutes is the administrator's script section: every script, what
// has been running, and one script in full.
//
// The detail is the SAME page an owner opens. An administrator runs, edits,
// dry-runs, schedules and reads the history of every script exactly as its
// owner does. A second detail page would have meant the two surfaces drifting
// apart, one feature at a time.

export function AdminScriptRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/admin/scripts") return <AdminScriptsPage onNavigate={onNavigate} />;

  const detail = route.match(/^\/admin\/scripts\/(.+)$/);
  if (!detail) return null;
  return (
    <ScriptDetailPage
      scriptId={detail[1]!}
      backLabel="All scripts"
      onBack={() => onNavigate("/admin/scripts")}
      onNavigate={onNavigate}
    />
  );
}
