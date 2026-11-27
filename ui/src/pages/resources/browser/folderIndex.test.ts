import { describe, expect, it } from "vitest";
import type { Folder } from "@/api/resources/types";
import { folderEntries, folderIndex, hasSubfolders } from "./model";

/**
 * counted wraps a folder array so every read of one of its elements is
 * counted: what a render of the tree touches.
 */
function counted(folders: Folder[]) {
  const reads = { n: 0 };
  const proxy = new Proxy(folders, {
    get(target, prop, receiver) {
      if (typeof prop === "string" && /^\d+$/.test(prop)) reads.n++;
      return Reflect.get(target, prop, receiver);
    },
  });
  return { proxy, reads };
}

/** A library of 20,000 folders: 40 days, each holding 500 message folders. */
function bigLibrary(): Folder[] {
  const out: Folder[] = [{ path: "mail", count: 40_000 }];
  for (let d = 0; d < 40; d++) {
    out.push({ path: `mail/day-${d}`, count: 1000 });
    for (let m = 0; m < 500; m++) out.push({ path: `mail/day-${d}/m-${m}`, count: 2 });
  }
  return out;
}

describe("the folder index (#2837)", () => {
  it("is built once per answer, and a level's rows touch no folder outside it", () => {
    const { proxy, reads } = counted(bigLibrary());
    folderIndex(proxy);
    const built = reads.n;
    expect(built).toBeGreaterThan(20_000);
    reads.n = 0;

    // What the tree does to draw one open day: its rows, and for each row
    // whether it opens.
    const rows = folderEntries(proxy, "mail/day-7");
    for (const r of rows) {
      if (r.kind === "folder") hasSubfolders(proxy, r.path, r.subfolders);
    }
    expect(rows).toHaveLength(500);
    expect(reads.n).toBe(0);
    expect(folderIndex(proxy)).toBe(folderIndex(proxy));
  });

  it("answers a level read by what the level says about each folder", () => {
    const level: Folder[] = [
      { path: "mail", count: 4, subfolders: 2 },
      { path: "mail/a", count: 2, subfolders: 1 },
      { path: "mail/b", count: 2, subfolders: 0 },
    ];
    const rows = folderEntries(level, "mail");
    expect(rows.map((r) => r.name)).toEqual(["a", "b"]);
    expect(rows.map((r) => (r.kind === "folder" ? hasSubfolders(level, r.path, r.subfolders) : null))).toEqual([true, false]);
    expect(hasSubfolders(level, "mail")).toBe(true);
    expect(hasSubfolders([{ path: "", count: 0 }], "")).toBe(false);
  });
});
