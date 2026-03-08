import { useEffect } from "react";
import { cn } from "@/lib/utils";
import {
  Home,
  Wrench,
  ScrollText,
  Lightbulb,
  Users,
  LogOut,
  LayoutGrid,
  Share2,
  ChevronsLeft,
  ChevronsRight,
  Activity,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";

interface Props {
  currentPath: string;
  onNavigate: (path: string) => void;
  collapsed: boolean;
  onToggleCollapse: () => void;
}

const basePortalNavItems = [
  { path: "/activity", label: "My Activity", icon: Activity },
  { path: "/", label: "My Assets", icon: LayoutGrid },
  { path: "/shared", label: "Shared With Me", icon: Share2 },
];

const adminNavItems = [
  { path: "/admin", label: "Dashboard", icon: Home },
  { path: "/admin/tools", label: "Tools", icon: Wrench },
  { path: "/admin/audit", label: "Audit Log", icon: ScrollText },
  { path: "/admin/knowledge", label: "Knowledge", icon: Lightbulb },
  { path: "/admin/personas", label: "Personas", icon: Users },
];

export function Sidebar({ currentPath, onNavigate, collapsed, onToggleCollapse }: Props) {
  const logout = useAuthStore((s) => s.logout);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const userTools = useAuthStore((s) => s.user?.tools);
  const hasKnowledge = userTools?.includes("capture_insight") ?? false;

  const portalNavItems = hasKnowledge
    ? [
        ...basePortalNavItems,
        { path: "/my-knowledge", label: "My Knowledge", icon: Lightbulb },
      ]
    : basePortalNavItems;
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
    if (itemPath === "/" || itemPath === "/shared" || itemPath === "/admin" || itemPath === "/activity" || itemPath === "/my-knowledge") return route === itemPath;
    return route === itemPath || route.startsWith(itemPath + "/");
  }

  return (
    <aside
      className={cn(
        "flex h-screen flex-col border-r bg-card transition-[width] duration-200 overflow-hidden",
        collapsed ? "w-[var(--sidebar-width-collapsed)]" : "w-[var(--sidebar-width)]",
      )}
    >
      <div className={cn("flex h-14 items-center border-b shrink-0", collapsed ? "justify-center px-2" : "gap-2 px-4")}>
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
        {!collapsed && (
          <span className="text-sm font-semibold truncate">{portalTitle}</span>
        )}
      </div>

      <nav className="flex-1 space-y-1 overflow-auto p-2">
        {portalNavItems.map((item) => (
          <button
            key={item.path}
            type="button"
            onClick={() => onNavigate(item.path)}
            title={collapsed ? item.label : undefined}
            className={cn(
              "flex w-full items-center rounded-md text-sm font-medium transition-colors",
              collapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
              isActive(item.path)
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            <item.icon className="h-4 w-4 shrink-0" />
            {!collapsed && item.label}
          </button>
        ))}

        {isAdmin && (
          <>
            <div className="my-2 border-t" />
            {!collapsed && (
              <p className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Admin
              </p>
            )}
            {adminNavItems.map((item) => (
              <button
                key={item.path}
                type="button"
                onClick={() => onNavigate(item.path)}
                title={collapsed ? item.label : undefined}
                className={cn(
                  "flex w-full items-center rounded-md text-sm font-medium transition-colors",
                  collapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
                  isActive(item.path)
                    ? "bg-primary/10 text-primary"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground",
                )}
              >
                <item.icon className="h-4 w-4 shrink-0" />
                {!collapsed && item.label}
              </button>
            ))}
          </>
        )}
      </nav>

      <div className="border-t p-2 space-y-1">
        <button
          type="button"
          onClick={onToggleCollapse}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          className={cn(
            "flex w-full items-center rounded-md text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
            collapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
          )}
        >
          {collapsed ? <ChevronsRight className="h-4 w-4" /> : <ChevronsLeft className="h-4 w-4" />}
          {!collapsed && "Collapse"}
        </button>
        <button
          type="button"
          onClick={logout}
          title={collapsed ? "Sign Out" : undefined}
          className={cn(
            "flex w-full items-center rounded-md text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
            collapsed ? "justify-center px-2 py-2" : "gap-3 px-3 py-2",
          )}
        >
          <LogOut className="h-4 w-4 shrink-0" />
          {!collapsed && "Sign Out"}
        </button>
      </div>
    </aside>
  );
}
