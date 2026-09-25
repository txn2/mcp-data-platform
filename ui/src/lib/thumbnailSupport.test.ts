import { describe, it, expect } from "vitest";
import { REGISTERED_CONTENT_TYPES, resolveRenderer } from "@/components/renderers/registry";
import {
  assetThumbnailSrc,
  captureFamily,
  CAPTURE_BY_RENDERER_KIND,
  collectionItemThumbnailSrc,
  collectionMosaicSrc,
  isThemeable,
  thumbnailSourceLimit,
  LARGE_THUMBNAIL_SOURCE_LIMIT,
  THUMBNAIL_SOURCE_LIMIT,
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
    thumbnail_dark_s3_key: "k/a/.thumbnail_dark.png",
    thumbnail_version: 3,
    thumbnail_dark_version: 3,
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

  it("is true for an HTML asset with no dark tile, which it is now drawn with (#1789)", () => {
    expect(thumbnailBehind(state({ thumbnail_dark_s3_key: "", thumbnail_dark_version: 0 }))).toBe(true);
  });

  // A type drawn as stored keeps one image and serves it in both modes, so
  // its empty dark key is not a gap.
  it("ignores the dark variant for a type drawn as stored", () => {
    expect(
      thumbnailBehind(state({ content_type: "image/svg+xml", thumbnail_dark_s3_key: "", thumbnail_dark_version: 0 })),
    ).toBe(false);
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
    // A PDF is drawn by the tile page itself rather than through a viewer
    // renderer, which is what the browser's missing plugin ruled out (#1794).
    expect(isThumbnailSupported("application/pdf")).toBe(true);
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

  it("marks every family drawn per color scheme as themeable", () => {
    expect(isThemeable("text/markdown; charset=utf-8")).toBe(true);
    expect(isThemeable("text/csv")).toBe(true);
    // Both JSON families are drawn on the platform's own background.
    expect(isThemeable("application/json")).toBe(true);
    expect(isThemeable("application/x-ndjson")).toBe(true);
    expect(isThemeable("text/plain")).toBe(true);
    // A document answers the scheme the renderer emulates, as it does in the
    // viewer's frame (#1789).
    expect(isThemeable("text/html; charset=utf-8")).toBe(true);
    expect(isThemeable("text/jsx")).toBe(true);
    // An SVG is drawn as stored, and is not the XML its name also contains.
    expect(isThemeable("image/svg+xml")).toBe(false);
    // A raster image is drawn as stored; capturing it twice would store the
    // same downscale under both keys.
    expect(isThemeable("image/png")).toBe(false);
    // A PDF page is drawn as the document looks, so one capture serves both
    // schemes (#1794).
    expect(isThemeable("application/pdf")).toBe(false);
  });
});

// The bound on how big a file a tile is drawn from. It rises for the families
// whose tile is drawn from part of the file: page one of a PDF, which a
// scanned letter page already carries past the default (#1794), and the first
// rows of a table, which is all the platform hands the tile page of a large
// one (#1802).
describe("thumbnailSourceLimit", () => {
  it("raises the bound for a PDF and a table, and leaves every other family on the default", () => {
    for (const ct of ["application/pdf", "text/csv", "text/tab-separated-values"]) {
      expect(thumbnailSourceLimit(ct)).toBe(LARGE_THUMBNAIL_SOURCE_LIMIT);
    }
    expect(LARGE_THUMBNAIL_SOURCE_LIMIT).toBeGreaterThan(THUMBNAIL_SOURCE_LIMIT);
    for (const ct of ["text/html", "image/png", "image/svg+xml", "text/markdown", "application/json"]) {
      expect(thumbnailSourceLimit(ct)).toBe(THUMBNAIL_SOURCE_LIMIT);
    }
  });

  // A type nothing draws has no tile to bound, and answering the raised bound
  // for it would read as "this family is drawn, generously".
  it("answers the default for a type that gets no tile at all", () => {
    expect(thumbnailSourceLimit("application/zip")).toBe(THUMBNAIL_SOURCE_LIMIT);
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
    expect(captureFamily("application/pdf")).toBe("pdf");
  });

  // "image/svg+xml" contains both fragments, and SVG is drawn as markup rather
  // than downscaled as a bitmap, so the order of the table is load-bearing.
  it("draws an SVG as markup rather than downscaling it", () => {
    expect(captureFamily("image/svg+xml")).toBe("svg");
  });

  // Every one of these contains "text", and a type is compared whole, so none
  // of them is plain text.
  it("does not read a specific text family as plain text", () => {
    expect(captureFamily("text/html")).not.toBe("text");
    expect(captureFamily("text/csv")).not.toBe("text");
    expect(captureFamily("text/markdown")).not.toBe("text");
  });

  // The seven the viewer laid out and the capturer would not draw, each of
  // which kept a content-type icon forever (#1754).
  it("draws every code family the viewer renders", () => {
    expect(captureFamily("application/yaml")).toBe("text");
    expect(captureFamily("application/xml")).toBe("text");
    expect(captureFamily("application/sql")).toBe("text");
    expect(captureFamily("text/x-python")).toBe("text");
    expect(captureFamily("text/javascript")).toBe("text");
    expect(captureFamily("text/css")).toBe("text");
    expect(captureFamily("text/tab-separated-values")).toBe("csv");
  });

  // Each is drawn on the platform's own background, so each stores a capture
  // per color scheme rather than one image for both.
  it("captures the code families in both color schemes", () => {
    for (const ct of [
      "application/yaml",
      "application/xml",
      "application/sql",
      "text/x-python",
      "text/javascript",
      "text/css",
      "text/tab-separated-values",
    ]) {
      expect(isThemeable(ct)).toBe(true);
    }
  });

  // SVG and XHTML end in "+xml" and are drawn as what they are rather than as
  // XML text, which the table's order decides; a JavaScript type is not a JSX
  // artifact.
  it("keeps the types that end in +xml in their own families", () => {
    expect(captureFamily("image/svg+xml")).toBe("svg");
    expect(captureFamily("application/xhtml+xml")).toBe("iframe");
    expect(captureFamily("text/jsx")).toBe("iframe");
    expect(captureFamily("text/javascript")).toBe("text");
  });
});

// An Office document is a zip container, and every Office Open XML type
// contains "xml". Matched anywhere in the type, a workbook was offered as XML
// and its tile was the text of its zip bytes (#1882).
describe("containers and binaries", () => {
  it("gets no tile for an Office, OpenDocument or archive type", () => {
    for (const ct of [
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      "application/vnd.openxmlformats-officedocument.presentationml.presentation",
      "application/vnd.oasis.opendocument.spreadsheet",
      "application/vnd.oasis.opendocument.text",
      "application/zip",
      "application/x-sqlite3",
      "application/vnd.acme.jsonish",
    ]) {
      expect(`${ct} -> ${captureFamily(ct)}`).toBe(`${ct} -> null`);
      expect(isThumbnailSupported(ct)).toBe(false);
      expect(isThemeable(ct)).toBe(false);
    }
  });

  it("still draws XML by its type and by any dialect's +xml suffix", () => {
    expect(captureFamily("application/xml")).toBe("text");
    expect(captureFamily("text/xml; charset=utf-8")).toBe("text");
    expect(captureFamily("application/atom+xml")).toBe("text");
    expect(captureFamily("application/rss+xml; charset=utf-8")).toBe("text");
    expect(captureFamily("application/svg+xml")).toBe("svg");
    expect(captureFamily("TEXT/CSV; header=present")).toBe("csv");
  });

  // The viewer renders every text/ type it has no family for as plain text.
  it("draws any other text/ type as plain text", () => {
    expect(captureFamily("text/calendar")).toBe("text");
    expect(captureFamily("TEXT/X-GO; charset=utf-8")).toBe("text");
    expect(captureFamily("text/csv")).toBe("csv");
  });
});

// If a browser can render it in the viewer, it can have a tile.
//
// The capturable set and the renderer registry were two unrelated tables, and
// the second was a hand-kept subset of the first, which is how seven families
// the viewer lays out every day were never offered a capture (#1754). This
// derives the expectation from the registry: a content type it renders and the
// fragment table does not draw fails here, naming the type.
describe("what the viewer renders and what the capturer draws", () => {
  it("agrees on every content type the renderer registry names", () => {
    expect(REGISTERED_CONTENT_TYPES.length).toBeGreaterThan(0);
    for (const contentType of REGISTERED_CONTENT_TYPES) {
      const declared = CAPTURE_BY_RENDERER_KIND[resolveRenderer({ contentType }).kind];
      const drawn = captureFamily(contentType);
      if (typeof declared === "string") {
        expect(`${contentType} -> ${drawn}`).toBe(`${contentType} -> ${declared}`);
      } else {
        // Rendered and deliberately not drawn, which the declaration must say
        // why of rather than leaving it to be guessed.
        expect(`${contentType} -> ${drawn}`).toBe(`${contentType} -> null`);
        expect(declared.because.length).toBeGreaterThan(20);
      }
    }
  });

  // The media families resolve by prefix rather than by name, so they are not
  // in the registry's key list; the capturer narrows image/ to the types a
  // browser actually decodes, which is the one place it is stricter than the
  // viewer and says so.
  it("draws the raster images a browser decodes and no other media", () => {
    expect(CAPTURE_BY_RENDERER_KIND.image).toBe("image");
    expect(captureFamily("image/png")).toBe("image");
    expect(captureFamily("image/tiff")).toBeNull();
    for (const kind of ["audio", "video", "binary"] as const) {
      const declared = CAPTURE_BY_RENDERER_KIND[kind];
      expect(typeof declared).toBe("object");
    }
    expect(captureFamily("audio/mpeg")).toBeNull();
    expect(captureFamily("video/mp4")).toBeNull();
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
    thumbnail_dark_s3_key: "user/u1/f/.thumbnail_dark.png",
    thumbnail_dark_captured_at: "2026-08-02T00:00:00Z",
  };
  const lightOnly = { ...resource, thumbnail_dark_s3_key: undefined, thumbnail_dark_captured_at: undefined };

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
    expect(resourceThumbnailBehind({ ...lightOnly, mime_type: "text/markdown" })).toBe(true);
    expect(resourceThumbnailBehind(lightOnly)).toBe(true);
  });

  it("ignores the dark variant for a type drawn as stored", () => {
    expect(resourceThumbnailBehind({ ...lightOnly, mime_type: "image/png" })).toBe(false);
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
  const asset = { id: "ast-1", ...state({ thumbnail_version: 6, thumbnail_dark_s3_key: "", thumbnail_dark_version: 0 }) };

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

  // A redraw by a new renderer generation keeps the version, so the URL carries
  // the generation too, or a browser shows the old picture for the hour it is
  // cached (#1789).
  it("carries the renderer generation, so a redraw is a new URL", () => {
    expect(assetThumbnailSrc({ ...asset, thumbnail_renderer: 1 })).toBe("/api/v1/portal/assets/ast-1/thumbnail?c=6&r=1");
    expect(assetThumbnailSrc({ ...asset, thumbnail_renderer: 2 })).toBe("/api/v1/portal/assets/ast-1/thumbnail?c=6&r=2");
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

describe("renderer generation in the other tile URLs (#1789)", () => {
  it("is on a resource's tile URL", () => {
    expect(
      resourceThumbnailSrc({
        id: "res-1",
        mime_type: "text/markdown",
        updated_at: "2026-08-02T00:00:00Z",
        thumbnail_s3_key: "k/.thumbnail.png",
        thumbnail_captured_at: "2026-08-02T00:00:00Z",
        thumbnail_renderer: 2,
      }),
    ).toBe("/api/v1/resources/res-1/thumbnail?c=2026-08-02T00%3A00%3A00Z&r=2");
  });

  it("is on a collection item's tile URL", () => {
    expect(
      collectionItemThumbnailSrc(
        { asset_id: "ast-1", asset_thumbnail_s3_key: "k", asset_thumbnail_version: 6, asset_thumbnail_renderer: 2 },
        "/api/v1/portal/assets",
      ),
    ).toBe("/api/v1/portal/assets/ast-1/thumbnail?c=6&r=2");
  });
});

// A collection has a dark mosaic of its members' dark tiles (#1789), stored
// beside the light one, and the route falls back to the light one.
describe("collectionMosaicSrc", () => {
  it("asks for the dark mosaic in a dark portal", () => {
    expect(collectionMosaicSrc({ id: "c1", thumbnail_s3_key: "k" }, true)).toBe(
      "/api/v1/portal/collections/c1/thumbnail?variant=dark",
    );
  });

  it("asks for the light mosaic in a light portal", () => {
    expect(collectionMosaicSrc({ id: "c1", thumbnail_s3_key: "k" })).toBe("/api/v1/portal/collections/c1/thumbnail");
  });

  it("is undefined for a collection with no mosaic, which shows the folder icon", () => {
    expect(collectionMosaicSrc({ id: "c1" }, true)).toBeUndefined();
  });
});
