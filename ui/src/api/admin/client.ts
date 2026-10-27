import { useAuthStore } from "@/stores/auth";
import { buildLoginURL } from "@/lib/loginUrl";
import { applyCsrfHeader } from "@/api/csrf";

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
//
// A 401 that arrives within SIGN_IN_LOOP_WINDOW_MS of the last redirect
// came right after a sign-in that just completed, so the session is not
// what failed: the server refused the fresh session itself. Sending the
// browser back would sign it in again at once and repeat forever (#2337),
// so the call fails where it was made instead.
function handleUnauthorized(): void {
  const { authMethod } = useAuthStore.getState();
  if (authMethod === "cookie" && redirectedForSignInRecently()) {
    return;
  }
  useAuthStore.getState().expireSession();

  if (authMethod !== "cookie") {
    return;
  }

  markSignInRedirect();
  window.location.replace(buildLoginURL());
}

// SIGN_IN_REDIRECT_KEY holds when the last 401 sent the browser to sign in.
const SIGN_IN_REDIRECT_KEY = "mcp-portal-sign-in-redirect-at";
const SIGN_IN_LOOP_WINDOW_MS = 60_000;

function markSignInRedirect(): void {
  try {
    sessionStorage.setItem(SIGN_IN_REDIRECT_KEY, String(Date.now()));
  } catch {
    // Without storage the guard is off and a 401 redirects as before.
  }
}

function redirectedForSignInRecently(): boolean {
  try {
    const at = Number(sessionStorage.getItem(SIGN_IN_REDIRECT_KEY));
    return at > 0 && Date.now() - at < SIGN_IN_LOOP_WINDOW_MS;
  } catch {
    return false;
  }
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
  applyCsrfHeader(headers, init?.method);

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
    // res.statusText is always "" over HTTP/2 (the protocol dropped reason
    // phrases), so it cannot be the last resort. A non-JSON error body, such
    // as a proxy's HTML 502 or a gateway timeout page, would otherwise
    // produce an empty message and an error UI that renders a bare icon.
    const detail = (typeof body.detail === "string" && body.detail)
      || (typeof body.error === "string" && body.error)
      || (typeof body.message === "string" && body.message)
      || res.statusText
      || `Request failed with status ${res.status}`;
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
  applyCsrfHeader(headers, init?.method);

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
