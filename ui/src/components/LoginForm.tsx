import { useState, useEffect } from "react";
import { useAuthStore } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";
import { LogIn } from "lucide-react";

const DEFAULT_PORTAL_TITLE = "MCP Data Platform";
const DEFAULT_PORTAL_TAGLINE = "Sign in to access the platform.";

const AUTH_ERROR_MESSAGES: Record<string, string> = {
  access_denied: "Access was denied by the identity provider.",
  invalid_request: "The authentication request was invalid.",
  invalid_state: "The authentication session expired. Please try again.",
  auth_failed: "Authentication failed. Please try again.",
};

function getAuthError(): string | null {
  const params = new URLSearchParams(window.location.search);
  const code = params.get("error");
  if (!code) return null;
  // Clear the error from the URL without reloading.
  window.history.replaceState({}, "", window.location.pathname);
  return AUTH_ERROR_MESSAGES[code] || "Authentication failed. Please try again.";
}

// resolveLogo picks the appropriate logo for the current theme, falling
// back through portal_logo_<theme> → portal_logo → bundled-default.
// Same precedence as Sidebar so the branding override pattern is
// consistent across the shell.
function resolveLogo(
  branding: { portal_logo?: string; portal_logo_light?: string; portal_logo_dark?: string } | undefined,
  isDark: boolean,
): string {
  const base = import.meta.env.BASE_URL;
  const fallback = isDark
    ? `${base}images/activity-svgrepo-com-white.svg`
    : `${base}images/activity-svgrepo-com.svg`;
  if (isDark) {
    return branding?.portal_logo_dark || branding?.portal_logo || fallback;
  }
  return branding?.portal_logo_light || branding?.portal_logo || fallback;
}

export function LoginForm() {
  const [key, setKey] = useState("");
  const [error, setError] = useState(() => getAuthError() || "");
  const [loading, setLoading] = useState(false);
  const sessionExpired = useAuthStore((s) => s.sessionExpired);
  const loginApiKey = useAuthStore((s) => s.loginApiKey);
  const loginOIDC = useAuthStore((s) => s.loginOIDC);
  const { data: branding } = useBranding();

  const theme = useThemeStore((s) => s.theme);
  const [isDark, setIsDark] = useState(
    () =>
      theme === "dark" ||
      (theme === "system" &&
        typeof window !== "undefined" &&
        window.matchMedia("(prefers-color-scheme: dark)").matches),
  );
  useEffect(() => {
    if (theme !== "system") {
      setIsDark(theme === "dark");
      return;
    }
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setIsDark(mq.matches);
    update();
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, [theme]);

  const portalTitle = branding?.portal_title || DEFAULT_PORTAL_TITLE;
  const portalTagline = branding?.portal_tagline || DEFAULT_PORTAL_TAGLINE;
  const portalLogo = resolveLogo(branding ?? undefined, isDark);
  const oidcEnabled = branding?.oidc_enabled ?? false;

  async function handleApiKeyLogin() {
    const trimmed = key.trim();
    if (!trimmed) return;

    setError("");
    setLoading(true);

    try {
      await loginApiKey(trimmed);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setLoading(false);
    }
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Enter") handleApiKeyLogin();
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/40">
      <div className="w-full max-w-sm rounded-lg border bg-card p-6 shadow-sm">
        {/* Brand header: logo + title row, tagline beneath. Matches the
            pattern operators see on downstream portals (e.g. the
            api-test fixture) so a deployment that overrides title /
            tagline / logo via portal.* config gets the same shape. */}
        <div className="mb-5 flex items-start gap-3">
          <img
            src={portalLogo}
            alt=""
            className="h-10 w-10 shrink-0"
            onError={(e) => {
              (e.target as HTMLImageElement).style.display = "none";
            }}
          />
          <div className="min-w-0">
            <h1 className="text-xl font-semibold leading-tight">{portalTitle}</h1>
            <p className="mt-1 text-sm text-muted-foreground">{portalTagline}</p>
          </div>
        </div>

        {sessionExpired && !error && (
          <p className="mb-3 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-700 dark:bg-amber-950 dark:text-amber-300">
            Your session has expired. Please sign in again.
          </p>
        )}

        {error && (
          <p className="mb-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">
            {error}
          </p>
        )}

        {/* SSO Button — shown when OIDC is enabled */}
        {oidcEnabled && (
          <button
            type="button"
            onClick={loginOIDC}
            className="mb-3 flex w-full items-center justify-center gap-2 rounded-md bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <LogIn className="h-4 w-4" />
            Sign in with OIDC
          </button>
        )}

        {/* "or use an API key" divider — only when OIDC is also an
            option, so a deployment without SSO doesn't get a redundant
            heading above its lone form. */}
        {oidcEnabled && (
          <div className="my-3 flex items-center gap-2 text-xs text-muted-foreground">
            <div className="h-px flex-1 bg-border" />
            <span>or use an API key</span>
            <div className="h-px flex-1 bg-border" />
          </div>
        )}

        <input
          type="text"
          autoComplete="off"
          data-1p-ignore
          data-lpignore="true"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="X-API-Key"
          style={{ WebkitTextSecurity: "disc" } as React.CSSProperties}
          className="mb-3 w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          autoFocus={!oidcEnabled}
        />
        <button
          type="button"
          disabled={!key.trim() || loading}
          onClick={handleApiKeyLogin}
          className={`w-full rounded-md px-4 py-2 text-sm font-medium disabled:opacity-50 ${
            oidcEnabled
              ? "border bg-secondary text-secondary-foreground hover:bg-secondary/80"
              : "bg-primary text-primary-foreground hover:bg-primary/90"
          }`}
        >
          {loading ? "Validating..." : "Sign in with API key"}
        </button>
      </div>
    </div>
  );
}
