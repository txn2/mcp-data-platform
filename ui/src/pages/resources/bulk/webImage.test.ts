import { describe, expect, it } from "vitest";
import { extensionOf, fileIsWebImage, isWebImage, typeFromName } from "./webImage";

describe("web image classification", () => {
  it("names the types a document can draw and nothing else", () => {
    for (const t of ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/svg+xml"]) {
      expect(isWebImage(t)).toBe(true);
    }
    expect(isWebImage("IMAGE/PNG; charset=binary")).toBe(true);
    expect(isWebImage("application/postscript")).toBe(false);
    expect(isWebImage("application/pdf")).toBe(false);
    expect(isWebImage("image/tiff")).toBe(false);
    expect(isWebImage("")).toBe(false);
  });

  it("reads an untyped file by its extension", () => {
    expect(typeFromName("Logo.JPG")).toBe("image/jpeg");
    expect(typeFromName("mark.svg")).toBe("image/svg+xml");
    expect(typeFromName("logo.eps")).toBe("");
    expect(typeFromName("README")).toBe("");
    expect(extensionOf("a.tar.gz")).toBe("gz");
    expect(fileIsWebImage({ name: "a.webp", type: "" })).toBe(true);
    expect(fileIsWebImage({ name: "a.bin", type: "image/gif" })).toBe(true);
    expect(fileIsWebImage({ name: "a.eps", type: "application/postscript" })).toBe(false);
  });
});
