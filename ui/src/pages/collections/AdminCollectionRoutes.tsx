import { AdminCollectionsPage } from "./AdminCollectionsPage";
import { AdminCollectionViewerPage } from "./AdminCollectionViewerPage";

// AdminCollectionRoutes is the admin's collection section: every collection on
// the platform, and one of them. It owns its own route matching so the shell
// composes the section rather than each of its pages, the same shape the portal
// script section takes.

export function AdminCollectionRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  if (route === "/admin/collections") return <AdminCollectionsPage onNavigate={onNavigate} />;

  const detail = route.match(/^\/admin\/collections\/(.+)$/);
  if (!detail) return null;
  return <AdminCollectionViewerPage collectionId={detail[1]!} onNavigate={onNavigate} />;
}
