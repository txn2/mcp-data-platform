import { WebhookDetailPage } from "./WebhookDetailPage";
import { WebhookEditor } from "./WebhookEditor";
import { WebhooksPage } from "./WebhooksPage";

// AdminWebhookRoutes is the Webhooks section (#1870): the list of sources, a
// new source, one source's page, and its editor. It owns its route matching,
// as the other sections do.

const NEW_ROUTE = "/admin/webhooks/new";
const DETAIL_ROUTE = /^\/admin\/webhooks\/([^/]+)$/;
const EDIT_ROUTE = /^\/admin\/webhooks\/([^/]+)\/edit$/;

export function AdminWebhookRoutes({
  route,
  onNavigate,
  onBack,
}: {
  route: string;
  onNavigate: (path: string, opts?: { replace?: boolean }) => void;
  onBack: (fallback: string) => void;
}) {
  const open = (name: string) =>
    onNavigate(`/admin/webhooks/${encodeURIComponent(name)}`, { replace: true });

  if (route === "/admin/webhooks") return <WebhooksPage onNavigate={onNavigate} />;
  if (route === NEW_ROUTE) {
    return <WebhookEditor onBack={() => onBack("/admin/webhooks")} onSaved={open} />;
  }
  const edit = route.match(EDIT_ROUTE);
  if (edit) {
    const name = decodeURIComponent(edit[1]!);
    return (
      <WebhookEditor
        name={name}
        onBack={() => onBack(`/admin/webhooks/${encodeURIComponent(name)}`)}
        onSaved={open}
      />
    );
  }
  const detail = route.match(DETAIL_ROUTE);
  if (!detail) return null;
  return (
    <WebhookDetailPage
      name={decodeURIComponent(detail[1]!)}
      onBack={() => onBack("/admin/webhooks")}
      onNavigate={onNavigate}
    />
  );
}
