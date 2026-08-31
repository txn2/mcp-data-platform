import { useAuthStore } from "@/stores/auth";
import { applyCsrfHeader } from "@/api/csrf";

/**
 * fetch carrying whatever this session authenticates with.
 *
 * A cookie session needs only `credentials: "include"`; an API-key session --
 * which is how the dev portal and every API-key deployment sign in -- needs the
 * `X-API-Key` header, and a request that omits it is answered 401. That is the
 * whole reason an `<img src>` pointed straight at an authenticated route does
 * not work (see AuthImg), and the same reason a bare `fetch` against one does
 * not either.
 *
 * It takes a whole path rather than a fragment because the surfaces that use it
 * live under different API roots, and a helper that prefixed one root would
 * send half its traffic to the wrong place -- which is how every resource
 * thumbnail capture came to PUT at /api/v1/portal/resources/... and 404 (#1554).
 */
export function authedFetch(url: string, init?: RequestInit): Promise<Response> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string>),
  };
  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }
  applyCsrfHeader(headers, init?.method);
  return fetch(url, { ...init, headers, credentials: "include" });
}
