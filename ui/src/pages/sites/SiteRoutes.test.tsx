import { describe, expect, it, vi } from "vitest";
import { isKnownRoute, SITE_TABS } from "@/lib/portalRoutes";
import { SiteRoutes } from "./SiteRoutes";

// #2622: the shell decides "not found" from its own list of paths, and the
// Sites section rendered four addresses that list did not hold, so the
// not-found card drew above a working page. The two now read one list; these
// hold every address the section renders to a page the shell knows, and an
// address it has no page for to neither.

const ADDRESSES = [
  "/sites/marketing",
  ...SITE_TABS.map((tab) => `/sites/marketing/${tab}`),
  "/sites/marketing/releases/12",
  "/sites/marketing/file/index.html",
  "/sites/marketing/file/blog/2026/post.md",
  "/sites/marketing/newfile/about.html",
];

describe("SiteRoutes", () => {
  it.each(ADDRESSES)("%s is a page the shell knows and the section renders", (route) => {
    expect(isKnownRoute(route)).toBe(true);
    expect(SiteRoutes({ route, onNavigate: vi.fn() })).not.toBeNull();
  });

  it("the Files and Media tabs and the file editor are among them", () => {
    for (const route of ["/sites/x/files", "/sites/x/media", "/sites/x/file/a.html", "/sites/x/newfile/a.html"]) {
      expect(isKnownRoute(route)).toBe(true);
    }
  });

  it.each(["/sites/marketing/nope", "/sites/marketing/releases/latest", "/sites/marketing/file/", "/sites/marketing/files/extra"])(
    "%s is not found, and the section renders nothing under the not-found page",
    (route) => {
      expect(isKnownRoute(route)).toBe(false);
      expect(SiteRoutes({ route, onNavigate: vi.fn() })).toBeNull();
    },
  );
});
