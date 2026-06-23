import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import {
  Home,
  Wrench,
  Users,
  LogOut,
  LayoutGrid,
  ChevronsLeft,
  ChevronsRight,
  Activity,
  FileText,
  Bot,
  Clock,
  ChevronDown,
  Cable,
  KeyRound,
  MessageSquare,
  MessageCircle,
  FileUp,
  BookOpen,
  Contact,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";
import { useBranding, usePractitionerWorklist, useSMEWorklist } from "@/api/portal/hooks";

interface Props {
  currentPath: string;
  onNavigate: (path: string) => void;
  collapsed: boolean;
  onToggleCollapse: () => void;
  mobile?: boolean;
  onClose?: () => void;
}

// Fixed order (#661): Assets, Prompts, Resources, Feedback, Knowledge,
// Activity. Knowledge is the single home for the Memory -> Insight -> Knowledge
// lifecycle (the former Knowledge Pages, Knowledge & Memory surfaces). Activity
// sits last as the audit/landing view.
const basePortalNavItems = [
  { path: "/", label: "Assets", icon: LayoutGrid },
  { path: "/prompts", label: "Prompts", icon: MessageSquare },
  { path: "/resources", label: "Resources", icon: FileUp },
  { path: "/feedback", label: "Feedback", icon: MessageCircle },
  { path: "/knowledge", label: "Knowledge", icon: BookOpen },
  { path: "/activity", label: "Activity", icon: Activity },
];

interface NavItem {
  path: string;
  label: string;
  icon: typeof Home;
  children?: NavItem[];
}

// Alphabetized by label (case-insensitive). Dashboard is pinned at
// the top because it's the admin landing view; everything else sorts.
const adminNavItems: NavItem[] = [
  { path: "/admin", label: "Dashboard", icon: Home },
  { path: "/admin/agent-instructions", label: "Agent Instructions", icon: Bot },
  { path: "/admin/api-catalogs", label: "API Catalogs", icon: BookOpen },
  { path: "/admin/assets", label: "Assets", icon: LayoutGrid },
  { path: "/admin/changelog", label: "Change Log", icon: Clock },
  { path: "/admin/connections", label: "Connections", icon: Cable },
  { path: "/admin/description", label: "Description", icon: FileText },
  { path: "/admin/keys", label: "Keys", icon: KeyRound },
  { path: "/admin/personas", label: "Personas", icon: Users },
  { path: "/admin/prompts", label: "Prompts", icon: MessageSquare },
  { path: "/admin/resources", label: "Resources", icon: FileUp },
  { path: "/admin/tools", label: "Tools", icon: Wrench },
  { path: "/admin/users", label: "Users", icon: Contact },
];

export function Sidebar({ currentPath, onNavigate, collapsed, onToggleCollapse, mobile, onClose }: Props) {
  const logout = useAuthStore((s) => s.logout);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({});

  // On mobile, close the sidebar after navigating.
  const handleNavigate = (path: string) => {
    onNavigate(path);
    if (mobile && onClose) onClose();
  };

  // Feedback notification cue (#617): with no push notifications, badge the
  // Feedback nav item with open work that needs the user (resolution + their
  // validation), so they notice without opening the page.
  const practitionerWorklist = usePractitionerWorklist();
  const smeWorklist = useSMEWorklist();
  const feedbackBadge = (practitionerWorklist.data?.total ?? 0) + (smeWorklist.data?.total ?? 0);

  const portalNavItems = basePortalNavItems;
  const { data: branding } = useBranding();
  const theme = useThemeStore((s) => s.theme);
  const isDark =
    theme === "dark" ||
    (theme === "system" &&
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);

  const portalTitle = branding?.portal_title || "MCP Data Platform";
  const base = import.meta.env.BASE_URL;
  const defaultLogo = isDark
    ? `${base}images/activity-svgrepo-com-white.svg`
    : `${base}images/activity-svgrepo-com.svg`;
  const portalLogo = isDark
    ? branding?.portal_logo_dark || branding?.portal_logo || defaultLogo
    : branding?.portal_logo_light || branding?.portal_logo || defaultLogo;

  useEffect(() => {
    let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    link.type = "image/svg+xml";
    link.href = portalLogo;
  }, [portalLogo]);

  const route = currentPath.split("#")[0] ?? "/";

  function isActive(itemPath: string) {
    // Hash-based sub-routes (e.g. /admin/settings#description) — compare against full path including hash.
    if (itemPath.includes("#")) return currentPath === itemPath;
    // Assets now also covers the Collections sub-tab and the asset/collection
    // viewer routes, since Collections lives under Assets.
    if (itemPath === "/") {
      return (
        route === "/" ||
        route === "/collections" ||
        route.startsWith("/collections/") ||
        route.startsWith("/assets/") ||
        route.startsWith("/shared/assets/")
      );
    }
    if (itemPath === "/admin" || itemPath === "/activity" || itemPath === "/knowledge" || itemPath === "/prompts") return route === itemPath;
    return route === itemPath || route.startsWith(itemPath + "/");
  }

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
        {portalLogo && (
          <img
            src={portalLogo}
            alt=""
            className="h-7 w-7 shrink-0"
            onError={(e) => {
              (e.target as HTMLImageElement).style.display = "none";
            }}
          />
        )}
        {!effectiveCollapsed && (
          <span className="text-sm font-semibold truncate">{portalTitle}</span>
        )}
      </div>

      <nav className="flex-1 space-y-1 overflow-auto p-2">
        {!effectiveCollapsed && (
          <p className="px-3 py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            User
          </p>
        )}
        {portalNavItems.map((item) => (
          <button
            key={item.path}
            type="button"
            onClick={() => handleNavigate(item.path)}
            title={effectiveCollapsed ? item.label : undefined}
            className={cn(
              "relative flex w-full items-center rounded-md text-sm font-medium transition-colors",
              effectiveCollapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
              isActive(item.path)
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            <item.icon className="h-4 w-4 shrink-0" />
            {!effectiveCollapsed && <span className="flex-1 text-left">{item.label}</span>}
            {item.path === "/feedback" && feedbackBadge > 0 &&
              (effectiveCollapsed ? (
                <span className="absolute right-1.5 top-1.5 h-2 w-2 rounded-full bg-primary" aria-label={`${feedbackBadge} feedback items need you`} />
              ) : (
                <span className="rounded-full bg-primary/15 px-1.5 text-[11px] font-semibold text-primary">
                  {feedbackBadge}
                </span>
              ))}
          </button>
        ))}

        {isAdmin && (
          <>
            <div className="my-2 border-t" />
            {!effectiveCollapsed && (
              <p className="px-3 py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Admin
              </p>
            )}
            {adminNavItems.map((item) => {
              const hasChildren = item.children && item.children.length > 0;
              const isParentActive = hasChildren && (isActive(item.path) || item.children!.some((c) => isActive(c.path)));
              const isExpanded = expandedGroups[item.path] ?? isParentActive;

              if (hasChildren) {
                return (
                  <div key={item.path}>
                    <button
                      type="button"
                      onClick={() => {
                        if (effectiveCollapsed) {
                          handleNavigate(item.children![0]!.path);
                        } else {
                          setExpandedGroups((prev) => ({ ...prev, [item.path]: !isExpanded }));
                        }
                      }}
                      title={effectiveCollapsed ? item.label : undefined}
                      className={cn(
                        "flex w-full items-center rounded-md text-sm font-medium transition-colors",
                        effectiveCollapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
                        isParentActive
                          ? "text-primary"
                          : "text-muted-foreground hover:bg-muted hover:text-foreground",
                      )}
                    >
                      <item.icon className="h-4 w-4 shrink-0" />
                      {!effectiveCollapsed && (
                        <>
                          <span className="flex-1 text-left">{item.label}</span>
                          <ChevronDown className={cn("h-3 w-3 transition-transform", isExpanded && "rotate-180")} />
                        </>
                      )}
                    </button>
                    {!effectiveCollapsed && isExpanded && (
                      <div className="ml-4 space-y-0.5 border-l pl-2">
                        {item.children!.map((child) => (
                          <button
                            key={child.path}
                            type="button"
                            onClick={() => handleNavigate(child.path)}
                            className={cn(
                              "flex w-full items-center gap-2.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                              isActive(child.path)
                                ? "bg-primary/10 text-primary"
                                : "text-muted-foreground hover:bg-muted hover:text-foreground",
                            )}
                          >
                            <child.icon className="h-3.5 w-3.5 shrink-0" />
                            {child.label}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                );
              }

              return (
                <button
                  key={item.path}
                  type="button"
                  onClick={() => handleNavigate(item.path)}
                  title={effectiveCollapsed ? item.label : undefined}
                  className={cn(
                    "flex w-full items-center rounded-md text-sm font-medium transition-colors",
                    effectiveCollapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
                    isActive(item.path)
                      ? "bg-primary/10 text-primary"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )}
                >
                  <item.icon className="h-4 w-4 shrink-0" />
                  {!effectiveCollapsed && item.label}
                </button>
              );
            })}
          </>
        )}
      </nav>

      <div className="border-t p-2 space-y-1">
        {!mobile && (
          <button
            type="button"
            onClick={onToggleCollapse}
            title={effectiveCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            className={cn(
              "flex w-full items-center rounded-md text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
              effectiveCollapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
            )}
          >
            {effectiveCollapsed ? <ChevronsRight className="h-4 w-4" /> : <ChevronsLeft className="h-4 w-4" />}
            {!effectiveCollapsed && "Collapse"}
          </button>
        )}
        <button
          type="button"
          onClick={logout}
          title={effectiveCollapsed ? "Sign Out" : undefined}
          className={cn(
            "flex w-full items-center rounded-md text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
            effectiveCollapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
          )}
        >
          <LogOut className="h-4 w-4 shrink-0" />
          {!effectiveCollapsed && "Sign Out"}
        </button>
      </div>
    </aside>
  );
}
