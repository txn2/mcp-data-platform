import { ApplicationDetailPage } from "./ApplicationDetailPage";
import { ApplicationsPage } from "./ApplicationsPage";

// ApplicationRoutes is the portal's Applications section (#2411): the listing
// and one application. It owns its route matching, as the Automations section
// does, so the shell mounts the section rather than each of its pages.
export function ApplicationRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/applications") return <ApplicationsPage onNavigate={onNavigate} />;
  const detail = route.match(/^\/applications\/([^/]+)$/);
  if (!detail) return null;
  return (
    <ApplicationDetailPage
      app={decodeURIComponent(detail[1]!)}
      onNavigate={onNavigate}
      onBack={() => onNavigate("/applications")}
    />
  );
}
