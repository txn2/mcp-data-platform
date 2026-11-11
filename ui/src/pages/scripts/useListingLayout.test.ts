import { describe, it, expect } from "vitest";
import { layoutFor, LAYOUT_WIDTHS } from "./useListingLayout";

// #2601: the listing's layout follows the width it has.
describe("layoutFor", () => {
  it("is a list on a phone, whatever the width", () => {
    expect(layoutFor(2000, true)).toBe("list");
  });

  it("is the widest table before anything is measured", () => {
    expect(layoutFor(undefined, false)).toBe("wide");
    expect(layoutFor(0, false)).toBe("wide");
  });

  it("drops columns as the width narrows, then lists", () => {
    expect(layoutFor(LAYOUT_WIDTHS.wide, false)).toBe("wide");
    expect(layoutFor(LAYOUT_WIDTHS.wide - 1, false)).toBe("medium");
    expect(layoutFor(940, false)).toBe("medium");
    expect(layoutFor(LAYOUT_WIDTHS.medium - 1, false)).toBe("narrow");
    expect(layoutFor(LAYOUT_WIDTHS.narrow - 1, false)).toBe("list");
  });
});
