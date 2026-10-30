import { describe, expect, it } from "vitest";
import { mayLoad, refsOnlyLoader } from "./vegaLoader";

describe("mayLoad", () => {
  const origin = "https://portal.example.com";
  it("admits only a declared reference, always on this origin", () => {
    expect(mayLoad("/portal/refs/ast_1/tok", origin)?.href).toBe(`${origin}/portal/refs/ast_1/tok`);
    expect(mayLoad(`${origin}/portal/refs/ast_1/tok?v=2`, origin)?.href).toBe(`${origin}/portal/refs/ast_1/tok?v=2`);
    // The public base URL the rewrite wrote, or any other host: the path is
    // kept and loaded from the page's own origin.
    expect(mayLoad("http://localhost:28080/portal/refs/a/b", origin)?.href).toBe(`${origin}/portal/refs/a/b`);
    expect(mayLoad("https://evil.example.com/portal/refs/a/b", origin)?.origin).toBe(origin);
    expect(mayLoad("data:text/csv,/portal/refs/a", origin)).toBeNull();
    expect(mayLoad("/api/v1/portal/assets", origin)).toBeNull();
    expect(mayLoad("data/x.csv", origin)).toBeNull();
    expect(mayLoad("javascript:alert(1)", origin)).toBeNull();
    expect(mayLoad("http://[bad", origin)).toBeNull();
  });
});

describe("refsOnlyLoader", () => {
  it("follows no link and loads nothing but a reference", async () => {
    const loader = refsOnlyLoader();
    await expect(loader.sanitize("/portal/refs/a/b", { context: "href" })).rejects.toThrow(/not followed/);
    await expect(loader.sanitize("https://example.com/d.csv", { context: "dataflow", response: "csv" })).rejects.toThrow(/refused/);
    await expect(loader.sanitize("/portal/refs/a/b", { context: "image" })).resolves.toEqual({
      href: `${window.location.origin}/portal/refs/a/b`,
    });
    // load goes through the replaced gate.
    await expect(loader.load("https://example.com/d.csv")).rejects.toThrow(/refused/);
  });
});
