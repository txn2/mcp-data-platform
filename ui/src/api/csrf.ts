import { useAuthStore } from "@/stores/auth";

/**
 * Header the backend requires on state-changing, cookie-authenticated
 * requests. Must match browsersession.CSRFHeaderName on the server.
 */
export const CSRF_HEADER = "X-CSRF-Token";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS", "TRACE"]);

/**
 * applyCsrfHeader adds the CSRF token header for state-changing requests made
 * under cookie authentication. It is a no-op for:
 *  - safe (read-only) methods, which the server does not CSRF-protect, and
 *  - API-key / Bearer auth, which is not attached automatically by the browser
 *    and therefore is not vulnerable to CSRF.
 *
 * Shared by every authenticated API client (portal, admin, resources) so the
 * header wiring lives in one place.
 */
export function applyCsrfHeader(
  headers: Record<string, string>,
  method?: string,
): void {
  const m = (method ?? "GET").toUpperCase();
  if (SAFE_METHODS.has(m)) {
    return;
  }
  const { authMethod, csrfToken } = useAuthStore.getState();
  if (authMethod === "cookie" && csrfToken) {
    headers[CSRF_HEADER] = csrfToken;
  }
}
