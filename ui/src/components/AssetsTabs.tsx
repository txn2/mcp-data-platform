import { LayoutGrid, FolderOpen } from "lucide-react";
import { cn } from "@/lib/utils";

export type AssetsTab = "assets" | "collections";

interface Props {
  active: AssetsTab;
  onNavigate: (path: string) => void;
}

const TABS: { id: AssetsTab; label: string; icon: typeof LayoutGrid; path: string }[] = [
  { id: "assets", label: "Assets", icon: LayoutGrid, path: "/" },
  { id: "collections", label: "Collections", icon: FolderOpen, path: "/collections" },
];

/**
 * Underline tab strip shared by the Assets and Collections pages. Navigates
 * between the two routes so they read as one consolidated area.
 */
export function AssetsTabs({ active, onNavigate }: Props) {
  return (
    <div className="flex gap-1 border-b">
      {TABS.map((t) => {
        const Icon = t.icon;
        const isActive = active === t.id;
        return (
          <button
            key={t.id}
            type="button"
            onClick={() => onNavigate(t.path)}
            className={cn(
              "-mb-px inline-flex items-center gap-2 border-b-2 px-3 py-2 text-sm font-medium transition-colors",
              isActive
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="h-4 w-4" />
            {t.label}
          </button>
        );
      })}
    </div>
  );
}
