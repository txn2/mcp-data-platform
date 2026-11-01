import { describe, expect, it } from "vitest";
import { botCheckSecretInput, isBotCheckSecret, newSecretProblems } from "./botCheckSecret";

describe("botCheckSecret (#2388)", () => {
  it("refuses a bad name, a taken name and a short key", () => {
    expect(newSecretProblems({ name: "ok-name", key: "long-enough" }, ["other"])).toEqual([]);
    expect(newSecretProblems({ name: "Bad Name", key: "long-enough" }, [])).toEqual([expect.stringContaining("lower case")]);
    expect(newSecretProblems({ name: "taken", key: "long-enough" }, ["taken"])).toEqual([
      expect.stringContaining("A secret named taken already exists"),
    ]);
    expect(newSecretProblems({ name: "ok", key: "abc" }, [])).toEqual([expect.stringContaining("at least 6")]);
  });

  it("stores the key as a value usable by bot checks and no persona", () => {
    expect(botCheckSecretInput("forms", "k-123456")).toEqual({
      description: "Provider secret key for the forms bot check.",
      kind: "value",
      value: "k-123456",
      allow_connections: ["bot_check"],
      allow_personas: [],
    });
  });

  it("offers only value secrets listing bot_check and no personas", () => {
    expect(isBotCheckSecret({ allow_connections: ["bot_check"], allow_personas: [] })).toBe(true);
    expect(isBotCheckSecret({ kind: "totp", allow_connections: ["bot_check"], allow_personas: [] })).toBe(false);
    expect(isBotCheckSecret({ allow_connections: ["bot_check"], allow_personas: ["analyst"] })).toBe(false);
    expect(isBotCheckSecret({ allow_connections: ["crm"], allow_personas: [] })).toBe(false);
  });
});
