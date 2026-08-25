import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { fakeLocalStorage } from "@/test/localStorage";
import type { Resource } from "@/api/resources/types";
import {
  TILE_INLINE_LIMIT,
  exceedsTileLimit,
  groupByCategory,
  isImageResource,
  neverRead,
  readCollapsed,
  tagOptions,
  writeCollapsed,
} from "./groups";

const daysAgo = (n: number) => new Date(Date.now() - n * 86_400_000).toISOString();

function resource(overrides: Partial<Resource> = {}): Resource {
  return {
    id: "res-1",
    scope: "user",
    scope_id: "analyst@example.com",
    category: "references",
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
  resource({ id, category: "visual", filename: `${id}.png`, mime_type: "image/png", ...extra });

describe("dividing the library into its sections", () => {
  it("puts each category in its own section, keeping the store's order inside it", () => {
    const groups = groupByCategory([
      resource({ id: "a", category: "playbooks" }),
      resource({ id: "b", category: "playbooks" }),
      resource({ id: "c", category: "data" }),
    ]);

    expect(groups.map((g) => g.category)).toEqual(["playbooks", "data"]);
    expect(groups[0]!.resources.map((r) => r.id)).toEqual(["a", "b"]);
  });

  // The server has already ordered the list by whatever the sort control asked
  // for. Sections follow the order their first member arrived in, so grouping
  // regroups the answer without reordering it: the resource the sort put first
  // is still the first row of the first section. A fixed category rank here
  // would leave "Recently read" selected and showing category order.
  it("takes its section order from the order the server returned", () => {
    const groups = groupByCategory([
      resource({ id: "a", category: "zebra" }),
      resource({ id: "b", category: "references" }),
      resource({ id: "c", category: "guides" }),
      resource({ id: "d", category: "zebra" }),
    ]);

    expect(groups.map((g) => g.category)).toEqual(["zebra", "references", "guides"]);
    expect(groups[0]!.resources.map((r) => r.id)).toEqual(["a", "d"]);
  });

  it("marks a section of nothing but images as one to show as images", () => {
    const groups = groupByCategory([photo("a"), photo("b")]);
    expect(groups[0]!.images).toBe(true);
  });

  // The grid is driven by what the section holds, not by what it is called.
  it("marks an image section as such under a category that says nothing about images", () => {
    const groups = groupByCategory([photo("a", { category: "references" })]);
    expect(groups[0]!.images).toBe(true);
  });

  it("leaves a section holding one written note as rows", () => {
    const groups = groupByCategory([photo("a"), resource({ id: "b", category: "visual" })]);
    expect(groups[0]!.images).toBe(false);
  });
});

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
  it("offers the tags the resources in view carry, in name order", () => {
    const options = tagOptions(
      [resource({ id: "a", tags: ["q3", "finance"] }), resource({ id: "b", tags: ["finance"] })],
      "",
    );
    expect(options).toEqual([
      { value: "", label: "All tags" },
      { value: "finance", label: "finance" },
      { value: "q3", label: "q3" },
    ]);
  });

  // Selecting a tag narrows the view to the resources carrying it, so a facet
  // built from the view alone would drop every other choice the moment one was
  // made. The selected tag is added back so the control still names itself.
  it("keeps the selected tag among them even when nothing in view carries it", () => {
    expect(tagOptions([], "q3")).toEqual([
      { value: "", label: "All tags" },
      { value: "q3", label: "q3" },
    ]);
  });

  it("offers only the unfiltered entry for a library nobody has tagged", () => {
    expect(tagOptions([resource()], "")).toEqual([{ value: "", label: "All tags" }]);
  });
});

// This jsdom realm has no localStorage of its own, which is why every read and
// write of it in the app is guarded. The persistence itself still has to be
// exercised, so the tests below supply a store to persist into.
describe("remembering which sections this reader folded", () => {
  beforeEach(() => vi.stubGlobal("localStorage", fakeLocalStorage()));
  afterEach(() => vi.unstubAllGlobals());

  it("round-trips the folded set", () => {
    writeCollapsed(["visual", "data"]);
    expect(readCollapsed()).toEqual(["visual", "data"]);
  });

  it("reads no preference from an empty store", () => {
    expect(readCollapsed()).toEqual([]);
  });

  // Storage is written by this browser and read back by it, but it is still
  // outside the page: unparseable or wrongly-shaped content is no preference,
  // not a library that fails to render.
  it("reads no preference from content it cannot use", () => {
    globalThis.localStorage.setItem("resource-library-collapsed", "not json");
    expect(readCollapsed()).toEqual([]);
    globalThis.localStorage.setItem("resource-library-collapsed", '{"visual":true}');
    expect(readCollapsed()).toEqual([]);
    globalThis.localStorage.setItem("resource-library-collapsed", '["visual",7]');
    expect(readCollapsed()).toEqual(["visual"]);
  });
});
