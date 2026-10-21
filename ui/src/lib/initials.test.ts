import { describe, it, expect } from "vitest";
import { initials } from "./initials";

describe("initials", () => {
  it.each([
    ["ada.lovelace@example.com", "AL"],
    ["grace@example.com", "GR"],
    ["j_doe-smith@example.com", "JD"],
    ["apikey:acme", "AP"],
    ["", "?"],
    ["@example.com", "?"],
  ])("%s -> %s", (identity, want) => {
    expect(initials(identity)).toBe(want);
  });
});
