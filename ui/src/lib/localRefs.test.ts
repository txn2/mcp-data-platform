import { describe, expect, it } from "vitest";
import { withLocalRefs } from "./localRefs";

describe("withLocalRefs", () => {
  it("points every reference route URL at the page's own origin", () => {
    expect(
      withLocalRefs(`<video src="http://localhost:28080/portal/refs/a1/tok1"></video><img src='https://portal.example.com/portal/refs/a1/tok2'>`),
    ).toBe(`<video src="/portal/refs/a1/tok1"></video><img src='/portal/refs/a1/tok2'>`);
  });

  it("leaves every other address alone", () => {
    const doc = `<a href="https://example.com/portal/view/x">x</a><img src="https://cdn.example.com/a.png"><video src="/portal/refs/a/b"></video>`;
    expect(withLocalRefs(doc)).toBe(doc);
  });
});
