import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAuthStore } from "@/stores/auth";
import { ReconcilePage } from "./ReconcilePage";
import { ScratchTablesPage } from "./ScratchTablesPage";
import { ScratchTableDetailPage } from "./ScratchTableDetailPage";

// ScratchTableRoutes is the portal's scratch-table section: the listing, and
// one registration at an address of its own (#1472). It owns its own route
// matching so the shell composes the section rather than each of its pages,
// which is what keeps the shell's route switch from growing a line per page a
// section gains.

const LIST_ROUTE = "/scratch-tables";
const RECONCILE_ROUTE = "/scratch-tables/reconcile";
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
  if (route === LIST_ROUTE || route === RECONCILE_ROUTE) {
    return <ListingTabs route={route} onNavigate={onNavigate} />;
  }

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

/** ListingTabs is the listing, and for an administrator the reconcile of a
 * scratch schema against its registrations beside it (#2136). Each tab is a
 * route, so only the active one is on the page. */
function ListingTabs({ route, onNavigate }: { route: string; onNavigate: (path: string) => void }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  if (!isAdmin) {
    return route === LIST_ROUTE ? <ScratchTablesPage onNavigate={onNavigate} /> : null;
  }
  return (
    <Tabs
      value={route === RECONCILE_ROUTE ? "reconcile" : "tables"}
      onValueChange={(v) => onNavigate(v === "reconcile" ? RECONCILE_ROUTE : LIST_ROUTE)}
      className="gap-6"
    >
      <TabsList variant="line" className="w-full justify-start border-b">
        <TabsTrigger value="tables" className="flex-none px-4 py-2">
          Tables
        </TabsTrigger>
        <TabsTrigger value="reconcile" className="flex-none px-4 py-2">
          Reconcile
        </TabsTrigger>
      </TabsList>
      <TabsContent value="tables">
        {route === LIST_ROUTE && <ScratchTablesPage onNavigate={onNavigate} />}
      </TabsContent>
      <TabsContent value="reconcile">{route === RECONCILE_ROUTE && <ReconcilePage />}</TabsContent>
    </Tabs>
  );
}
