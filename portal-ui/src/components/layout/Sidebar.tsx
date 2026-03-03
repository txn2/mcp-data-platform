import { LayoutGrid, Share2, LogOut } from "lucide-react";
import { useAuthStore } from "@/stores/auth";

interface Props {
  currentPath: string;
  onNavigate: (path: string) => void;
}

const NAV_ITEMS = [
  { path: "/", label: "My Assets", icon: LayoutGrid },
  { path: "/shared", label: "Shared With Me", icon: Share2 },
];

export function Sidebar({ currentPath, onNavigate }: Props) {
  const clearApiKey = useAuthStore((s) => s.clearApiKey);

  const route = currentPath.split("#")[0] ?? "/";

  return (
    <aside
      className="flex h-screen flex-col border-r bg-card"
      style={{ width: "var(--sidebar-width)" }}
    >
      <div className="flex items-center gap-2 border-b px-4 py-3">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground text-sm font-bold">
          AP
        </div>
        <span className="text-sm font-semibold">Asset Portal</span>
      </div>

      <nav className="flex-1 space-y-1 p-2">
        {NAV_ITEMS.map((item) => {
          const isActive = route === item.path;
          return (
            <button
              key={item.path}
              type="button"
              onClick={() => onNavigate(item.path)}
              className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors ${
                isActive
                  ? "bg-accent text-accent-foreground font-medium"
                  : "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
              }`}
            >
              <item.icon className="h-4 w-4" />
              {item.label}
            </button>
          );
        })}
      </nav>

      <div className="border-t p-2">
        <button
          type="button"
          onClick={clearApiKey}
          className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
        >
          <LogOut className="h-4 w-4" />
          Sign Out
        </button>
      </div>
    </aside>
  );
}
