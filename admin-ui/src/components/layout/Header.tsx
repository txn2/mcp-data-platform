import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import { useThemeStore } from "@/stores/theme";
import { Sun, Moon, Monitor } from "lucide-react";

interface HeaderProps {
  title: string;
}

const presets: { value: TimeRangePreset; label: string }[] = [
  { value: "1h", label: "Last 1h" },
  { value: "6h", label: "Last 6h" },
  { value: "24h", label: "Last 24h" },
  { value: "7d", label: "Last 7d" },
];

const themeOptions = [
  { value: "light" as const, icon: Sun, label: "Light" },
  { value: "dark" as const, icon: Moon, label: "Dark" },
  { value: "system" as const, icon: Monitor, label: "System" },
];

export function Header({ title }: HeaderProps) {
  const { preset, setPreset } = useTimeRangeStore();
  const { theme, setTheme } = useThemeStore();

  return (
    <header className="flex h-14 items-center justify-between border-b bg-card px-6">
      <h1 className="text-lg font-semibold">{title}</h1>
      <div className="flex items-center gap-4">
        <div className="flex gap-1">
          {presets.map((p) => (
            <button
              key={p.value}
              onClick={() => setPreset(p.value)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                preset === p.value
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted"
              }`}
            >
              {p.label}
            </button>
          ))}
        </div>
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
