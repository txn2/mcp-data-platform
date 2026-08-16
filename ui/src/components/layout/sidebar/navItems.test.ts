import { describe, it, expect } from "vitest";
import { adminNavItems, isNavActive, portalNavItems } from "./navItems";

describe("isNavActive", () => {
  it("lights Assets across the collections and viewer routes it owns", () => {
    for (const route of [
      "/",
      "/collections",
      "/collections/col-1",
      "/assets/ast-1",
      "/shared/assets/ast-1",
    ]) {
      expect(isNavActive("/", route), route).toBe(true);
    }
  });

  it("lights admin Assets across the admin collection routes (#1292)", () => {
    for (const route of [
      "/admin/assets",
      "/admin/assets/ast-1",
      "/admin/collections",
      "/admin/collections/col-1",
    ]) {
      expect(isNavActive("/admin/assets", route), route).toBe(true);
    }
    expect(isNavActive("/admin/assets", "/admin/resources")).toBe(false);
  });

  it("does not light Assets for a section that merely starts with a slash", () => {
    expect(isNavActive("/", "/resources")).toBe(false);
  });

  it("keeps Knowledge lit on its addressable page routes", () => {
    expect(isNavActive("/knowledge", "/knowledge")).toBe(true);
    expect(isNavActive("/knowledge", "/knowledge/pages")).toBe(true);
    expect(isNavActive("/knowledge", "/knowledge/pages/kp-1")).toBe(true);
  });

  it("matches the sections that own no deeper routes exactly", () => {
    expect(isNavActive("/admin", "/admin")).toBe(true);
    // /admin/tools is the Tools item's route, not the Dashboard's.
    expect(isNavActive("/admin", "/admin/tools")).toBe(false);
    expect(isNavActive("/prompts", "/prompts/pr-1")).toBe(false);
  });

  it("still matches when the route carries a query string or hash", () => {
    // The Tools deep link (#859): an unstripped "?selected=..." used to match
    // no item at all, so the rail lost its highlight until the next refresh.
    expect(isNavActive("/admin/tools", "/admin/tools?selected=x&tab=tryit")).toBe(true);
    expect(isNavActive("/knowledge", "/knowledge#insights")).toBe(true);
  });

  it("compares a hash-addressed item against the whole path", () => {
    expect(isNavActive("/admin/settings#smtp", "/admin/settings#smtp")).toBe(true);
    expect(isNavActive("/admin/settings#smtp", "/admin/settings#alerts")).toBe(false);
  });

  it("lights exactly one item for any route the rail offers", () => {
    const items = [...portalNavItems, ...adminNavItems];
    for (const item of items) {
      const lit = items.filter((i) => isNavActive(i.path, item.path));
      expect(lit.map((i) => i.path), item.path).toEqual([item.path]);
    }
  });
});
