import { describe, expect, it } from "vitest";
import { AVAILABLE_KINDS, KIND_LABELS, kindLabel } from "./constants";

describe("connection kind names (#2551)", () => {
  it("names every kind the create form offers", () => {
    for (const kind of AVAILABLE_KINDS) {
      expect(KIND_LABELS[kind], `no label for ${kind}`).toBeTruthy();
    }
  });

  it("uses the agreed names", () => {
    expect(AVAILABLE_KINDS.map(kindLabel)).toEqual(["Trino", "S3", "MCP", "API", "GraphQL", "AppDB"]);
  });

  it("shows a kind it does not know as its identifier", () => {
    expect(kindLabel("ldap")).toBe("ldap");
  });
});
