import { describe, it, expect } from "vitest";
import { parseEmailAddress } from "./emailAddress";

describe("parseEmailAddress", () => {
  it("passes a bare address through, lowercased", () => {
    expect(parseEmailAddress("user@example.com")).toBe("user@example.com");
    expect(parseEmailAddress("USER@Example.COM")).toBe("user@example.com");
    expect(parseEmailAddress("  user@example.com  ")).toBe("user@example.com");
  });

  it("extracts the address from the display-name form mail clients copy", () => {
    expect(parseEmailAddress("Example User <User@Example.com>")).toBe("user@example.com");
    expect(parseEmailAddress("<user@example.com>")).toBe("user@example.com");
    expect(parseEmailAddress('"Doe, Jane" <jane.doe@example.com>')).toBe("jane.doe@example.com");
    expect(parseEmailAddress("Ops <ops+alerts@example.com>")).toBe("ops+alerts@example.com");
  });

  it("rejects input that names no single routable address", () => {
    for (const input of [
      "",
      "   ",
      "Example User",
      "user@localhost",
      "@example.com",
      "user@",
      "a@example.com, b@example.com",
      "Example User <user@example.com",
      "<>",
      "user@example.",
      "user@.com",
      `${"a".repeat(250)}@example.com`,
    ]) {
      expect(parseEmailAddress(input), input).toBeNull();
    }
  });
});
