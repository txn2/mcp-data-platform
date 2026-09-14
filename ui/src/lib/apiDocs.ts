// Where the platform describes its own REST surface, and how a reader over that
// description addresses one tag.
//
// The document and the Swagger UI over it are served by the platform itself
// (pkg/admin/handler.go, `docsPrefix`), from the mux that runs before the admin
// session check, so both are reachable by every portal reader and not by
// administrators alone. That is what lets the caller-facing operation browser
// point at them.

/** API_SPEC_URL is the served OpenAPI document, same origin as the portal. */
export const API_SPEC_URL = "/api/v1/admin/docs/doc.json";

/** SWAGGER_UI_URL is the interactive reader over that document: the one with
 * "Try it out", which an operator with an API key can send a request from. */
export const SWAGGER_UI_URL = "/api/v1/admin/docs/index.html";

/** GATEWAY_TAG is the tag the document files `POST /gateway/{connection}/invoke`
 * and `/invoke-raw` under -- the routes a non-MCP caller uses. */
export const GATEWAY_TAG = "Gateway";

/**
 * swaggerUiTagUrl is the address of one tag's section in the served Swagger UI.
 *
 * The fragment is Swagger UI's own: its deep-linking plugin writes a tag anchor
 * as `#/` followed by `createDeepLinkPath(tag)`, which trims the name and
 * percent-encodes whitespace (swagger-ui `src/core/utils.js`, and the anchor in
 * `operation-tag.jsx`). Reproduced here rather than guessed, because a fragment
 * that does not match the one the page mints scrolls nowhere.
 */
export function swaggerUiTagUrl(tag: string): string {
  return `${SWAGGER_UI_URL}#/${tag.trim().replace(/\s/g, "%20")}`;
}
