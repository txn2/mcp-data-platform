import { useAuthStore } from "@/stores/auth";
import { buildLoginURL } from "@/lib/loginUrl";

const BASE_URL = "/api/v1/admin";

class ApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
  ) {
    super(detail);
    this.name = "ApiError";
  }
}

// handleUnauthorized centralizes session-expiry recovery for admin API
// calls. The cookie-backed session is what gates /api/v1/admin/*, so a
// 401 from any admin endpoint means the browser session is gone (or
// never existed). Cookie-mode users are sent back through OIDC with a
// return_to that brings them right back to the page they were on so
// the action they were trying to take (e.g. starting a connection's
// upstream OAuth flow) doesn't get lost. API-key-mode users get the
// store flag flipped — their key is server-side invalid, the login
// form re-renders with "your session has expired" and they re-enter.
function handleUnauthorized(): void {
  const { authMethod } = useAuthStore.getState();
  useAuthStore.getState().expireSession();

  if (authMethod !== "cookie") {
    return;
  }

  window.location.replace(buildLoginURL());
}

// apiFetchAt is the shared authenticated JSON fetch. It attaches the
// session credential (cookie or X-API-Key), routes 401s through the
// session-expiry recovery, and normalizes error bodies into ApiError.
// The base is a parameter so non-admin authenticated surfaces (e.g. the
// observability proxy under /api/v1/observability) reuse the exact same
// auth + error handling instead of forking it.
async function apiFetchAt<T>(base: string, path: string, init?: RequestInit): Promise<T> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init?.headers as Record<string, string>),
  };

  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }

  const res = await fetch(`${base}${path}`, {
    ...init,
    headers,
    credentials: "include",
  });

  if (!res.ok) {
    if (res.status === 401) {
      handleUnauthorized();
    }
    const body = await res.json().catch(() => ({} as Record<string, unknown>));
    const detail = (typeof body.detail === "string" && body.detail)
      || (typeof body.error === "string" && body.error)
      || (typeof body.message === "string" && body.message)
      || res.statusText;
    throw new ApiError(res.status, detail);
  }

  return res.json() as Promise<T>;
}

function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  return apiFetchAt<T>(BASE_URL, path, init);
}

async function apiFetchRaw(path: string, init?: RequestInit): Promise<Response> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string>),
  };

  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers,
    credentials: "include",
  });

  if (res.status === 401) {
    handleUnauthorized();
  }

  return res;
}

export { apiFetch, apiFetchAt, apiFetchRaw, ApiError, BASE_URL };
