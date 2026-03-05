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
} from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";

interface Props {
  currentPath: string;
  onNavigate: (path: string) => void;
}

const portalNavItems = [
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

export function Sidebar({ currentPath, onNavigate }: Props) {
  const logout = useAuthStore((s) => s.logout);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const { data: branding } = useBranding();
  const theme = useThemeStore((s) => s.theme);
  const isDark =
    theme === "dark" ||
    (theme === "system" &&
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);

  const portalTitle = branding?.portal_title || "MCP Platform";
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
    // Exact match for root paths, startsWith for /admin sub-routes
    if (itemPath === "/" || itemPath === "/shared") return route === itemPath;
    return route === itemPath || route.startsWith(itemPath + "/");
  }

  return (
    <aside className="flex h-screen w-[var(--sidebar-width)] flex-col border-r bg-card">
      <div className="flex h-14 items-center gap-2 border-b px-4">
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
        <span className="text-sm font-semibold truncate">{portalTitle}</span>
      </div>

      <nav className="flex-1 space-y-1 overflow-auto p-2">
        {/* Portal section — everyone */}
        {portalNavItems.map((item) => (
          <button
            key={item.path}
            type="button"
            onClick={() => onNavigate(item.path)}
            className={cn(
              "flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
              isActive(item.path)
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            <item.icon className="h-4 w-4" />
            {item.label}
          </button>
        ))}

        {/* Admin section — admins only */}
        {isAdmin && (
          <>
            <div className="my-2 border-t" />
            <p className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              Admin
            </p>
            {adminNavItems.map((item) => (
              <button
                key={item.path}
                type="button"
                onClick={() => onNavigate(item.path)}
                className={cn(
                  "flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                  isActive(item.path)
                    ? "bg-primary/10 text-primary"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground",
                )}
              >
                <item.icon className="h-4 w-4" />
                {item.label}
              </button>
            ))}
          </>
        )}
      </nav>

      <div className="border-t p-2">
        <button
          type="button"
          onClick={logout}
          className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <LogOut className="h-4 w-4" />
          Sign Out
        </button>
      </div>
    </aside>
  );
}
