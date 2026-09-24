import { describe, expect, it } from "vitest";
import { isIgnored, MAX_BATCH_FILES, planItems, sendProblem, targetPath, type SourceFile } from "./plan";

const MB = 1024 * 1024;

function src(relPath: string, size = 10, type = ""): SourceFile {
  const name = relPath.split("/").pop() ?? relPath;
  const file = new File([new Uint8Array(0)], name, { type });
  Object.defineProperty(file, "size", { value: size });
  return { file, relPath };
}

describe("isIgnored", () => {
  it("skips operating-system litter only", () => {
    expect(isIgnored(".DS_Store")).toBe(true);
    expect(isIgnored("brand/.git/config")).toBe(true);
    expect(isIgnored("__MACOSX/brand/logo.png")).toBe(true);
    expect(isIgnored("brand/Thumbs.db")).toBe(true);
    expect(isIgnored("brand/logo.png")).toBe(false);
  });
});

describe("planItems", () => {
  it("lists nothing for nothing picked", () => {
    expect(planItems([], MB)).toEqual([]);
  });

  it("files each directory as a folder and names the file as the server will", () => {
    const [item] = planItems([src("Brand Kit/Logos/Acme Logo.PNG", 10, "image/png")], MB);
    expect(item).toMatchObject({
      relDir: "brand-kit/logos",
      filename: "acme-logo.png",
      displayName: "Acme Logo",
      webImage: true,
      problem: null,
    });
  });

  it("drops litter and refuses each file with its own reason", () => {
    const items = planItems(
      [
        src(".DS_Store"),
        src("setup.exe"),
        src("big.png", 2 * MB),
        src("***/a.png"),
        src("ok.eps"),
        src("dup/Logo.png"),
        src("dup/logo.png"),
      ],
      MB,
    );
    expect(items.map((i) => i.problem)).toEqual([
      "Files ending in .exe are not accepted.",
      "The file is larger than the 1 MB limit.",
      'The folder "***" has no letters or digits to name a resource folder with.',
      null,
      null,
      "Another file in this batch has the same folder and file name.",
    ]);
    expect(items[3]?.webImage).toBe(false);
  });

  it("keeps a source's own problem", () => {
    const [item] = planItems([{ ...src("brand.zip"), problem: "The archive could not be unpacked: bad." }], MB);
    expect(item?.problem).toBe("The archive could not be unpacked: bad.");
  });

  it("refuses files past the batch cap", () => {
    const many = Array.from({ length: MAX_BATCH_FILES + 1 }, (_, i) => src(`f${i}.png`));
    const items = planItems(many, MB);
    expect(items[MAX_BATCH_FILES - 1]?.problem).toBeNull();
    expect(items[MAX_BATCH_FILES]?.problem).toMatch(/at most 5,000 files/);
  });
});

describe("targetPath and sendProblem", () => {
  it("joins the base folder and the item's folders", () => {
    expect(targetPath("brand", "")).toBe("brand");
    expect(targetPath("brand", "logos")).toBe("brand/logos");
    expect(targetPath("", "logos")).toBe("logos");
  });

  it("asks the folder, name and description at send time", () => {
    const [item] = planItems([src("logos/a.png")], MB);
    if (!item) throw new Error("no item");
    expect(sendProblem(item, "brand", "d")).toBeNull();
    expect(sendProblem(item, "Brand!", "d")).toMatch(/lowercase letters/);
    expect(sendProblem({ ...item, displayName: "  " }, "brand", "d")).toBe("A display name is required.");
    expect(sendProblem({ ...item, displayName: "x".repeat(201) }, "brand", "d")).toMatch(/at most 200/);
    expect(sendProblem(item, "brand", "x".repeat(2001))).toMatch(/at most 2000/);
    expect(sendProblem({ ...item, problem: "no" }, "brand", "d")).toBe("no");
  });
});
