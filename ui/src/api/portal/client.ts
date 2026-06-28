import { useAuthStore } from "@/stores/auth";

const BASE_URL = "/api/v1/portal";

class ApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
    // body is the parsed error response, so callers can read structured payloads
    // (e.g. the knowledge-page dedup 409's candidates list). Undefined when the
    // response had no JSON body.
    public body?: unknown,
  ) {
    super(detail);
    this.name = "ApiError";
  }
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string>),
  };

  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }

  // Only set Content-Type for non-GET requests (GET has no body).
  if (!init?.method || init.method !== "GET") {
    headers["Content-Type"] = headers["Content-Type"] ?? "application/json";
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers,
    credentials: "include",
  });

  if (!res.ok) {
    if (res.status === 401) {
      useAuthStore.getState().expireSession();
    }
    const body = await res.json().catch(() => ({ detail: res.statusText }));
    throw new ApiError(res.status, body.detail || body.message || res.statusText, body);
  }

  return res.json() as Promise<T>;
}

async function apiFetchRaw(path: string, init?: RequestInit): Promise<Response> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string>),
  };
  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }
  const res = await fetch(`${BASE_URL}${path}`, { ...init, headers, credentials: "include" });
  if (res.status === 401) {
    useAuthStore.getState().expireSession();
  }
  return res;
}

export { apiFetch, apiFetchRaw, ApiError, BASE_URL };
