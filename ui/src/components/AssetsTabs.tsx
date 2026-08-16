import { LayoutGrid, FolderOpen } from "lucide-react";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

export type AssetsTab = "assets" | "collections";

interface Props {
  active: AssetsTab;
  onNavigate: (path: string) => void;
  /** Navigate between the admin-scoped faces (every owner) instead of the reader's own. */
  admin?: boolean;
}

const TABS: { id: AssetsTab; label: string; icon: typeof LayoutGrid; path: string; adminPath: string }[] = [
  { id: "assets", label: "Assets", icon: LayoutGrid, path: "/", adminPath: "/admin/assets" },
  {
    id: "collections",
    label: "Collections",
    icon: FolderOpen,
    path: "/collections",
    adminPath: "/admin/collections",
  },
];

/**
 * Underline tab strip shared by the Assets and Collections pages. Navigates
 * between the two routes so they read as one consolidated area.
 *
 * The two faces are routes, not panels, so choosing one navigates instead of
 * revealing a `TabsContent`; the tablist is still the honest shape for a strip
 * where exactly one of two views of the same area is showing.
 */
export function AssetsTabs({ active, onNavigate, admin = false }: Props) {
  return (
    <Tabs
      value={active}
      // Manual activation, because choosing a face here is a navigation.
      // Radix's default selects on focus, so an arrow key through the strip
      // would leave the page the reader is on before they asked for it.
      activationMode="manual"
      onValueChange={(next) => {
        const tab = TABS.find((t) => t.id === next);
        if (tab) onNavigate(admin ? tab.adminPath : tab.path);
      }}
    >
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-0 border-b p-0"
      >
        {TABS.map((t) => {
          const Icon = t.icon;
          return (
            <TabsTrigger
              key={t.id}
              value={t.id}
              type="button"
              // Radix names a panel these faces do not have: the content each
              // one leads to is a route, not a TabsContent, so the stamped id
              // would resolve to nothing.
              aria-controls={undefined}
              className="flex-none gap-2 px-3 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
            >
              <Icon />
              {t.label}
            </TabsTrigger>
          );
        })}
      </TabsList>
    </Tabs>
  );
}
