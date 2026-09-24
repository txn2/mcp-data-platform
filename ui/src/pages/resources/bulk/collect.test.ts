import { beforeAll, describe, expect, it } from "vitest";
import { strToU8, zipSync } from "fflate";
import { expandArchives, fflateUnzip, fromDrop, fromFileList, isZip, type DropEntry } from "./collect";
import { MAX_ZIP_BYTES, type SourceFile } from "./plan";

// jsdom's Blob has no arrayBuffer() or text(); every browser the portal
// supports does. Both read through FileReader here, the one reader jsdom has.
beforeAll(() => {
  const read = (b: Blob, how: "buffer" | "text") =>
    new Promise<ArrayBuffer | string>((resolve) => {
      const r = new FileReader();
      r.onload = () => resolve(r.result as ArrayBuffer | string);
      if (how === "buffer") r.readAsArrayBuffer(b);
      else r.readAsText(b);
    });
  if (!Blob.prototype.arrayBuffer) {
    Blob.prototype.arrayBuffer = function () {
      return read(this, "buffer") as Promise<ArrayBuffer>;
    };
  }
  if (!Blob.prototype.text) {
    Blob.prototype.text = function () {
      return read(this, "text") as Promise<string>;
    };
  }
});

function zipFile(name: string, entries: Record<string, string>): File {
  const data = zipSync(Object.fromEntries(Object.entries(entries).map(([k, v]) => [k, strToU8(v)])));
  return new File([data], name, { type: "application/zip" });
}

describe("fromFileList", () => {
  it("keeps a folder pick's relative path and a plain pick's name", () => {
    const picked = new File(["a"], "a.png");
    Object.defineProperty(picked, "webkitRelativePath", { value: "Brand/logos/a.png" });
    const plain = new File(["b"], "b.png");
    expect(fromFileList([picked, plain]).map((s) => s.relPath)).toEqual(["Brand/logos/a.png", "b.png"]);
    expect(fromFileList([])).toEqual([]);
  });
});

describe("expandArchives", () => {
  it("unpacks an archive beneath the directory it sat in", async () => {
    const zip = zipFile("kit.zip", { "logos/": "", "logos/Mark.PNG": "png", "readme.txt": "hi" });
    const out = await expandArchives([{ file: zip, relPath: "Brand/kit.zip" }, { file: new File(["x"], "a.eps"), relPath: "a.eps" }], true);
    expect(out.map((s) => s.relPath).sort()).toEqual(["Brand/logos/Mark.PNG", "Brand/readme.txt", "a.eps"]);
    const mark = out.find((s) => s.relPath.endsWith("Mark.PNG"));
    expect(mark?.file.type).toBe("image/png");
    expect(await mark?.file.text()).toBe("png");
  });

  it("leaves archives whole when unpacking is off, and keeps a source's problem", async () => {
    const zip = zipFile("kit.zip", { "a.txt": "a" });
    const broken: SourceFile = { file: zip, relPath: "kit.zip", problem: "unreadable" };
    expect(await expandArchives([{ file: zip, relPath: "kit.zip" }], false)).toHaveLength(1);
    expect(await expandArchives([broken], true)).toEqual([broken]);
  });

  it("refuses an archive over the cap and one that will not unpack", async () => {
    const big = new File([], "big.zip");
    Object.defineProperty(big, "size", { value: MAX_ZIP_BYTES + 1 });
    const [tooBig] = await expandArchives([{ file: big, relPath: "big.zip" }], true);
    expect(tooBig?.problem).toMatch(/unpacked only up to 1 GB/);

    const junk = new File(["not a zip"], "junk.zip");
    const [bad] = await expandArchives([{ file: junk, relPath: "junk.zip" }], true, fflateUnzip);
    expect(bad?.problem).toMatch(/^The archive could not be unpacked: /);

    const [odd] = await expandArchives([{ file: junk, relPath: "junk.zip" }], true, () => Promise.reject("x"));
    expect(odd?.problem).toBe("The archive could not be unpacked: unreadable.");
  });

  it("recognizes an archive by its extension", () => {
    expect(isZip(new File([], "A.ZIP"))).toBe(true);
    expect(isZip(new File([], "a.zip.png"))).toBe(false);
  });
});

/** A dropped tree: directories answer their children in two batches. */
function fileEntry(fullPath: string, content = "x", fail = false): DropEntry {
  const name = fullPath.split("/").pop() ?? "";
  return {
    isFile: true,
    isDirectory: false,
    name,
    fullPath,
    file: (ok: (f: File) => void, bad?: (e: unknown) => void) => (fail ? bad?.(new Error("gone")) : ok(new File([content], name))),
  } as DropEntry;
}

function dirEntry(fullPath: string, children: DropEntry[]): DropEntry {
  const batches = [children.slice(0, 1), children.slice(1), []];
  return {
    isFile: false,
    isDirectory: true,
    name: fullPath.split("/").pop() ?? "",
    fullPath,
    createReader: () => ({ readEntries: (ok: (e: DropEntry[]) => void) => ok(batches.shift() ?? []) }),
  } as DropEntry;
}

function transfer(entries: (DropEntry | null)[], files: File[] = []): DataTransfer {
  return {
    items: entries.map((e) => ({ webkitGetAsEntry: () => e })),
    files,
  } as unknown as DataTransfer;
}

describe("fromDrop", () => {
  it("walks a dropped folder to every file beneath it", async () => {
    const tree = dirEntry("/Brand", [
      fileEntry("/Brand/a.png"),
      dirEntry("/Brand/logos", [fileEntry("/Brand/logos/b.png"), fileEntry("/Brand/logos/c.png")]),
    ]);
    const out = await fromDrop(transfer([tree, fileEntry("/loose.txt")]));
    expect(out.map((s) => s.relPath).sort()).toEqual(["Brand/a.png", "Brand/logos/b.png", "Brand/logos/c.png", "loose.txt"]);
  });

  it("lists a file the browser could not read, and ignores other entry kinds", async () => {
    const other = { isFile: false, isDirectory: false, name: "x", fullPath: "" } as DropEntry;
    const out = await fromDrop(transfer([fileEntry("/gone.png", "", true), other]));
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ relPath: "gone.png", problem: "The browser could not read this file." });
  });

  it("falls back to the file list when the drop offers no entries", async () => {
    const out = await fromDrop(transfer([null], [new File(["a"], "a.png")]));
    expect(out.map((s) => s.relPath)).toEqual(["a.png"]);
  });
});
