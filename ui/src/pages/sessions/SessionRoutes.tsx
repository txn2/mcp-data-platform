import { SessionsPage } from "./SessionsPage";
import { SessionDetailPage } from "./SessionDetailPage";

// SessionRoutes is the admin's session section: the sessions the audit log has
// recorded, and one of them. It owns its own route matching so the shell
// composes the section rather than each of its pages, the same shape the admin
// collection and portal script sections take.

export function SessionRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/admin/sessions") return <SessionsPage onNavigate={onNavigate} />;

  // The detail is addressable so an operator can hand a session to a
  // colleague as a link (#1318).
  const detail = route.match(/^\/admin\/sessions\/(.+)$/);
  if (!detail) return null;
  return (
    <SessionDetailPage
      sessionId={decodeURIComponent(detail[1]!)}
      onNavigate={onNavigate}
      onBack={() => onNavigate("/admin/sessions")}
    />
  );
}
