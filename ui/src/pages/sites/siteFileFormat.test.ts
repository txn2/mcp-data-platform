import { describe, expect, it } from "vitest";
import type { SiteFile } from "@/api/portal/hooks/sites";
import { isCompiled, isRendered, isText } from "./siteFileFormat";

const file = (path: string, content_type = "text/plain; charset=utf-8") => ({ path, content_type, sha256: "", size: 1 }) as SiteFile;

describe("what a site's source does at publish (#2774)", () => {
  it("compiles when the root holds _build.json, and renders when it has layouts", () => {
    expect(isCompiled([file("_build.json"), file("app.tsx")])).toBe(true);
    expect(isCompiled([file("admin/_build.json"), file("app.tsx")])).toBe(false);
    expect(isRendered([file("_build.json")])).toBe(false);
    expect(isRendered([file("_layouts/base.html")])).toBe(true);
  });

  it("opens TypeScript source in the editor", () => {
    expect(isText(file("Table.tsx"))).toBe(true);
    expect(isText(file("logo.png", "image/png"))).toBe(false);
  });
});
