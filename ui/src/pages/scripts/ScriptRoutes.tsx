import { MyScriptsPage } from "./MyScriptsPage";
import { ScriptDetailPage } from "./ScriptDetailPage";

// PortalScriptRoutes is the portal's script section: the listing and one
// script. It owns its own route matching so the shell composes the section
// rather than each of its pages, which is what keeps the shell's route switch
// from growing a line per page a section gains.

export function PortalScriptRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/scripts") return <MyScriptsPage onNavigate={onNavigate} />;

  const detail = route.match(/^\/scripts\/(.+)$/);
  if (!detail) return null;
  return (
    <ScriptDetailPage
      scriptId={detail[1]!}
      onNavigate={onNavigate}
      onBack={() => onNavigate("/scripts")}
    />
  );
}
