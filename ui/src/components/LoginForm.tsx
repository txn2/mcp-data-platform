import { useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { useResolvedDark } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";
import { resolvePortalLogo } from "@/lib/portalLogo";
import { LogIn } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const DEFAULT_PORTAL_TITLE = "MCP Data Platform";
const DEFAULT_PORTAL_TAGLINE = "Sign in to access the platform.";
const DEFAULT_OIDC_BUTTON_LABEL = "Sign in with OIDC";

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

export function LoginForm() {
  const [key, setKey] = useState("");
  const [error, setError] = useState(() => getAuthError() || "");
  const [loading, setLoading] = useState(false);
  const sessionExpired = useAuthStore((s) => s.sessionExpired);
  const loginApiKey = useAuthStore((s) => s.loginApiKey);
  const loginOIDC = useAuthStore((s) => s.loginOIDC);
  const { data: branding } = useBranding();

  const isDark = useResolvedDark();

  const portalTitle = branding?.portal_title || DEFAULT_PORTAL_TITLE;
  const portalTagline = branding?.portal_tagline || DEFAULT_PORTAL_TAGLINE;
  const oidcButtonLabel = branding?.oidc_button_label || DEFAULT_OIDC_BUTTON_LABEL;
  const portalLogo = resolvePortalLogo(branding ?? undefined, isDark);
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
    <div className="flex min-h-screen items-center justify-center bg-background">
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
          <Alert variant="warning" className="mb-3">
            <AlertDescription>Your session has expired. Please sign in again.</AlertDescription>
          </Alert>
        )}

        {error && (
          <Alert variant="destructive" className="mb-3">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {/* SSO Button — shown when OIDC is enabled */}
        {oidcEnabled && (
          <Button type="button" onClick={loginOIDC} className="mb-3 w-full">
            <LogIn />
            {oidcButtonLabel}
          </Button>
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

        <Input
          type="text"
          autoComplete="off"
          data-1p-ignore
          data-lpignore="true"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="X-API-Key"
          style={{ WebkitTextSecurity: "disc" } as React.CSSProperties}
          className="mb-3"
          autoFocus={!oidcEnabled}
        />
        {/* With SSO on the page the key form is the secondary way in, so it
            takes the secondary face and leaves the filled one to OIDC. */}
        <Button
          type="button"
          variant={oidcEnabled ? "secondary" : "default"}
          disabled={!key.trim() || loading}
          onClick={handleApiKeyLogin}
          className="w-full"
        >
          {loading ? "Validating..." : "Sign in with API key"}
        </Button>
      </div>
    </div>
  );
}
