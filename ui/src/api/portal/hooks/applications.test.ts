import { describe, it, expect } from "vitest";
import { attachmentName, bundleNames } from "./applications";

describe("application helpers", () => {
  it("lists every name a bundle refers to, sorted and once", () => {
    expect(
      bundleNames({
        names: {
          connections: ["orders", "acme-shop"],
          secrets: ["stripe-key"],
          sources: ["orders"],
          bot_checks: [],
        },
      }),
    ).toEqual(["acme-shop", "orders", "stripe-key"]);
  });

  it("reads a bundle with no names as none", () => {
    expect(bundleNames({})).toEqual([]);
  });

  it("reads the filename an attachment names", () => {
    expect(attachmentName('attachment; filename="shop-release-3.json"')).toBe("shop-release-3.json");
    expect(attachmentName("attachment; filename=shop.json")).toBe("shop.json");
    expect(attachmentName(null)).toBeUndefined();
  });
});
