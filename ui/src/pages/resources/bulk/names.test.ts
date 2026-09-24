import { describe, expect, it } from "vitest";
import { describe as expand, displayNameFor, folderSegment, sanitizeFilename } from "./names";

describe("sanitizeFilename", () => {
  it("files a name the way the server does", () => {
    expect(sanitizeFilename("Acme Logo (Color).PNG")).toEqual({ filename: "acme-logo-color.png" });
    expect(sanitizeFilename("dir/sub\\Café.jpg")).toEqual({ filename: "café.jpg" });
    expect(sanitizeFilename("  report_v2.pdf ")).toEqual({ filename: "report_v2.pdf" });
  });

  it("refuses what the server refuses", () => {
    expect(sanitizeFilename("").problem).toMatch(/no name/);
    expect(sanitizeFilename("dir/").problem).toMatch(/no name/);
    expect(sanitizeFilename("()").problem).toMatch(/no characters/);
    expect(sanitizeFilename("(.)").problem).toMatch(/no characters/);
    expect(sanitizeFilename("setup.EXE").problem).toBe("Files ending in .exe are not accepted.");
    expect(sanitizeFilename("run.sh").problem).toMatch(/\.sh/);
    expect(sanitizeFilename("Makefile")).toEqual({ filename: "makefile" });
  });
});

describe("displayNameFor", () => {
  it("drops the extension and keeps the writing", () => {
    expect(displayNameFor("Acme Logo (Color).png")).toBe("Acme Logo (Color)");
    expect(displayNameFor("logos/Mark.svg")).toBe("Mark");
    expect(displayNameFor(".png")).toBe(".png");
    expect(displayNameFor("noext")).toBe("noext");
    expect(Array.from(displayNameFor("x".repeat(300) + ".png"))).toHaveLength(200);
  });
});

describe("folderSegment", () => {
  it("turns a directory name into a folder name", () => {
    expect(folderSegment("Brand Assets")).toBe("brand-assets");
    expect(folderSegment("  Logos__Reversed!! ")).toBe("logos-reversed");
    expect(folderSegment("2024")).toBe("f-2024");
    expect(folderSegment("a".repeat(40))).toHaveLength(31);
    expect(folderSegment("abcdefghij-abcdefghij-abcdefghi-x")).toBe("abcdefghij-abcdefghij-abcdefghi");
    expect(folderSegment("***")).toBeNull();
    expect(folderSegment("日本")).toBeNull();
  });
});

describe("describe", () => {
  it("expands every {name}", () => {
    expect(expand("{name} logo, {name} variant", "Acme")).toBe("Acme logo, Acme variant");
    expect(expand("Product shot", "Acme")).toBe("Product shot");
    expect(expand("   ", "Acme")).toBe("Acme");
  });
});
