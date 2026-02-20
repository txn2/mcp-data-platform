import { useEffect } from "react";
import { cn } from "@/lib/utils";
import { Home, Wrench, ScrollText, Lightbulb, Users, LogOut } from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { useSystemInfo } from "@/api/hooks";
import { useThemeStore } from "@/stores/theme";

interface SidebarProps {
  currentPath: string;
  onNavigate: (path: string) => void;
}

const navItems = [
  { path: "/", label: "Home", icon: Home },
  { path: "/tools", label: "Tools", icon: Wrench },
  { path: "/audit", label: "Audit Log", icon: ScrollText },
  { path: "/knowledge", label: "Knowledge", icon: Lightbulb },
  { path: "/personas", label: "Personas", icon: Users },
];

export function Sidebar({ currentPath, onNavigate }: SidebarProps) {
  const clearApiKey = useAuthStore((s) => s.clearApiKey);
  const { data: systemInfo } = useSystemInfo();
  const theme = useThemeStore((s) => s.theme);
  const isDark = theme === "dark" || (theme === "system" && typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  const portalTitle = systemInfo?.portal_title ?? "Admin Portal";
  const base = import.meta.env.BASE_URL;
  const defaultLogo = isDark
    ? `${base}images/activity-svgrepo-com-white.svg`
    : `${base}images/activity-svgrepo-com.svg`;
  const portalLogo = isDark
    ? (systemInfo?.portal_logo_dark || systemInfo?.portal_logo || defaultLogo)
    : (systemInfo?.portal_logo_light || systemInfo?.portal_logo || defaultLogo);

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

  return (
    <aside className="flex h-screen w-[var(--sidebar-width)] flex-col border-r bg-card">
      <div className="flex h-14 items-center gap-2 border-b px-4">
        {portalLogo && (
          <img
            src={portalLogo}
            alt=""
            className="h-7 w-7 shrink-0"
            onError={(e) => { (e.target as HTMLImageElement).style.display = 'none'; }}
          />
        )}
        <span className="text-sm font-semibold truncate">{portalTitle}</span>
      </div>

      <nav className="flex-1 space-y-1 p-2">
        {navItems.map((item) => (
          <button
            key={item.path}
            onClick={() => onNavigate(item.path)}
            className={cn(
              "flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
              currentPath.split("#")[0] === item.path
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            <item.icon className="h-4 w-4" />
            {item.label}
          </button>
        ))}
      </nav>

      <div className="border-t p-2">
        <button
          onClick={clearApiKey}
          className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <LogOut className="h-4 w-4" />
          Sign Out
        </button>
      </div>
    </aside>
  );
}
