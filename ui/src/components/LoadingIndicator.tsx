import { useBranding } from "@/api/portal/hooks";
import { useThemeStore } from "@/stores/theme";

export function LoadingIndicator() {
  const { data: branding } = useBranding();
  const theme = useThemeStore((s) => s.theme);
  const isDark =
    theme === "dark" ||
    (theme === "system" &&
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);

  const base = import.meta.env.BASE_URL;
  const defaultLogo = isDark
    ? `${base}images/activity-svgrepo-com-white.svg`
    : `${base}images/activity-svgrepo-com.svg`;
  const logo = isDark
    ? branding?.portal_logo_dark || branding?.portal_logo || defaultLogo
    : branding?.portal_logo_light || branding?.portal_logo || defaultLogo;

  return (
    <div className="flex min-h-[200px] flex-col items-center justify-center gap-3">
      <img
        src={logo}
        alt=""
        className="h-16 w-16 animate-pulse-brand"
        onError={(e) => {
          (e.target as HTMLImageElement).style.display = "none";
        }}
      />
      <p className="text-sm text-muted-foreground">Loading...</p>
    </div>
  );
}
