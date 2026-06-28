// Shared construction of the OIDC login redirect. Two entry points send a user
// through login and both want to return them to where they were: a fresh
// "Sign in with OIDC" click (stores/auth.ts loginOIDC) and a 401 session-expiry
// recovery (api/admin/client.ts handleUnauthorized). Keeping the URL building in
// one place means the return_to contract stays identical across both, so a
// future change to the login path or capture rule cannot drift one entry point
// out of sync with the other.

// The captured return_to is folded into the signed OIDC state cookie at the
// server's LoginHandler. A pathological deep link long enough to push that
// cookie past the browser's ~4KB per-cookie limit would be silently dropped,
// failing login with a misleading "session expired". Cap the captured path well
// under that budget and fall back to a bare login URL (the server then lands the
// user on the default page, the documented safe fallback) rather than risk a
// dropped cookie. Real in-app routes are far shorter than this.
const MAX_RETURN_TO = 1024;

/**
 * buildLoginURL returns the OIDC login endpoint with the current in-app location
 * captured as a return_to, so a user sent through login is brought back to the
 * page they were on instead of the default landing page (#710). The server
 * sanitizes return_to against open redirects (sanitizeReturnTo in
 * pkg/browsersession/oidcflow.go), so a cross-origin or scheme-relative value is
 * dropped server-side; this only captures a same-origin in-app path.
 */
export function buildLoginURL(): string {
  const returnTo =
    window.location.pathname + window.location.search + window.location.hash;
  if (returnTo.length > MAX_RETURN_TO) {
    return "/portal/auth/login";
  }
  return "/portal/auth/login?return_to=" + encodeURIComponent(returnTo);
}
