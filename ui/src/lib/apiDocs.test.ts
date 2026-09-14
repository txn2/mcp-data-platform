import { describe, it, expect } from "vitest";
import { API_SPEC_URL, GATEWAY_TAG, SWAGGER_UI_URL, swaggerUiTagUrl } from "./apiDocs";

describe("the served API reference", () => {
  it("addresses a tag the way the served Swagger UI mints the anchor", () => {
    // swagger-ui writes a tag anchor as "#/" + createDeepLinkPath(tag), which
    // trims and percent-encodes whitespace. A fragment in any other shape
    // resolves to nothing and the reader lands at the top of the document.
    expect(swaggerUiTagUrl(GATEWAY_TAG)).toBe(
      "/api/v1/admin/docs/index.html#/Gateway",
    );
    expect(swaggerUiTagUrl("  Auth Keys ")).toBe(
      "/api/v1/admin/docs/index.html#/Auth%20Keys",
    );
  });

  it("reads the document from the platform's own origin", () => {
    // Both are paths, not absolute URLs: the portal and the document are served
    // by the same process, so the reader's session and any reverse proxy in
    // front of it carry over. An absolute URL would hardcode a hostname the
    // deployment chooses.
    for (const url of [API_SPEC_URL, SWAGGER_UI_URL]) {
      expect(url.startsWith("/api/v1/admin/docs/"), url).toBe(true);
    }
  });
});
