import { describe, it, expect } from "vitest";
import { shareTokenFromSearch, sharedPagePath } from "./shareLink";

const TOKEN = "a".repeat(64);

describe("shareTokenFromSearch (#1473)", () => {
  it("reads the token the share redirect carried", () => {
    expect(shareTokenFromSearch(`?share=${TOKEN}`)).toBe(TOKEN);
  });

  it("reads it alongside other parameters", () => {
    expect(shareTokenFromSearch(`?tab=history&share=${TOKEN}`)).toBe(TOKEN);
  });

  it("has no token for a page reached any other way", () => {
    expect(shareTokenFromSearch("")).toBeNull();
    expect(shareTokenFromSearch("?tab=history")).toBeNull();
    expect(shareTokenFromSearch("?share=")).toBeNull();
  });

  it("refuses a value that is not the shape the server issues", () => {
    // A link is built from this. A value that is not a hex token came from
    // somewhere other than a share redirect, and pointing at whatever it names
    // is what a link built from an arbitrary query value does.
    expect(shareTokenFromSearch("?share=//evil.example.com")).toBeNull();
    expect(shareTokenFromSearch("?share=../../admin")).toBeNull();
    expect(shareTokenFromSearch("?share=https://evil.example.com")).toBeNull();
    expect(shareTokenFromSearch("?share=NOTHEX")).toBeNull();
    expect(shareTokenFromSearch("?share=abc")).toBeNull();
  });
});

describe("sharedPagePath (#1473)", () => {
  it("asks the share route for the public page itself", () => {
    // Without public=1 the server would send the reader who clicked the link
    // back to the portal page they clicked it from.
    expect(sharedPagePath(TOKEN)).toBe(`/portal/view/${TOKEN}?public=1`);
  });
});
