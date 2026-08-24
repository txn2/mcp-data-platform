import { describe, it, expect } from "vitest";
import {
  assetThumbnailSrc,
  collectionItemThumbnailSrc,
  isThemeable,
  isThumbnailSupported,
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
    ]) {
      expect(isThumbnailSupported(ct)).toBe(true);
    }
    expect(isThumbnailSupported("application/pdf")).toBe(false);
  });

  it("marks only the forced-background types as themeable", () => {
    expect(isThemeable("text/markdown; charset=utf-8")).toBe(true);
    expect(isThemeable("text/csv")).toBe(true);
    // Both JSON families are drawn on the platform's own background.
    expect(isThemeable("application/json")).toBe(true);
    expect(isThemeable("application/x-ndjson")).toBe(true);
    expect(isThemeable("text/html")).toBe(false);
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
