import { describe, it, expect, afterEach } from "vitest";
import { buildLoginURL } from "./loginUrl";

describe("buildLoginURL (#710)", () => {
  const originalLocation = window.location;

  function mockLocation(pathname: string, search = "", hash = ""): void {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { pathname, search, hash },
    });
  }

  afterEach(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
  });

  it("captures the current path as return_to", () => {
    mockLocation("/assets/asset-001");
    expect(buildLoginURL()).toBe(
      "/portal/auth/login?return_to=" + encodeURIComponent("/assets/asset-001"),
    );
  });

  it("captures path, query, and hash together", () => {
    mockLocation("/knowledge", "?tab=pages", "#section-2");
    expect(buildLoginURL()).toBe(
      "/portal/auth/login?return_to=" +
        encodeURIComponent("/knowledge?tab=pages#section-2"),
    );
  });

  it("encodes the return_to so a query string does not split into a second top-level param", () => {
    mockLocation("/collections/c1/items", "?q=a&b=c");
    const url = buildLoginURL();
    // Exactly one "?" (the login endpoint's); the captured "&" is escaped.
    expect(url.indexOf("?")).toBe(url.lastIndexOf("?"));
    expect(url).not.toContain("&b=c");
    const encoded = url.split("return_to=")[1] ?? "";
    expect(decodeURIComponent(encoded)).toBe("/collections/c1/items?q=a&b=c");
  });

  it("falls back to a bare login URL when the path is pathologically long", () => {
    // A return_to long enough to risk overflowing the signed state cookie is
    // dropped so the server lands the user on the default page rather than
    // setting a cookie the browser would silently discard.
    mockLocation("/x" + "a".repeat(2000));
    expect(buildLoginURL()).toBe("/portal/auth/login");
  });

  it("keeps a return_to that is just under the cap", () => {
    mockLocation("/" + "a".repeat(1000));
    expect(buildLoginURL()).toContain("?return_to=");
  });
});
