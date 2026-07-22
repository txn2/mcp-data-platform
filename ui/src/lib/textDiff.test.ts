import { describe, expect, it } from "vitest";
import { diffLines, diffStats } from "./textDiff";

describe("diffLines", () => {
  it("reports identical texts as all-same", () => {
    const d = diffLines("a\nb", "a\nb");
    expect(d).toEqual([
      { kind: "same", text: "a" },
      { kind: "same", text: "b" },
    ]);
    expect(diffStats(d)).toEqual({ added: 0, removed: 0 });
  });

  it("detects an in-place line change as remove + add", () => {
    const d = diffLines("a\nb\nc", "a\nB\nc");
    expect(d).toEqual([
      { kind: "same", text: "a" },
      { kind: "removed", text: "b" },
      { kind: "added", text: "B" },
      { kind: "same", text: "c" },
    ]);
    expect(diffStats(d)).toEqual({ added: 1, removed: 1 });
  });

  it("detects pure additions and removals", () => {
    expect(diffLines("a", "a\nb")).toEqual([
      { kind: "same", text: "a" },
      { kind: "added", text: "b" },
    ]);
    expect(diffLines("a\nb", "b")).toEqual([
      { kind: "removed", text: "a" },
      { kind: "same", text: "b" },
    ]);
  });

  it("handles fully disjoint texts", () => {
    const d = diffLines("x\ny", "p\nq");
    expect(diffStats(d)).toEqual({ added: 2, removed: 2 });
  });

  it("handles empty inputs", () => {
    expect(diffLines("", "")).toEqual([{ kind: "same", text: "" }]);
    expect(diffLines("", "a")).toEqual([
      { kind: "removed", text: "" },
      { kind: "added", text: "a" },
    ]);
  });

  it("keeps surrounding context stable around a moved block", () => {
    const from = "intro\nstep one\nstep two\noutro";
    const to = "intro\nstep two\nstep one\noutro";
    const d = diffLines(from, to);
    expect(d[0]).toEqual({ kind: "same", text: "intro" });
    expect(d[d.length - 1]).toEqual({ kind: "same", text: "outro" });
    const stats = diffStats(d);
    expect(stats.added).toBe(1);
    expect(stats.removed).toBe(1);
  });
});
