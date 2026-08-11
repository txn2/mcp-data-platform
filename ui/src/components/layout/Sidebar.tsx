import { cn } from "@/lib/utils";
import { LogOut, ChevronsLeft, ChevronsRight } from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { NavButton } from "./sidebar/NavButton";
import { NavSection } from "./sidebar/NavSection";
import { adminNavItems, portalNavItems } from "./sidebar/navItems";
import { useNavBadges } from "./sidebar/useNavBadges";
import { useSidebarBrand } from "./sidebar/useSidebarBrand";

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
