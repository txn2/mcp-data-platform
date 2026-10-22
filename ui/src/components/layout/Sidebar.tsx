import { cn } from "@/lib/utils";
import { LogOut, ChevronsLeft, ChevronsRight } from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { initials } from "@/lib/initials";
import { NavButton } from "./sidebar/NavButton";
import { NavSection } from "./sidebar/NavSection";
import { adminNavItems, portalMoreNavItems, portalPinnedNavItems } from "./sidebar/navItems";
import { useNavBadges } from "./sidebar/useNavBadges";
import { useSidebarBrand } from "./sidebar/useSidebarBrand";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { portalSectionVisible } from "@/lib/portalAccess";

/**
 * BrandMark is the rail's masthead: the deployment's logo, and its name when
 * the rail is wide enough to carry words.
 *
 * A configured brand URL turns the whole mark into one link to the brand's own
 * site, matching how the public viewer and the guest landing page render their
 * brand block. It opens in a new tab: the mark is an outward link, and a reader
 * mid-task should not lose the portal to it. With no brand URL the mark is inert
 * markup, so an unbranded deployment gets no dead link to nowhere.
 */
function BrandMark({
  logo,
  fallbackLogo,
  title,
  url,
  showTitle,
}: {
  logo: string;
  fallbackLogo: string;
  title: string;
  url: string;
  showTitle: boolean;
}) {
  const content = (
    <>
      <img
        src={logo}
        alt=""
        className="h-7 w-7 shrink-0"
        // A configured logo URL that fails to load falls back to the bundled
        // mark rather than being hidden: collapsed, the logo is the whole
        // masthead, and hiding it would leave the brand link a zero-size
        // target that is focusable but invisible.
        onError={(e) => {
          const img = e.target as HTMLImageElement;
          if (img.src !== fallbackLogo) img.src = fallbackLogo;
        }}
      />
      {showTitle && <span className="text-sm font-semibold truncate">{title}</span>}
    </>
  );

  if (!url) {
    return <div className="flex min-w-0 items-center gap-2">{content}</div>;
  }

  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      // Collapsed, the logo alone is left: the name has to come from the
      // attributes or the link announces as bare "link" and hovers bare.
      // Expanded, the visible text already says it, so no tooltip is added.
      title={showTitle ? undefined : title}
      aria-label={title}
      className="flex min-w-0 items-center gap-2 rounded-md outline-none transition-opacity hover:opacity-80 focus-visible:ring-[3px] focus-visible:ring-ring/50"
    >
      {content}
    </a>
  );
}

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
  // A persona sets which portal areas its people have (#2225); the server
  // reports an administrator as having every one.
  const portal = usePortalAccess();
  const pinnedItems = portalPinnedNavItems.filter((item) => portalSectionVisible(item.path, portal));
  const moreItems = portalMoreNavItems.filter((item) => portalSectionVisible(item.path, portal));
  const badges = useNavBadges(isAdmin);
  const { portalLogo, fallbackLogo, portalTitle, brandURL } = useSidebarBrand();

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
      <div className={cn("flex h-14 items-center border-b shrink-0", effectiveCollapsed ? "justify-center px-2" : "px-4")}>
        <BrandMark
          logo={portalLogo}
          fallbackLogo={fallbackLogo}
          title={portalTitle}
          url={brandURL}
          showTitle={!effectiveCollapsed}
        />
      </div>

      <nav className="flex-1 space-y-1 overflow-auto p-2">
        <NavSection
          label="User"
          items={pinnedItems}
          currentPath={currentPath}
          collapsed={effectiveCollapsed}
          badges={badges}
          onNavigate={handleNavigate}
        />
        {/* Lighter than the rule above Admin: the same section, a second group. */}
        <div className="mx-3 my-1.5 border-t border-border/60" data-testid="portal-nav-group-break" />
        <NavSection
          items={moreItems}
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
        <ProfileButton
          collapsed={effectiveCollapsed}
          active={currentPath === "/profile"}
          onClick={() => handleNavigate("/profile")}
        />
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

/**
 * ProfileButton says who is signed in, above Sign Out (#2215): an avatar of
 * initials and the account's email, or its id when it has none. Collapsed,
 * the avatar alone, with the account as its hover copy. It opens the profile.
 */
function ProfileButton({ collapsed, active, onClick }: { collapsed: boolean; active: boolean; onClick: () => void }) {
  const user = useAuthStore((s) => s.user);
  if (!user) return null;
  const identity = user.email || user.user_id;
  return (
    <button
      type="button"
      onClick={onClick}
      title={collapsed ? identity : undefined}
      aria-label={`Profile: ${identity}`}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm hover:bg-accent",
        collapsed && "justify-center px-2",
        active && "bg-primary/10 text-primary",
      )}
    >
      <span
        aria-hidden
        className="flex size-6 shrink-0 items-center justify-center rounded-full bg-primary/15 text-[0.65rem] font-semibold text-primary"
      >
        {initials(identity)}
      </span>
      {!collapsed && <span className="min-w-0 truncate">{identity}</span>}
    </button>
  );
}
