/**
 * Forms embedded in an HTML or JSX asset (#2177).
 *
 * An asset embeds a form by declaring mcp:form:<endpoint>, which the platform
 * serves rewritten to the form's page, /portal/forms/<endpoint>, framed where
 * the asset puts it. The page fetches the form's spec from its endpoint and
 * hands it the query string it was opened with, so a shared link carrying a
 * token or an id (?t=...) reaches form_spec(req) as req["query"]. The asset's
 * own frame is opened from srcdoc and has no query string of its own, so the
 * viewer copies the query of the page the asset is opened at onto each form
 * page's address before framing the document, along with the portal's theme,
 * and points the address at the origin the reader browses.
 *
 * The parameters the form page keeps for itself (the viewer token the
 * platform signs, the theme) are never taken from the page's query.
 */

/** The query parameters a form's page reads for itself, never the form's. */
export const FORM_OWN_PARAMS = ["form_viewer", "form_theme"] as const;

/** A form page's address, absolute or root-relative, and its query. */
const FORM_PAGE = /((?:https?:\/\/[^\s"'<>()]+)?\/portal\/forms\/[a-z0-9][a-z0-9_-]*)(\?[^\s"'<>()]*)?/g;

/**
 * The query to pass on: the page's, without the parameters a form page keeps
 * for itself, and the portal's theme.
 */
export function formQuery(search: string, theme: "light" | "dark" | null): string {
  const params = new URLSearchParams(search);
  for (const own of FORM_OWN_PARAMS) params.delete(own);
  if (theme) params.set("form_theme", theme);
  return params.toString();
}

/**
 * content with every form page address it names pointed at origin and given
 * query. The platform writes the address against portal.public_base_url,
 * which is not always the origin the reader browses (a proxy, a relocated
 * port), and both asset frames frame only the reader's own origin: the
 * srcdoc frame through the viewer's frame-src 'self', the JSX frame through
 * formSource. In markup the separator is written &amp;, which an attribute
 * reads as &; in JSX source it is written as itself.
 */
export function passQueryToForms(content: string, query: string, markup: boolean, origin = ""): string {
  if (!content.includes("/portal/forms/")) return content;
  const sep = markup ? "&amp;" : "&";
  const added = markup ? query.replace(/&/g, "&amp;") : query;
  return content.replace(FORM_PAGE, (_match, base: string, existing: string | undefined) => {
    const page = origin ? origin + base.slice(base.indexOf("/portal/forms/")) : base;
    if (!added) return page + (existing ?? "");
    return existing ? `${page}${existing}${sep}${added}` : `${page}?${added}`;
  });
}

/** The portal's current theme, read off the document root. */
export function portalTheme(): "light" | "dark" | null {
  if (typeof document === "undefined") return null;
  return document.documentElement.classList.contains("dark") ? "dark" : "light";
}

/**
 * content as an asset frame is handed it: each embedded form page on the
 * reader's own origin, given the query of the page the asset is opened at.
 */
export function withFormQuery(content: string, markup: boolean): string {
  if (typeof window === "undefined") return content;
  return passQueryToForms(content, formQuery(window.location.search, portalTheme()), markup, window.location.origin);
}
