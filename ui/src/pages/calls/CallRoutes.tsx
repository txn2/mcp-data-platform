import { CallsPage } from "./CallsPage";
import { CallDetailPage } from "./CallDetailPage";

// CallRoutes is the admin's call-catalog section: the recorded calls, and one
// of them. It owns its own route matching so the shell composes the section
// rather than each of its pages, the same shape the admin session section
// takes.

export function CallRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/admin/calls") return <CallsPage onNavigate={onNavigate} />;

  // The detail is addressable so an operator can hand a record to a colleague
  // as a link, the same way a session is.
  const detail = route.match(/^\/admin\/calls\/(.+)$/);
  if (!detail) return null;
  return (
    <CallDetailPage
      callId={decodeURIComponent(detail[1]!)}
      onNavigate={onNavigate}
      onBack={() => onNavigate("/admin/calls")}
    />
  );
}
