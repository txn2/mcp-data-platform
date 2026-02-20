import { useThemeStore } from "@/stores/theme";
import { useSystemInfo } from "@/api/hooks";
import { Sun, Moon, Monitor } from "lucide-react";

interface HeaderProps {
  title: string;
}

const themeOptions = [
  { value: "light" as const, icon: Sun, label: "Light" },
  { value: "dark" as const, icon: Moon, label: "Dark" },
  { value: "system" as const, icon: Monitor, label: "System" },
];

export function Header({ title }: HeaderProps) {
  const { theme, setTheme } = useThemeStore();
  const { data: systemInfo } = useSystemInfo();
  const version = systemInfo?.version ?? "dev";
  const tooltip = systemInfo
    ? `${systemInfo.version}\nCommit: ${systemInfo.commit}\nBuilt: ${systemInfo.build_date}`
    : "";

  return (
    <header className="flex h-14 items-center justify-between border-b bg-card px-6">
      <h1 className="text-lg font-semibold">{title}</h1>
      <div className="flex items-center gap-3">
        {systemInfo && (
          <span
            className="text-xs text-muted-foreground"
            title={tooltip}
          >
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
