import { useState, useEffect } from "react";
import { useAuthStore } from "@/stores/auth";

const DEFAULT_PLATFORM_NAME = "MCP Data Platform";

export function LoginForm() {
  const [key, setKey] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [platformName, setPlatformName] = useState(DEFAULT_PLATFORM_NAME);
  const setApiKey = useAuthStore((s) => s.setApiKey);

  useEffect(() => {
    fetch("/api/v1/admin/public/branding")
      .then((res) => (res.ok ? res.json() : null))
      .then((data: { name?: string; portal_title?: string } | null) => {
        const name = data?.portal_title || data?.name;
        if (name) setPlatformName(name);
      })
      .catch(() => {
        // Silently fall back to default name
      });
  }, []);

  async function handleLogin() {
    const trimmed = key.trim();
    if (!trimmed) return;

    setError("");
    setLoading(true);

    try {
      const res = await fetch("/api/v1/admin/system/info", {
        headers: { "X-API-Key": trimmed },
      });
      if (!res.ok) {
        setError(res.status === 401 ? "Invalid API key" : `Server error (${res.status})`);
        return;
      }
      setApiKey(trimmed);
    } catch {
      setError("Unable to reach server");
    } finally {
      setLoading(false);
    }
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Enter") handleLogin();
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/40">
      <div className="w-full max-w-sm rounded-lg border bg-card p-6 shadow-sm">
        <h1 className="mb-1 text-xl font-semibold">{platformName}</h1>
        <p className="mb-4 text-sm text-muted-foreground">
          Enter your API key to access the admin dashboard.
        </p>
        {error && (
          <p className="mb-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">
            {error}
          </p>
        )}
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
          autoFocus
        />
        <button
          type="button"
          disabled={!key.trim() || loading}
          onClick={handleLogin}
          className="w-full rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {loading ? "Validating..." : "Sign In"}
        </button>
      </div>
    </div>
  );
}
