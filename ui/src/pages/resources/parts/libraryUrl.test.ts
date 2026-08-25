import { describe, it, expect } from "vitest";
import { libraryPath, readLibraryView } from "./libraryUrl";

// The library's view is in the address bar so that Back from a resource page
// lands on the library the reader left, and so a narrowed library can be linked
// to (#1470). What is asserted here is that the two directions agree, that the
// plain library keeps a plain address, and that a hand-typed address cannot put
// the library into a state it has no control for.

describe("reading a library view out of an address", () => {
  it("falls back to the section's own default scope", () => {
    expect(readLibraryView("", "user")).toEqual({
      tab: "user",
      q: "",
      category: "",
      tag: "",
      sort: "updated",
    });
    expect(readLibraryView("", "all").tab).toBe("all");
  });

  it("takes the scope, the search, the category, the tag and the order", () => {
    expect(
      readLibraryView("?tab=global&q=demand&category=references&tag=q3&sort=last_read", "user"),
    ).toEqual({
      tab: "global",
      q: "demand",
      category: "references",
      tag: "q3",
      sort: "last_read",
    });
  });

  it("reads an order it does not recognize as the default one", () => {
    expect(readLibraryView("?sort=whatever", "user").sort).toBe("updated");
  });

  it("reads an empty scope as the default one", () => {
    expect(readLibraryView("?tab=", "user").tab).toBe("user");
  });
});

describe("writing a library view into an address", () => {
  it("leaves the plain library its plain address", () => {
    const view = readLibraryView("", "user");
    expect(libraryPath("/resources", view, "user")).toBe("/resources");
  });

  it("names only what is not at its default", () => {
    const view = readLibraryView("?tab=global", "user");
    expect(libraryPath("/resources", view, "user")).toBe("/resources?tab=global");
  });

  // The tag facet is the one the server has always supported and the page never
  // set (#1471); it belongs in the address for the reason the others do.
  it("carries a tag the view is narrowed to", () => {
    const view = readLibraryView("?tag=q3", "user");
    expect(libraryPath("/resources", view, "user")).toBe("/resources?tag=q3");
  });

  it("round-trips a narrowed library", () => {
    const search = "?tab=global&q=demand&category=references&tag=q3&sort=last_read";
    const view = readLibraryView(search, "user");
    const path = libraryPath("/admin/resources", view, "all");
    expect(readLibraryView(path.slice(path.indexOf("?")), "all")).toEqual(view);
  });

  it("escapes a search that would otherwise break the address", () => {
    const view = { tab: "user", q: "a&b=c", category: "", tag: "", sort: "updated" as const };
    const path = libraryPath("/resources", view, "user");
    expect(readLibraryView(path.slice(path.indexOf("?")), "user").q).toBe("a&b=c");
  });
});
