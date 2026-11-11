import { useThemeStore } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";
import { Button } from "@/components/ui/button";
import {
  SegmentedControl,
  type SegmentedOption,
} from "@/components/patterns/SegmentedControl";
import { Sun, Moon, Monitor, Menu } from "lucide-react";
import { useIsMobile } from "@/hooks/useIsMobile";

interface Props {
  title: string;
  onMenuClick?: () => void;
}

type Theme = "light" | "dark" | "system";

// The theme trio is a segmented switch, not a tablist: nothing below it is a
// panel, the same page is redrawn in another palette.
const themeOptions: SegmentedOption<Theme>[] = [
  { value: "light", icon: Sun, label: "Light" },
  { value: "dark", icon: Moon, label: "Dark" },
  { value: "system", icon: Monitor, label: "System" },
];

/**
 * VersionBadge shows the running server version, linked when the deployment
 * configured somewhere for it to go (portal.version_url — release notes, a
 * changelog, an internal wiki). Unconfigured it stays plain text, so no reader
 * is offered a link the operator never pointed anywhere.
 */
function VersionBadge({ version, url }: { version: string; url: string }) {
  const label = `v${version}`;
  if (!url) {
    return <span className="text-xs text-muted-foreground">{label}</span>;
  }
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className="rounded-sm text-xs text-muted-foreground underline-offset-4 outline-none hover:text-foreground hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
    >
      {label}
    </a>
  );
}

// PHONE_TITLES are the section a page sits in, where a phone's header names
// the section rather than the page (#2601): "Asset Viewer" says nothing the
// page under it does not.
const PHONE_TITLES: Record<string, string> = { "Asset Viewer": "Assets" };

/** ThemeButton is the theme as one button on a phone, stepping light, dark,
 * system; the three-way switch takes more than half a phone's header. */
function ThemeButton({ theme, setTheme }: { theme: Theme; setTheme: (t: Theme) => void }) {
  const at = themeOptions.findIndex((o) => o.value === theme);
  const current = themeOptions[at] ?? themeOptions[0]!;
  const next = themeOptions[(at + 1) % themeOptions.length]!;
  const Icon = current.icon ?? Sun;
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      onClick={() => setTheme(next.value)}
      aria-label={`Theme: ${current.label}. Switch to ${next.label}`}
      title={`Theme: ${current.label}`}
      className="text-muted-foreground"
    >
      <Icon className="size-5" />
    </Button>
  );
}

export function Header({ title, onMenuClick }: Props) {
  const { theme, setTheme } = useThemeStore();
  const { data: branding } = useBranding();
  const version = branding?.version;
  const phone = useIsMobile();

  // On a phone: the section's name, no platform version, one theme button.
  if (phone) {
    return (
      <header className="flex h-[var(--app-header-height)] items-center gap-2 border-b bg-card px-2">
        {onMenuClick && (
          <Button type="button" variant="ghost" size="icon-sm" onClick={onMenuClick} aria-label="Open navigation" className="text-muted-foreground">
            <Menu className="size-5" />
          </Button>
        )}
        <h1 className="min-w-0 flex-1 truncate text-base font-semibold">{PHONE_TITLES[title] ?? title}</h1>
        <ThemeButton theme={theme} setTheme={setTheme} />
      </header>
    );
  }

  return (
    <header className="flex h-[var(--app-header-height)] items-center justify-between border-b bg-card px-4 sm:px-6">
      <div className="flex items-center gap-3">
        {onMenuClick && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={onMenuClick}
            aria-label="Open navigation"
            className="text-muted-foreground"
          >
            <Menu className="size-5" />
          </Button>
        )}
        <h1 className="text-lg font-semibold truncate">{title}</h1>
      </div>
      <div className="flex items-center gap-3">
        {version && <VersionBadge version={version} url={branding?.version_url || ""} />}
        <SegmentedControl
          label="Theme"
          value={theme}
          onChange={setTheme}
          options={themeOptions}
        />
      </div>
    </header>
  );
}
