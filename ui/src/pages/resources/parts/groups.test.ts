import { describe, it, expect } from "vitest";
import type { Resource } from "@/api/resources/types";
import {
  TILE_INLINE_LIMIT,
  exceedsTileLimit,
  isImageResource,
  neverRead,
  tagOptions,
} from "./groups";

const daysAgo = (n: number) => new Date(Date.now() - n * 86_400_000).toISOString();

function resource(overrides: Partial<Resource> = {}): Resource {
  return {
    id: "res-1",
    scope: "user",
    scope_id: "analyst@example.com",
    path: "references",
    filename: "notes.md",
    display_name: "Notes",
    description: "",
    mime_type: "text/markdown",
    size_bytes: 64,
    s3_key: "k",
    uri: "mcp://resources/analyst/notes.md",
    tags: [],
    uploader_sub: "analyst@example.com",
    uploader_email: "analyst@example.com",
    created_at: "2026-08-03T10:00:00Z",
    updated_at: "2026-08-17T10:00:00Z",
    ...overrides,
  };
}

const photo = (id: string, extra: Partial<Resource> = {}) =>
  resource({ id, path: "visual", filename: `${id}.png`, mime_type: "image/png", ...extra });

describe("what counts as an image tile", () => {
  it("takes the raster families", () => {
    expect(isImageResource(photo("a"))).toBe(true);
    expect(isImageResource(resource({ mime_type: "image/jpeg", filename: "a.jpg" }))).toBe(true);
  });

  // SVG resolves to its own family: the viewer sanitizes and renders it inline
  // rather than pointing an element at the content endpoint, which serves it as
  // an attachment for the same reason. A section of SVG logos is rows.
  it("leaves SVG out of them", () => {
    expect(isImageResource(resource({ mime_type: "image/svg+xml", filename: "logo.svg" }))).toBe(
      false,
    );
  });

  it("leaves a document out of them", () => {
    expect(isImageResource(resource())).toBe(false);
  });
});

describe("the tile size cutoff", () => {
  it("loads an image under it and stands in for one over it", () => {
    expect(exceedsTileLimit(photo("a", { size_bytes: TILE_INLINE_LIMIT }))).toBe(false);
    expect(exceedsTileLimit(photo("b", { size_bytes: TILE_INLINE_LIMIT + 1 }))).toBe(true);
  });
});

// The library's Last-read column and its image tiles flag the same resources,
// so the rule is read from one place rather than reimplemented per surface.
describe("what counts as never read", () => {
  it("flags a resource old enough to have been read and never was", () => {
    expect(neverRead(resource({ created_at: daysAgo(60) }))).toBe(true);
  });

  it("does not flag one uploaded too recently for that to mean anything", () => {
    expect(neverRead(resource({ created_at: daysAgo(3) }))).toBe(false);
  });

  it("does not flag one that has been read", () => {
    expect(neverRead(resource({ created_at: daysAgo(60), last_read_at: daysAgo(1) }))).toBe(false);
  });
});

describe("the tag facet's choices", () => {
  // The tags are the library's, from the facets endpoint, rather than the ones
  // a loaded page happens to carry (#1555): at a library root no page is loaded
  // at all, and the facet was empty there.
  it("offers every tag the library holds, in name order", () => {
    expect(tagOptions(["q3", "finance"], "")).toEqual([
      { value: "", label: "All tags" },
      { value: "finance", label: "finance" },
      { value: "q3", label: "q3" },
    ]);
  });

  it("names each tag once however many files carry it", () => {
    expect(tagOptions(["finance", "finance"], "")).toEqual([
      { value: "", label: "All tags" },
      { value: "finance", label: "finance" },
    ]);
  });

  // The selected tag may name one no longer in use, and a facet that dropped it
  // would leave no way back but the unfiltered entry.
  it("keeps the selected tag among them even when the library no longer holds it", () => {
    expect(tagOptions([], "q3")).toEqual([
      { value: "", label: "All tags" },
      { value: "q3", label: "q3" },
    ]);
  });

  it("offers only the unfiltered entry for a library nobody has tagged", () => {
    expect(tagOptions([], "")).toEqual([{ value: "", label: "All tags" }]);
  });
});

// This jsdom realm has no localStorage of its own, which is why every read and
// write of it in the app is guarded. The persistence itself still has to be
// exercised, so the tests below supply a store to persist into.
