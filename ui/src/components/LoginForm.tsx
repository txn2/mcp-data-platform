import { useEffect, useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { useResolvedDark } from "@/stores/theme";
import { useBranding } from "@/api/portal/hooks";
import { resolvePortalLogo } from "@/lib/portalLogo";
import { usePortalTitle } from "@/hooks/usePortalTitle";
import { buildLoginURL } from "@/lib/loginUrl";
import { LogIn } from "lucide-react";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const DEFAULT_PORTAL_TAGLINE = "Sign in to access the platform.";
const DEFAULT_OIDC_BUTTON_LABEL = "Sign in with OIDC";

const AUTH_ERROR_MESSAGES: Record<string, string> = {
  access_denied: "Access was denied by the identity provider.",
  invalid_request: "The authentication request was invalid.",
  invalid_state: "The authentication session expired. Please try again.",
  auth_failed: "Authentication failed. Please try again.",
  link_invalid: "That sign-in link has expired or was already used. Ask for a new one.",
};

// The query parameter that opens the emailed-link form an external user signs
// in with (#2092). Never redirected: an external user has no account at the
// identity provider.
function emailLoginRequested(): boolean {
  return new URLSearchParams(window.location.search).get("login") === "email";
}

// The query parameter that keeps the API key form reachable on a deployment
// that sends sign-in straight to the identity provider (#2242).
function apiKeyLoginRequested(): boolean {
  return new URLSearchParams(window.location.search).get("login") === "api_key";
}

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
  // Read before getAuthError clears the query string.
  const [keyRequested] = useState(apiKeyLoginRequested);
  const [emailMode, setEmailMode] = useState(emailLoginRequested);
  const [error, setError] = useState(() => getAuthError() || "");
  // A sign-in the identity provider refused is not redirected again until
  // the user asks, or the portal and the provider would loop (#2242).
  const [authFailed] = useState(() => error !== "");
  const [loading, setLoading] = useState(false);
  const sessionExpired = useAuthStore((s) => s.sessionExpired);
  const loginApiKey = useAuthStore((s) => s.loginApiKey);
  const loginOIDC = useAuthStore((s) => s.loginOIDC);
  const { data: branding, isLoading: brandingLoading } = useBranding();

  const isDark = useResolvedDark();

  const { portalTitle } = usePortalTitle();
  const portalTagline = branding?.portal_tagline || DEFAULT_PORTAL_TAGLINE;
  const oidcButtonLabel = branding?.oidc_button_label || DEFAULT_OIDC_BUTTON_LABEL;
  const portalLogo = resolvePortalLogo(branding ?? undefined, isDark);
  const oidcEnabled = branding?.oidc_enabled ?? false;
  const loginRedirect = branding?.login_redirect ?? false;
  const keyFormOffered = !loginRedirect || (keyRequested && (branding?.allow_api_key_login ?? true));
  const redirecting = loginRedirect && !keyFormOffered && !authFailed && !emailMode;

  useEffect(() => {
    if (redirecting) window.location.replace(buildLoginURL());
  }, [redirecting]);

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

  // Until branding says whether to redirect, and while the redirect is under
  // way, the login screen is not drawn: it would flash before the identity
  // provider's page.
  if (brandingLoading || redirecting) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <LoadingIndicator />
      </div>
    );
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

        {emailMode ? (
          <EmailLinkForm onBack={() => setEmailMode(false)} />
        ) : (
          <>
            {/* SSO Button — shown when OIDC is enabled. On a redirecting
            deployment the screen is drawn without the key form only after
            the identity provider refused the sign-in, and the button is the
            way to try again. */}
            {oidcEnabled && (
              <Button type="button" onClick={loginOIDC} className="mb-3 w-full">
                <LogIn />
                {keyFormOffered ? oidcButtonLabel : "Try again"}
              </Button>
            )}

            {keyFormOffered && (
              <>
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
              </>
            )}
            {branding?.email_link_login && (
              <Button
                type="button"
                variant="link"
                size="sm"
                className="mt-2 w-full"
                onClick={() => setEmailMode(true)}
              >
                Email me a sign-in link
              </Button>
            )}
          </>
        )}
      </div>
    </div>
  );
}

// EmailLinkForm asks for a sign-in link for an external account (#2092). The
// answer is the same whether or not the address has one.
function EmailLinkForm({ onBack }: { onBack: () => void }) {
  const [email, setEmail] = useState("");
  const [state, setState] = useState<"idle" | "sending" | "sent" | "failed">(
    "idle",
  );

  async function send() {
    if (!email.trim()) return;
    setState("sending");
    try {
      const res = await fetch("/portal/auth/email-link", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: email.trim() }),
      });
      setState(res.ok ? "sent" : "failed");
    } catch {
      setState("failed");
    }
  }

  if (state === "sent") {
    return (
      <Alert className="mb-3">
        <AlertDescription>
          If that address has an account, a sign-in link is on its way.
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <>
      {state === "failed" && (
        <Alert variant="destructive" className="mb-3">
          <AlertDescription>
            The link could not be requested. Please try again.
          </AlertDescription>
        </Alert>
      )}
      <Input
        type="email"
        aria-label="Email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") void send();
        }}
        placeholder="you@example.com"
        className="mb-3"
        autoFocus
      />
      <Button
        type="button"
        className="w-full"
        disabled={!email.trim() || state === "sending"}
        onClick={() => void send()}
      >
        Email me a sign-in link
      </Button>
      <Button
        type="button"
        variant="link"
        size="sm"
        className="mt-2 w-full"
        onClick={onBack}
      >
        Back
      </Button>
    </>
  );
}
