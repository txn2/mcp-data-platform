import { describe, it, expect } from "vitest";
import { personaHref, shareText } from "./topcallers";

describe("shareText", () => {
  it("floors to one decimal so a caller short of the whole never reads as all of it", () => {
    expect(shareText(0.9999)).toBe("99.9%");
    expect(shareText(1)).toBe("100.0%");
    expect(shareText(0.994)).toBe("99.4%");
    expect(shareText(0)).toBe("0.0%");
  });
});

describe("personaHref", () => {
  it("opens the persona editor on the named persona", () => {
    expect(personaHref("crm sync/1")).toBe("/portal/admin/personas?persona=crm%20sync%2F1");
  });
});
