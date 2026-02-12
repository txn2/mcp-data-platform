import { cn } from "@/lib/utils";
import { LayoutDashboard, ScrollText, LogOut } from "lucide-react";
import { useAuthStore } from "@/stores/auth";

interface SidebarProps {
  currentPath: string;
  onNavigate: (path: string) => void;
}

const navItems = [
  { path: "/", label: "Dashboard", icon: LayoutDashboard },
  { path: "/audit", label: "Audit Log", icon: ScrollText },
];

export function Sidebar({ currentPath, onNavigate }: SidebarProps) {
  const clearApiKey = useAuthStore((s) => s.clearApiKey);

  return (
    <aside className="flex h-screen w-[var(--sidebar-width)] flex-col border-r bg-card">
      <div className="flex h-14 items-center border-b px-4">
        <span className="text-sm font-semibold">MCP Admin</span>
      </div>

      <nav className="flex-1 space-y-1 p-2">
        {navItems.map((item) => (
          <button
            key={item.path}
            onClick={() => onNavigate(item.path)}
            className={cn(
              "flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
              currentPath === item.path
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
