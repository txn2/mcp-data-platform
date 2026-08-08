import type { LucideIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { NavBadge } from "./useNavBadges";

/**
 * NavCount states the work waiting in a section. A collapsed rail has no room
 * for the figure, so it shows a dot carrying the badge's label instead — the
 * cue survives the collapse even though the number does not.
 */
function NavCount({ badge, collapsed }: { badge: NavBadge; collapsed: boolean }) {
  if (badge.count <= 0) return null;
  return collapsed ? (
    <span
      className="absolute right-1.5 top-1.5 size-2 rounded-full bg-primary"
      aria-label={badge.label}
    />
  ) : (
    <Badge className="bg-primary/15 px-1.5 text-primary">{badge.count}</Badge>
  );
}

/**
 * NavButton is the one face a sidebar row wears — a section link, the collapse
 * toggle, Sign Out — so a row cannot look like a nav item in one part of the
 * rail and like something else in another.
 *
 * A collapsed rail shows the icon alone, which is why `label` also becomes the
 * hover copy there: the words are the only thing that says where the row goes.
 */
export function NavButton({
  icon: Icon,
  label,
  collapsed,
  active = false,
  badge,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  collapsed: boolean;
  /** Set on the section the reader is currently in. */
  active?: boolean;
  /** Work waiting on the reader in this section; a zero count shows nothing. */
  badge?: NavBadge;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      onClick={onClick}
      title={collapsed ? label : undefined}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative h-auto w-full justify-start gap-3 px-3 py-2 font-medium",
        collapsed && "justify-center gap-0 px-2",
        active
          ? "bg-primary/10 text-primary hover:bg-primary/10 hover:text-primary"
          : "text-muted-foreground",
      )}
    >
      <Icon className="size-4 shrink-0" />
      {!collapsed && <span className="flex-1 text-left">{label}</span>}
      {badge && <NavCount badge={badge} collapsed={collapsed} />}
    </Button>
  );
}
