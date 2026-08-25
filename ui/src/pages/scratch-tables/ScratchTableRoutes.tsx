import { ScratchTablesPage } from "./ScratchTablesPage";
import { ScratchTableDetailPage } from "./ScratchTableDetailPage";

// ScratchTableRoutes is the portal's scratch-table section: the listing, and
// one registration at an address of its own (#1472). It owns its own route
// matching so the shell composes the section rather than each of its pages,
// which is what keeps the shell's route switch from growing a line per page a
// section gains.

const DETAIL_ROUTE = /^\/scratch-tables\/([^/]+)$/;

export function ScratchTableRoutes({
  route,
  onNavigate,
  onBack,
}: {
  route: string;
  onNavigate: (path: string) => void;
  /** onBack returns to wherever the reader came from, falling back to the listing. */
  onBack: (fallback: string) => void;
}) {
  if (route === "/scratch-tables") return <ScratchTablesPage onNavigate={onNavigate} />;

  const detail = route.match(DETAIL_ROUTE);
  if (!detail) return null;
  return (
    <ScratchTableDetailPage
      registrationId={decodeURIComponent(detail[1]!)}
      onNavigate={onNavigate}
      onBack={() => onBack("/scratch-tables")}
    />
  );
}
