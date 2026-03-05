import { useState, useEffect } from "react";
import { useAuthStore } from "@/stores/auth";
import { LogIn, Key, ChevronDown, ChevronUp } from "lucide-react";

const DEFAULT_PLATFORM_NAME = "MCP Data Platform";

export function LoginForm() {
  const [key, setKey] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [platformName, setPlatformName] = useState(DEFAULT_PLATFORM_NAME);
  const [oidcEnabled, setOidcEnabled] = useState(false);
  const [showApiKey, setShowApiKey] = useState(false);
  const loginApiKey = useAuthStore((s) => s.loginApiKey);
  const loginOIDC = useAuthStore((s) => s.loginOIDC);

  useEffect(() => {
    fetch("/api/v1/admin/public/branding")
      .then((res) => (res.ok ? res.json() : null))
      .then(
        (data: {
          name?: string;
          portal_title?: string;
          oidc_enabled?: boolean;
        } | null) => {
          const name = data?.portal_title || data?.name;
          if (name) setPlatformName(name);
          if (data?.oidc_enabled) {
            setOidcEnabled(true);
          } else {
            // No SSO available — show API key form by default.
            setShowApiKey(true);
          }
        },
      )
      .catch(() => {
        setShowApiKey(true);
      });
  }, []);

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
        <h1 className="mb-1 text-xl font-semibold">{platformName}</h1>
        <p className="mb-4 text-sm text-muted-foreground">
          Sign in to access the platform.
        </p>

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
            Sign in with SSO
          </button>
        )}

        {/* Divider + API key toggle — when SSO is also available */}
        {oidcEnabled && (
          <button
            type="button"
            onClick={() => setShowApiKey(!showApiKey)}
            className="mb-3 flex w-full items-center gap-2 text-xs text-muted-foreground hover:text-foreground"
          >
            <div className="h-px flex-1 bg-border" />
            <span className="flex items-center gap-1">
              <Key className="h-3 w-3" />
              API Key
              {showApiKey ? (
                <ChevronUp className="h-3 w-3" />
              ) : (
                <ChevronDown className="h-3 w-3" />
              )}
            </span>
            <div className="h-px flex-1 bg-border" />
          </button>
        )}

        {/* API Key form */}
        {showApiKey && (
          <>
            <input
              type="text"
              autoComplete="off"
              data-1p-ignore
              data-lpignore="true"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="API Key"
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
              {loading ? "Validating..." : "Sign In with API Key"}
            </button>
          </>
        )}
      </div>
    </div>
  );
}
