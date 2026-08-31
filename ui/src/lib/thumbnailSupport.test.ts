import { describe, it, expect } from "vitest";
import {
  assetThumbnailSrc,
  captureFamily,
  collectionItemThumbnailSrc,
  isThemeable,
  isThumbnailSupported,
  resourceThumbnailBehind,
  resourceThumbnailSrc,
  thumbnailBehind,
} from "./thumbnailSupport";

// thumbnailBehind is what the asset viewer asks of the asset it is showing.
// The refresh queue asks the same question of every asset at once, in SQL
// (internal/portal/portalstore), so these cases mirror the ones pinned in
// pending_realdb_integration_test.go.
function state(over: Partial<Parameters<typeof thumbnailBehind>[0]> = {}) {
  return {
    content_type: "text/html",
    current_version: 3,
    thumbnail_s3_key: "k/a/.thumbnail.png",
    thumbnail_dark_s3_key: "",
    thumbnail_version: 3,
    thumbnail_dark_version: 0,
    ...over,
  };
}

describe("thumbnailBehind", () => {
  it("is false for a capture taken from the current version", () => {
    expect(thumbnailBehind(state())).toBe(false);
  });

  it("is true when no capture has ever been taken", () => {
    expect(thumbnailBehind(state({ thumbnail_s3_key: "", thumbnail_version: 0 }))).toBe(true);
  });

  // The image still serves — that is the point of keeping the pointer — but it
  // shows the body the asset had two versions ago (#1431).
  it("is true when the capture is older than the asset's current version", () => {
    expect(thumbnailBehind(state({ thumbnail_version: 1 }))).toBe(true);
  });

  // The object sits beside the content as an ordinary file, which is what keeps
  // a CSV asset from registering as a table (#1327).
  it("is true when the capture is under the pre-rename filename", () => {
    expect(thumbnailBehind(state({ thumbnail_s3_key: "k/a/thumbnail.png" }))).toBe(true);
  });

  // Markdown and CSV are rendered on a forced background, so each carries its
  // own dark capture; a light pass that landed while the dark one threw leaves
  // exactly this state.
  it("is true for a themeable asset whose dark variant is missing", () => {
    expect(
      thumbnailBehind(state({ content_type: "text/csv", thumbnail_dark_s3_key: "" })),
    ).toBe(true);
  });

  it("is true for a themeable asset whose dark variant is behind", () => {
    expect(
      thumbnailBehind(
        state({
          content_type: "text/markdown",
          thumbnail_dark_s3_key: "k/a/.thumbnail_dark.png",
          thumbnail_dark_version: 2,
        }),
      ),
    ).toBe(true);
  });

  // A type carrying its own colors stores one image and serves it in both
  // modes, so its empty dark key is not a gap.
  it("ignores the dark variant for a type that carries its own colors", () => {
    expect(thumbnailBehind(state({ content_type: "text/html" }))).toBe(false);
  });
});

describe("thumbnail support", () => {
  it("recognizes the types the capturer renders", () => {
    for (const ct of [
      "text/html",
      "text/jsx",
      "image/svg+xml",
      "text/markdown",
      "text/csv",
      "application/json",
      "application/x-ndjson",
      "application/jsonl",
      "application/vnd.acme.report+json",
      // Plain text is one of the commonest things anyone uploads and got no
      // thumbnail at all, for either kind, until #1568.
      "text/plain",
      "text/plain; charset=utf-8",
      // The capturer downscales a raster image rather than rendering it, and
      // an image asset was never offered the work.
      "image/png",
      "image/jpeg",
      "image/webp",
      "image/gif",
    ]) {
      expect(isThumbnailSupported(ct)).toBe(true);
    }
    expect(isThumbnailSupported("application/pdf")).toBe(false);
    expect(isThumbnailSupported("application/zip")).toBe(false);
  });

  // A capture decodes a raster image in the browser, so an image no browser
  // decodes is work that fails every time. The server's pending list is a
  // bounded window, so offering them would starve the documents behind them.
  it("does not offer an image family no browser decodes", () => {
    expect(isThumbnailSupported("image/tiff")).toBe(false);
    expect(isThumbnailSupported("image/heic")).toBe(false);
    expect(isThumbnailSupported("image/vnd.adobe.photoshop")).toBe(false);
  });

  it("marks only the forced-background types as themeable", () => {
    expect(isThemeable("text/markdown; charset=utf-8")).toBe(true);
    expect(isThemeable("text/csv")).toBe(true);
    // Both JSON families are drawn on the platform's own background.
    expect(isThemeable("application/json")).toBe(true);
    expect(isThemeable("application/x-ndjson")).toBe(true);
    expect(isThemeable("text/plain")).toBe(true);
    expect(isThemeable("text/html")).toBe(false);
    // A raster image carries its own colors; capturing it twice would store
    // the same downscale under both keys.
    expect(isThemeable("image/png")).toBe(false);
  });
});

// The capturer dispatches on this rather than on a second list of its own, so
// what a surface offers and what can be drawn cannot disagree (#1568).
describe("captureFamily", () => {
  it("names the family each type is drawn as", () => {
    expect(captureFamily("text/html")).toBe("iframe");
    expect(captureFamily("text/jsx")).toBe("iframe");
    expect(captureFamily("text/markdown")).toBe("markdown");
    expect(captureFamily("text/csv")).toBe("csv");
    expect(captureFamily("application/json")).toBe("json");
    expect(captureFamily("text/plain")).toBe("text");
    expect(captureFamily("image/png")).toBe("image");
    expect(captureFamily("application/pdf")).toBeNull();
  });

  // "image/svg+xml" contains both fragments, and SVG is drawn as markup rather
  // than downscaled as a bitmap, so the order of the table is load-bearing.
  it("draws an SVG as markup rather than downscaling it", () => {
    expect(captureFamily("image/svg+xml")).toBe("svg");
  });

  // Every one of these contains "text", which is why the plain-text fragment is
  // spelled in full.
  it("does not read a specific text family as plain text", () => {
    expect(captureFamily("text/html")).not.toBe("text");
    expect(captureFamily("text/csv")).not.toBe("text");
    expect(captureFamily("text/markdown")).not.toBe("text");
  });
});

// A resource carries no version: its captures are dated against the file's own
// updated_at, which is the comparison the pending query makes in SQL
// (pkg/resource, buildPendingThumbnails).
describe("resourceThumbnailBehind", () => {
  const resource = {
    id: "res-1",
    mime_type: "text/html",
    updated_at: "2026-08-02T00:00:00Z",
    thumbnail_s3_key: "user/u1/f/.thumbnail.png",
    thumbnail_captured_at: "2026-08-02T00:00:00Z",
  };

  it("is false for a capture taken at the file's own last write", () => {
    expect(resourceThumbnailBehind(resource)).toBe(false);
  });

  it("is true when no capture has ever been taken", () => {
    expect(
      resourceThumbnailBehind({
        ...resource,
        thumbnail_s3_key: undefined,
        thumbnail_captured_at: undefined,
      }),
    ).toBe(true);
  });

  it("is true when the capture predates the file's last write", () => {
    expect(
      resourceThumbnailBehind({ ...resource, thumbnail_captured_at: "2026-08-01T00:00:00Z" }),
    ).toBe(true);
  });

  it("is true for a themeable resource whose dark variant is missing", () => {
    expect(resourceThumbnailBehind({ ...resource, mime_type: "text/markdown" })).toBe(true);
  });

  it("ignores the dark variant for a type that carries its own colors", () => {
    expect(resourceThumbnailBehind(resource)).toBe(false);
  });
});

// The library grid asked for neither variant, so a themeable resource with a
// dark capture stored was drawn as a white card in a dark grid (#1568).
describe("resourceThumbnailSrc", () => {
  const resource = {
    id: "res-1",
    mime_type: "text/markdown",
    updated_at: "2026-08-02T00:00:00Z",
    thumbnail_s3_key: "user/u1/f/.thumbnail.png",
    thumbnail_captured_at: "2026-08-02T00:00:00Z",
    thumbnail_dark_s3_key: "user/u1/f/.thumbnail_dark.png",
    thumbnail_dark_captured_at: "2026-08-02T00:00:00Z",
  };

  it("carries the moment the capture was taken", () => {
    expect(resourceThumbnailSrc(resource)).toBe(
      "/api/v1/resources/res-1/thumbnail?c=2026-08-02T00%3A00%3A00Z",
    );
  });

  it("asks for the dark capture when the portal is dark", () => {
    expect(resourceThumbnailSrc(resource, true)).toBe(
      "/api/v1/resources/res-1/thumbnail?variant=dark&c=2026-08-02T00%3A00%3A00Z",
    );
  });

  // An HTML resource stores one capture and serves it in both modes, so its
  // empty dark key means "use the light one", not "no thumbnail".
  it("falls back to the light capture when the resource has no dark variant", () => {
    expect(
      resourceThumbnailSrc(
        {
          ...resource,
          mime_type: "text/html",
          thumbnail_dark_s3_key: undefined,
          thumbnail_dark_captured_at: undefined,
        },
        true,
      ),
    ).toBe("/api/v1/resources/res-1/thumbnail?c=2026-08-02T00%3A00%3A00Z");
  });

  it("is undefined when no capture has been recorded, which is what shows the icon", () => {
    expect(resourceThumbnailSrc({ ...resource, thumbnail_s3_key: undefined })).toBeUndefined();
  });
});

// The endpoint serves one URL per asset and variant and its response is
// cacheable for an hour, so a refreshed capture that reused the URL would not
// reach a browser holding the old image until the hour was up (#1431).
describe("assetThumbnailSrc", () => {
  const asset = { id: "ast-1", ...state({ thumbnail_version: 6 }) };

  it("carries the version of the capture it points at", () => {
    expect(assetThumbnailSrc(asset)).toBe("/api/v1/portal/assets/ast-1/thumbnail?c=6");
  });

  it("asks for the dark variant and its own version when the portal is dark", () => {
    const themed = {
      ...asset,
      content_type: "text/csv",
      thumbnail_dark_s3_key: "k/a/.thumbnail_dark.png",
      thumbnail_dark_version: 5,
    };
    expect(assetThumbnailSrc(themed, true)).toBe(
      "/api/v1/portal/assets/ast-1/thumbnail?variant=dark&c=5",
    );
  });

  it("falls back to the light capture when the asset has no dark variant", () => {
    expect(assetThumbnailSrc(asset, true)).toBe("/api/v1/portal/assets/ast-1/thumbnail?c=6");
  });

  it("is undefined when no capture has been recorded, which is what shows the icon", () => {
    expect(assetThumbnailSrc({ ...asset, thumbnail_s3_key: "" })).toBeUndefined();
  });
});

// A collection tile asks the same questions of the same captures, under the
// item's field names and against whichever asset route the reader is entitled
// to. Before #1468 it built its own URL and never asked for a variant, so every
// thumbnail in a collection was the light capture in both color modes.
describe("collectionItemThumbnailSrc", () => {
  const PORTAL = "/api/v1/portal/assets";
  const ADMIN = "/api/v1/admin/assets";
  const item = {
    asset_id: "ast-1",
    asset_thumbnail_s3_key: "k/a/.thumbnail.png",
    asset_thumbnail_version: 6,
  };
  const themed = {
    ...item,
    asset_thumbnail_dark_s3_key: "k/a/.thumbnail_dark.png",
    asset_thumbnail_dark_version: 5,
  };

  it("carries the version of the capture it points at", () => {
    expect(collectionItemThumbnailSrc(item, PORTAL)).toBe(`${PORTAL}/ast-1/thumbnail?c=6`);
  });

  it("asks for the dark variant and its own version when the portal is dark", () => {
    expect(collectionItemThumbnailSrc(themed, PORTAL, true)).toBe(
      `${PORTAL}/ast-1/thumbnail?variant=dark&c=5`,
    );
  });

  // An administrator reading a collection they do not own gets its tiles from
  // the admin route (#1292), and has a color mode of their own.
  it("asks the admin route for the dark variant too", () => {
    expect(collectionItemThumbnailSrc(themed, ADMIN, true)).toBe(
      `${ADMIN}/ast-1/thumbnail?variant=dark&c=5`,
    );
  });

  it("falls back to the light capture when the asset has no dark variant", () => {
    expect(collectionItemThumbnailSrc(item, PORTAL, true)).toBe(`${PORTAL}/ast-1/thumbnail?c=6`);
  });

  it("is undefined when no capture has been recorded, which is what shows the icon", () => {
    expect(collectionItemThumbnailSrc({ asset_id: "ast-1" }, PORTAL)).toBeUndefined();
  });

  // An item served by a deployment that predates the join carrying the version
  // still resolves to a URL rather than "undefined" in the query string.
  it("uses version zero when the item carries no capture version", () => {
    expect(
      collectionItemThumbnailSrc({ asset_id: "ast-1", asset_thumbnail_s3_key: "k.png" }, PORTAL),
    ).toBe(`${PORTAL}/ast-1/thumbnail?c=0`);
  });
});
