import { describe, expect, it } from "vitest";
import { BuildWatch, readPageBuild, type BuildStore } from "./buildWatch";

// A tab's sessionStorage, kept across the reloads one test walks through.
function tab(): BuildStore {
  const m = new Map<string, string>();
  return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => void m.set(k, v) };
}

describe("BuildWatch (#2727)", () => {
  it("a page on build A that hears of build B reloads at the next navigation, once", () => {
    const store = tab();
    const page = new BuildWatch("v1+a", store);
    page.observe("v1+a");
    expect(page.pendingBuild()).toBeNull();
    page.observe("v2+b");
    expect(page.pendingBuild()).toBe("v2+b");
    expect(page.reloadOnNavigate()).toBe(true);
    expect(page.reloadOnNavigate()).toBe(false);
  });

  it("after moving to B, an answer from a replica still on A changes nothing", () => {
    const store = tab();
    new BuildWatch("v1+a", store).observe("v2+b");
    const reloaded = new BuildWatch("v2+b", store);
    reloaded.observe("v1+a");
    expect(reloaded.pendingBuild()).toBeNull();
    expect(reloaded.reloadOnNavigate()).toBe(false);
  });

  it("a reload that lands back on A never reloads again for B by itself", () => {
    const store = tab();
    const first = new BuildWatch("v1+a", store);
    first.observe("v2+b");
    expect(first.reloadOnNavigate()).toBe(true);
    // index.html came from an old replica: the page is A again.
    const again = new BuildWatch("v1+a", store);
    again.observe("v2+b");
    expect(again.pendingBuild()).toBe("v2+b");
    expect(again.alreadyTried()).toBe(true);
    expect(again.reloadOnNavigate()).toBe(false);
  });

  it("a third build after B reloads once more", () => {
    const store = tab();
    const a = new BuildWatch("v1+a", store);
    a.observe("v2+b");
    a.reloadOnNavigate();
    const b = new BuildWatch("v2+b", store);
    b.observe("v3+c");
    expect(b.reloadOnNavigate()).toBe(true);
  });

  it("a page that names no build, or an answer that names none, does nothing", () => {
    const none = new BuildWatch("", tab());
    none.observe("v2+b");
    expect(none.pendingBuild()).toBeNull();
    const page = new BuildWatch("v1+a", tab());
    page.observe(null);
    expect(page.pendingBuild()).toBeNull();
  });

  it("tells its listeners once per new build", () => {
    const page = new BuildWatch("v1+a", tab());
    let told = 0;
    page.subscribe(() => told++);
    page.observe("v2+b");
    page.observe("v2+b");
    expect(told).toBe(1);
  });

  it("works with no storage at all", () => {
    const page = new BuildWatch("v1+a", null);
    page.observe("v2+b");
    expect(page.reloadOnNavigate()).toBe(true);
    expect(page.reloadOnNavigate()).toBe(false);
  });

  it("reads the build the served index.html names", () => {
    const doc = document.implementation.createHTMLDocument("x");
    expect(readPageBuild(doc)).toBe("");
    const meta = doc.createElement("meta");
    meta.name = "mcp-platform-build";
    meta.content = "v1.166.2+8b91e56";
    doc.head.appendChild(meta);
    expect(readPageBuild(doc)).toBe("v1.166.2+8b91e56");
  });
});
