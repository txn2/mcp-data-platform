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
  return themed || branding?.portal_logo || bundledPortalLogo(isDark);
}

/**
 * The logo shipped with the portal, for the given theme.
 *
 * It is both the default when a deployment configures no logo and the recovery
 * when a configured logo URL fails to load: a mark that renders nothing leaves
 * a hole where the masthead should be, and when the mark is a link, an invisible
 * one.
 */
export function bundledPortalLogo(isDark: boolean): string {
  const file = isDark ? "images/activity-svgrepo-com-white.svg" : "images/activity-svgrepo-com.svg";
  return `${import.meta.env.BASE_URL}${file}`;
}
