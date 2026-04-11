import { useAuthStore } from "@/stores/auth";

const BASE_URL = "/api/v1/resources";

class ResourceApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
  ) {
    super(detail);
    this.name = "ResourceApiError";
  }
}

async function resourceFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string>),
  };

  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }

  if (init?.method && init.method !== "GET") {
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
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new ResourceApiError(res.status, body.error || res.statusText);
  }

  return res.json() as Promise<T>;
}

async function resourceFetchRaw(path: string, init?: RequestInit): Promise<Response> {
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

export { resourceFetch, resourceFetchRaw, ResourceApiError, BASE_URL };
