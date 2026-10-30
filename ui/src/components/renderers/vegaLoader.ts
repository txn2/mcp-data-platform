import { loader as vegaLoader, type Loader } from "vega";

/**
 * The path every viewing surface serves a declared reference under: an asset
 * that names mcp://... or mcp:asset:... in its content is served with each
 * declared one rewritten to /portal/refs/{asset}/{token}.
 */
export const REFS_PATH = "/portal/refs/";

/**
 * The address a Vega-Lite spec may load in place of the one it names, or null
 * when it may load nothing there (#2296).
 *
 * Only a declared reference, which the serving rewrite turns into a URL under
 * REFS_PATH. Save refuses a spec that names anything else, and this refuses an
 * address the spec builds at run time from its data: an image mark's url
 * field, a lookup's source.
 *
 * The rewrite writes the deployment's public_base_url in front of the path,
 * which need not be the origin the reader opened the page on (a proxy, a
 * second hostname). Only the path and query are kept, on the page's own
 * origin, so a chart never reaches another host, whatever host it names.
 */
export function mayLoad(uri: string, origin: string = window.location.origin): URL | null {
  let url: URL;
  try {
    url = new URL(uri, origin);
  } catch {
    return null;
  }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || !url.pathname.startsWith(REFS_PATH)) return null;
  return new URL(url.pathname + url.search, origin);
}

/**
 * Vega's loader with its one gate replaced. Every data source, image and link
 * a view loads goes through sanitize, with the context it is for; a link is
 * never followed and anything else loads only from a declared reference.
 */
export function refsOnlyLoader(): Loader {
  const base = vegaLoader();
  return {
    ...base,
    sanitize: async (uri, options) => {
      if (options?.context === "href") {
        throw new Error("links in a chart are not followed");
      }
      const url = mayLoad(uri);
      if (!url) {
        throw new Error(`a chart loads only a declared reference; refused ${uri}`);
      }
      return { href: url.href };
    },
  };
}
