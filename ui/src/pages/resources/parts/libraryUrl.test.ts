import { describe, it, expect } from "vitest";
import { folderAddress, libraryPath, libraryTabFor, readLibraryView } from "./libraryUrl";

// The library's location lives in the route and its filters in the query
// string (#1530). What is asserted here is that the two directions agree, that
// the plain library keeps a plain address, that a browse address can never be
// mistaken for a resource id, and that a hand-typed address cannot put the
// library into a state it has no control for.

describe("reading a library view out of an address", () => {
  it("reads the plain section path as the default library at its root", () => {
    expect(readLibraryView("/resources", "/resources", "user")).toEqual({
      tab: "user",
      path: "",
      q: "",
      tag: "",
      sort: "updated",
    });
    expect(readLibraryView("/admin/resources", "/admin/resources", "all").tab).toBe("all");
  });

  it("takes the library and the folder out of the route", () => {
    expect(
      readLibraryView("/resources/lib/global/data/media-manager", "/resources", "user"),
    ).toMatchObject({ tab: "global", path: "data/media-manager" });
  });

  it("takes the search, the tag and the order out of the query", () => {
    expect(
      readLibraryView("/resources/lib/global/data?q=demand&tag=q3&sort=last_read", "/resources", "user"),
    ).toEqual({
      tab: "global",
      path: "data",
      q: "demand",
      tag: "q3",
      sort: "last_read",
    });
  });

  it("reads a library with no folder as that library's root", () => {
    expect(readLibraryView("/resources/lib/global", "/resources", "user")).toMatchObject({
      tab: "global",
      path: "",
    });
  });

  // The address bar takes anything at all, and a doubled or trailing slash
  // names the folder the person meant.
  it("drops empty segments from a hand-typed address", () => {
    expect(readLibraryView("/resources/lib/user//data/", "/resources", "user").path).toBe("data");
  });

  it("reads an order it does not recognize as the default one", () => {
    expect(readLibraryView("/resources?sort=whatever", "/resources", "user").sort).toBe("updated");
  });

  it("reads a resource's own route as the default library rather than a folder", () => {
    // /resources/{id} is one segment and browsing is at least two, so a
    // resource page mounted beside the library cannot be read as a location in
    // it.
    expect(readLibraryView("/resources/8f3ac21", "/resources", "user")).toMatchObject({
      tab: "user",
      path: "",
    });
  });
});

describe("writing a library view into an address", () => {
  const view = (over: Partial<ReturnType<typeof readLibraryView>> = {}) => ({
    tab: "user",
    path: "",
    q: "",
    tag: "",
    sort: "updated" as const,
    ...over,
  });

  it("leaves the plain library its plain address", () => {
    expect(libraryPath("/resources", view(), "user")).toBe("/resources");
  });

  it("names the library once it is not the default one", () => {
    expect(libraryPath("/resources", view({ tab: "global" }), "user")).toBe(
      "/resources/lib/global",
    );
  });

  it("names the folder as route segments, not as a query parameter", () => {
    expect(libraryPath("/resources", view({ path: "data/media-manager" }), "user")).toBe(
      "/resources/lib/user/data/media-manager",
    );
  });

  it("keeps the filters in the query", () => {
    expect(libraryPath("/resources", view({ path: "data", tag: "q3" }), "user")).toBe(
      "/resources/lib/user/data?tag=q3",
    );
  });

  it("round-trips a narrowed folder", () => {
    const original = readLibraryView(
      "/admin/resources/lib/global/data/shows?q=demand&tag=q3&sort=last_read",
      "/admin/resources",
      "all",
    );
    const path = libraryPath("/admin/resources", original, "all");
    expect(readLibraryView(path, "/admin/resources", "all")).toEqual(original);
  });

  it("escapes a search that would otherwise break the address", () => {
    const path = libraryPath("/resources", view({ q: "a&b=c" }), "user");
    expect(readLibraryView(path, "/resources", "user").q).toBe("a&b=c");
  });

  // Every level of the tree is a distinct address, which is what makes Back
  // step out one folder rather than out of the library.
  it("gives each level of the tree its own address", () => {
    const levels = ["", "data", "data/media-manager", "data/media-manager/shows"].map((path) =>
      libraryPath("/resources", view({ tab: "global", path }), "user"),
    );
    expect(new Set(levels).size).toBe(levels.length);
  });
});

describe("addressing a folder from somewhere with no view of its own", () => {
  it("names a persona library by its persona and every other by its scope", () => {
    expect(libraryTabFor("persona", "analyst")).toBe("analyst");
    expect(libraryTabFor("user", "sub-1")).toBe("user");
    expect(libraryTabFor("global", "")).toBe("global");
  });

  it("builds an address a reader can be sent to", () => {
    expect(folderAddress("/resources", "global", "data/shows")).toBe(
      "/resources/lib/global/data/shows",
    );
    expect(folderAddress("/resources", "user", "")).toBe("/resources/lib/user");
  });
});
