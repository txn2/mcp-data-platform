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

  // #2612: the Sites list chooses its layout by the same rule at its own widths.
  it("reads the widths a listing passes in place of the Automations ones", () => {
    const widths = { wide: 1000, medium: 680, narrow: 480 };
    expect(layoutFor(1000, false, widths)).toBe("wide");
    expect(layoutFor(999, false, widths)).toBe("medium");
    expect(layoutFor(679, false, widths)).toBe("narrow");
    expect(layoutFor(479, false, widths)).toBe("list");
  });
});
