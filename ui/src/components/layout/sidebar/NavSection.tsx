import { NavButton } from "./NavButton";
import { isNavActive, type NavItem } from "./navItems";
import type { NavBadge } from "./useNavBadges";

/**
 * NavSection is one captioned run of nav items in the rail. The caption is
 * dropped on a collapsed rail, where there is no room for words and the
 * separator above the group already says a new group has started.
 */
export function NavSection({
  label,
  items,
  currentPath,
  collapsed,
  badges,
  onNavigate,
}: {
  label: string;
  items: NavItem[];
  currentPath: string;
  collapsed: boolean;
  /** Waiting work per nav path; a path with no entry carries no badge. */
  badges?: Record<string, NavBadge>;
  onNavigate: (path: string) => void;
}) {
  return (
    <>
      {!collapsed && (
        <p className="px-3 py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </p>
      )}
      {items.map((item) => (
        <NavButton
          key={item.path}
          icon={item.icon}
          label={item.label}
          collapsed={collapsed}
          active={isNavActive(item.path, currentPath)}
          badge={badges?.[item.path]}
          onClick={() => onNavigate(item.path)}
        />
      ))}
    </>
  );
}
