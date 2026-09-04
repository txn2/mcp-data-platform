import { describe, it, expect } from "vitest";
import { mockResources } from "./resources";
import { fixtureTile, resourceTileSVG } from "./resourceTile";
import { isThemeable, isThumbnailSupported } from "../../lib/thumbnailSupport";

// A resource whose row says it was captured has to have bytes behind that
// claim. Twenty-four of them said so and one did, so every library capture in
// the documentation was a grid of file-type icons (#1619).

/** The content the mock serves for a fixture, as the content route serves it. */
function body(id: string): string {
  return mockResources.content[id] ?? "";
}

describe("every recorded capture can be served", () => {
  const declaring = mockResources.resources.filter((r) => r.thumbnail_s3_key);

  it("covers most of the library, or this suite proves nothing", () => {
    expect(declaring.length).toBeGreaterThan(10);
  });

  it.each(declaring.map((r) => [r.id, r] as const))("%s has a tile behind its row", (_id, r) => {
    expect(fixtureTile(r.id, r.mime_type, body(r.id))).not.toBeNull();
  });

  it.each(declaring.filter((r) => isThemeable(r.mime_type)).map((r) => [r.id, r] as const))(
    "%s draws a distinct tile in each scheme, which is what a dark key promises",
    (_id, r) => {
      const light = resourceTileSVG(r.mime_type, body(r.id), false);
      const dark = resourceTileSVG(r.mime_type, body(r.id), true);
      expect(light).toBeDefined();
      expect(dark).toBeDefined();
      expect(dark).not.toBe(light);
    },
  );

  it("records a dark capture for exactly the families that need one", () => {
    for (const r of declaring) {
      expect(Boolean(r.thumbnail_dark_s3_key), `${r.id}`).toBe(isThemeable(r.mime_type));
    }
  });
});

describe("a resource with no tile", () => {
  it("records no capture rather than one that answers nothing", () => {
    for (const r of mockResources.resources) {
      if (fixtureTile(r.id, r.mime_type, body(r.id))) continue;
      expect(r.thumbnail_s3_key, `${r.id} claims a capture it cannot serve`).toBeUndefined();
    }
  });

  it("is not offered to the capture queue either, so the library settles", () => {
    const offered = mockResources.resources.filter(
      (r) => isThumbnailSupported(r.mime_type) && !fixtureTile(r.id, r.mime_type, body(r.id)),
    );
    for (const r of offered) {
      expect(r.thumbnail_captured_at).toBeUndefined();
    }
  });
});

describe("the drawn tile", () => {
  it("is the document's own content, not a description of it", () => {
    const svg = resourceTileSVG("text/csv", mockResources.content["res-011"] ?? "", false)!;
    expect(svg).toContain("store_code");
    expect(svg).toContain("Portland");
  });

  it("draws a markdown heading larger and bolder than the prose under it", () => {
    const svg = resourceTileSVG("text/markdown", "# Title\n\nbody text\n", false)!;
    const sizeOf = (text: string) =>
      Number(new RegExp(`font-size="(\\d+)"[^>]*>${text}<`).exec(svg)?.[1]);
    expect(sizeOf("Title")).toBeGreaterThan(sizeOf("body text"));
    expect(svg).toMatch(/font-weight="700"[^>]*>Title/);
    // At the size the capturer renders at, so the tile is legible at tile
    // scale rather than a page of text too small to read.
    expect(sizeOf("body text")).toBe(12);
  });

  it("escapes markup in the file so the tile is not the file's own SVG", () => {
    const svg = resourceTileSVG("text/plain", '<script>&"\n', false)!;
    expect(svg).toContain("&lt;script&gt;&amp;&quot;");
    expect(svg).not.toContain("<script>");
  });

  it("has nothing to draw for a family the real capturer renders in a frame", () => {
    expect(resourceTileSVG("text/html", "<p>hi</p>", false)).toBeUndefined();
  });
});
