import { ApprovalsPage } from "@/pages/approvals/ApprovalsPage";
import { ApplicationDetailPage } from "./ApplicationDetailPage";
import { ApplicationsPage } from "./ApplicationsPage";

// ApplicationRoutes is the portal's Applications section (#2411): the listing
// and one application, and the Approvals queue a form's public use is
// approved through (#2494). It owns its route matching, as the Automations
// section does, so the shell mounts the section rather than each of its pages.
export function ApplicationRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/applications") return <ApplicationsPage onNavigate={onNavigate} />;
  if (route === "/approvals") return <ApprovalsPage onNavigate={onNavigate} />;
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
