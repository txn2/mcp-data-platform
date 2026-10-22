import { describe, it, expect } from "vitest";
import { sharedLine } from "./sharedLine";

describe("sharedLine (#2089)", () => {
  it("names the persona an item reached the reader through", () => {
    expect(sharedLine({ shared_by: "alice@example.com" })).toBe("Shared by alice@example.com");
    expect(sharedLine({ shared_by: "alice@example.com", via_persona: "finance" })).toBe(
      "Shared by alice@example.com with finance",
    );
  });
});
