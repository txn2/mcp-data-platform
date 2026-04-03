import { useThemeStore } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";
import { Sun, Moon, Monitor, Menu } from "lucide-react";

interface Props {
  title: string;
  onMenuClick?: () => void;
}

const themeOptions = [
  { value: "light" as const, icon: Sun, label: "Light" },
  { value: "dark" as const, icon: Moon, label: "Dark" },
  { value: "system" as const, icon: Monitor, label: "System" },
];

export function Header({ title, onMenuClick }: Props) {
  const { theme, setTheme } = useThemeStore();
  const { data: branding } = useBranding();
  const version = branding?.version;

  return (
    <header className="flex h-14 items-center justify-between border-b bg-card px-4 sm:px-6">
      <div className="flex items-center gap-3">
        {onMenuClick && (
          <button
            type="button"
            onClick={onMenuClick}
            className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <Menu className="h-5 w-5" />
          </button>
        )}
        <h1 className="text-lg font-semibold truncate">{title}</h1>
      </div>
      <div className="flex items-center gap-3">
        {version && (
          <span className="text-xs text-muted-foreground">
            v{version}
          </span>
        )}
        <div className="flex gap-0.5 rounded-md border p-0.5">
          {themeOptions.map((opt) => (
            <button
              key={opt.value}
              onClick={() => setTheme(opt.value)}
              title={opt.label}
              className={`rounded-sm p-1.5 transition-colors ${
                theme === opt.value
                  ? "bg-muted text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              <opt.icon className="h-3.5 w-3.5" />
            </button>
          ))}
        </div>
      </div>
    </header>
  );
}
