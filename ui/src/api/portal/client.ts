import { useAuthStore } from "@/stores/auth";

const BASE_URL = "/api/v1/portal";

class ApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
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
    const body = await res.json().catch(() => ({ detail: res.statusText }));
    throw new ApiError(res.status, body.detail || res.statusText);
  }

  return res.json() as Promise<T>;
}

async function apiFetchRaw(path: string): Promise<Response> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {};
  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }
  return fetch(`${BASE_URL}${path}`, { headers, credentials: "include" });
}

export { apiFetch, apiFetchRaw, ApiError, BASE_URL };
