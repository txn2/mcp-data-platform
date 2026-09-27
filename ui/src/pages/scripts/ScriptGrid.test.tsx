import { describe, expect, it } from "vitest";
import { scriptTileSrc } from "./ScriptGrid";

describe("scriptTileSrc", () => {
  it("addresses a script's tile by the version it should show, light or dark", () => {
    expect(scriptTileSrc("s1", 4, false)).toBe("/api/v1/portal/scripts/s1/thumbnail?v=4");
    expect(scriptTileSrc("s1", 4, true)).toBe("/api/v1/portal/scripts/s1/thumbnail?v=4&variant=dark");
  });
});
