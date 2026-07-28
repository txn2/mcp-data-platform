/** The subset of the branding payload that names a portal logo. */
export interface LogoBranding {
  portal_logo?: string;
  portal_logo_light?: string;
  portal_logo_dark?: string;
}

/**
 * Pick the portal logo for the current theme, falling back through
 * portal_logo_<theme> to portal_logo to the bundled default.
 *
 * Shared by every pre-shell screen (sign-in, access denied) and the Sidebar so a
 * deployment's branding override resolves identically wherever the logo appears.
 */
export function resolvePortalLogo(branding: LogoBranding | undefined, isDark: boolean): string {
  const themed = isDark ? branding?.portal_logo_dark : branding?.portal_logo_light;
  const bundled = isDark
    ? "images/activity-svgrepo-com-white.svg"
    : "images/activity-svgrepo-com.svg";
  return themed || branding?.portal_logo || `${import.meta.env.BASE_URL}${bundled}`;
}
