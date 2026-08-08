import { cn } from "@/lib/utils";
import { LogOut, ChevronsLeft, ChevronsRight } from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { NavButton } from "./sidebar/NavButton";
import { NavSection } from "./sidebar/NavSection";
import { adminNavItems, portalNavItems } from "./sidebar/navItems";
import { useNavBadges } from "./sidebar/useNavBadges";
import { useSidebarBrand } from "./sidebar/useSidebarBrand";

interface Props {
  currentPath: string;
  onNavigate: (path: string) => void;
  collapsed: boolean;
  onToggleCollapse: () => void;
  mobile?: boolean;
  onClose?: () => void;
}

/**
 * Sidebar is the portal's navigation rail: the deployment's mark, the sections
 * the reader can reach, and the two rows that act on the rail itself.
 *
 * It renders in three arrangements — expanded, collapsed to icons, and the
 * mobile overlay, which is always expanded because a rail that has to be
 * opened has no reason to hide its words.
 */
export function Sidebar({ currentPath, onNavigate, collapsed, onToggleCollapse, mobile, onClose }: Props) {
  const logout = useAuthStore((s) => s.logout);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const badges = useNavBadges(isAdmin);
  const { portalLogo, portalTitle } = useSidebarBrand();

  // On mobile, close the sidebar after navigating.
  const handleNavigate = (path: string) => {
    onNavigate(path);
    onClose?.();
  };

  // On mobile, sidebar always renders expanded (never collapsed).
  const effectiveCollapsed = mobile ? false : collapsed;

  return (
    <aside
      className={cn(
        "flex h-screen flex-col border-r bg-card overflow-hidden",
        mobile
          ? "w-72"
          : cn("transition-[width] duration-200", effectiveCollapsed ? "w-[var(--sidebar-width-collapsed)]" : "w-[var(--sidebar-width)]"),
      )}
    >
      <div className={cn("flex h-14 items-center border-b shrink-0", effectiveCollapsed ? "justify-center px-2" : "gap-2 px-4")}>
        <img
          src={portalLogo}
          alt=""
          className="h-7 w-7 shrink-0"
          onError={(e) => {
            (e.target as HTMLImageElement).style.display = "none";
          }}
        />
        {!effectiveCollapsed && (
          <span className="text-sm font-semibold truncate">{portalTitle}</span>
        )}
      </div>

      <nav className="flex-1 space-y-1 overflow-auto p-2">
        <NavSection
          label="User"
          items={portalNavItems}
          currentPath={currentPath}
          collapsed={effectiveCollapsed}
          badges={badges}
          onNavigate={handleNavigate}
        />

        {isAdmin && (
          <>
            <div className="my-2 border-t" />
            <NavSection
              label="Admin"
              items={adminNavItems}
              currentPath={currentPath}
              collapsed={effectiveCollapsed}
              onNavigate={handleNavigate}
            />
          </>
        )}
      </nav>

      <div className="border-t p-2 space-y-1">
        {!mobile && (
          <NavButton
            icon={effectiveCollapsed ? ChevronsRight : ChevronsLeft}
            label={effectiveCollapsed ? "Expand sidebar" : "Collapse"}
            collapsed={effectiveCollapsed}
            onClick={onToggleCollapse}
          />
        )}
        <NavButton
          icon={LogOut}
          label="Sign Out"
          collapsed={effectiveCollapsed}
          onClick={logout}
        />
      </div>
    </aside>
  );
}
