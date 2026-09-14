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

  it("does not let the reference route light the APIs item, or the reverse", () => {
    // "/admin/apis" and "/admin/api-reference" are two sections whose paths
    // share a prefix (#1742). Neither is a route beneath the other.
    expect(isNavActive("/admin/apis", "/admin/api-reference")).toBe(false);
    expect(isNavActive("/admin/api-reference", "/admin/apis")).toBe(false);
    expect(isNavActive("/admin/api-catalogs", "/admin/api-reference")).toBe(false);
  });

  it("lights exactly one item for any route the rail offers", () => {
    const items = [...portalNavItems, ...adminNavItems];
    for (const item of items) {
      const lit = items.filter((i) => isNavActive(i.path, item.path));
      expect(lit.map((i) => i.path), item.path).toEqual([item.path]);
    }
  });
});

describe("the admin rail", () => {
  it("keeps Dashboard first and everything after it alphabetized", () => {
    // The order is what a reader scans, so a new section that lands in the
    // middle of the list is the failure this holds against.
    const [first, ...rest] = adminNavItems;
    expect(first?.label).toBe("Dashboard");
    const labels = rest.map((i) => i.label.toLowerCase());
    expect(labels).toEqual([...labels].sort());
  });

  it("offers the served API reference, in the admin section alone", () => {
    const item = adminNavItems.find((i) => i.path === "/admin/api-reference");
    expect(item?.label).toBe("API Reference");
    // The portal rail is every reader's; the reference is reached from the
    // administrator's section (#1742).
    expect(portalNavItems.some((i) => i.path.startsWith("/admin"))).toBe(false);
  });
});
