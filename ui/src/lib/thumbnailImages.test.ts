import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

import { flattenImages } from "./thumbnailImages";

// jsdom neither loads images nor rasterizes a canvas, so both are stood in for:
// a source assignment settles on the next microtask with whatever verdict the
// test asked for, and a canvas answers with a recorded draw. That a document
// holding an SVG with only a viewBox then captures WHOLE is held by
// e2e/thumbnails/capture.spec.ts against a real browser (#1771).

/** What a stubbed canvas was asked to draw, in order. */
const draws: { source: unknown; width: number; height: number }[] = [];

/** Addresses whose load fails, by exact src. */
const failing = new Set<string>();

/** Whether toDataURL should throw, as a tainted canvas does. */
let tainted = false;

/** Whether getContext should answer null, as a document with no rasterizer does. */
let contextless = false;

const originalSrc = Object.getOwnPropertyDescriptor(HTMLImageElement.prototype, "src");

beforeEach(() => {
  draws.length = 0;
  failing.clear();
  tainted = false;
  contextless = false;
  document.body.innerHTML = "";

  Object.defineProperty(HTMLImageElement.prototype, "src", {
    configurable: true,
    get(this: HTMLImageElement) {
      return this.getAttribute("src") ?? "";
    },
    set(this: HTMLImageElement, value: string) {
      this.setAttribute("src", value);
      queueMicrotask(() => {
        this.dispatchEvent(new Event(failing.has(value) ? "error" : "load"));
      });
    },
  });

  HTMLCanvasElement.prototype.getContext = vi.fn(function (this: HTMLCanvasElement) {
    if (contextless) return null;
    return {
      drawImage: (source: unknown, _x: number, _y: number, width: number, height: number) => {
        draws.push({ source, width, height });
      },
    };
  }) as unknown as HTMLCanvasElement["getContext"];

  HTMLCanvasElement.prototype.toDataURL = vi.fn(function (this: HTMLCanvasElement) {
    if (tainted) throw new Error("SecurityError: tainted canvas");
    return `data:image/png;base64,${this.width}x${this.height}`;
  }) as unknown as HTMLCanvasElement["toDataURL"];
});

afterEach(() => {
  if (originalSrc) Object.defineProperty(HTMLImageElement.prototype, "src", originalSrc);
});

/** An image of the given rendered size, in a root this returns. */
function tree(
  images: { src: string; width: number; height: number; markup?: string }[],
): HTMLElement {
  const root = document.createElement("div");
  for (const image of images) {
    const holder = document.createElement("div");
    holder.innerHTML = image.markup ?? `<img src="${image.src}" alt="">`;
    const img = holder.querySelector("img")!;
    img.getBoundingClientRect = () =>
      ({ width: image.width, height: image.height }) as DOMRect;
    root.appendChild(holder);
  }
  document.body.appendChild(root);
  return root;
}

describe("flattenImages", () => {
  it("gives an image an intrinsic size equal to its rendered size (#1771)", async () => {
    const root = tree([{ src: "/portal/refs/asset/mark", width: 180, height: 161 }]);

    const flattened = await flattenImages(root);

    expect(flattened).toBe(1);
    // The whole image, drawn into a copy the size it is rendered at: the
    // five-argument drawImage, which is what html2canvas does not do.
    expect(draws).toEqual([{ source: expect.anything(), width: 360, height: 322 }]);
    expect(root.querySelector("img")!.getAttribute("src")).toBe(
      "data:image/png;base64,360x322",
    );
  });

  it("copies through a CORS request, as html2canvas loads", async () => {
    const root = tree([{ src: "https://example.com/refs/mark", width: 100, height: 100 }]);

    await flattenImages(root);

    const source = draws[0]!.source as HTMLImageElement;
    expect(source.crossOrigin).toBe("anonymous");
    // The copy, not the element in the tree: the element carries no CORS
    // request and drawing it would taint the canvas.
    expect(source).not.toBe(root.querySelector("img"));
  });

  it("draws the element itself when the CORS request is refused", async () => {
    failing.add("https://example.com/no-cors");
    const root = tree([{ src: "https://example.com/no-cors", width: 40, height: 40 }]);
    const img = root.querySelector("img")!;
    Object.defineProperty(img, "complete", { value: true });
    Object.defineProperty(img, "naturalWidth", { value: 300 });

    expect(await flattenImages(root)).toBe(1);
    expect(draws[0]!.source).toBe(img);
  });

  it("leaves an image alone when nothing can be read from it", async () => {
    failing.add("https://example.com/gone");
    const root = tree([{ src: "https://example.com/gone", width: 40, height: 40 }]);

    expect(await flattenImages(root)).toBe(0);
    expect(draws).toEqual([]);
    expect(root.querySelector("img")!.getAttribute("src")).toBe("https://example.com/gone");
  });

  it("leaves an image alone when the canvas is tainted", async () => {
    tainted = true;
    const root = tree([{ src: "/portal/refs/asset/mark", width: 80, height: 80 }]);

    expect(await flattenImages(root)).toBe(0);
    expect(root.querySelector("img")!.getAttribute("src")).toBe("/portal/refs/asset/mark");
  });

  it("leaves an image alone when the document has no rasterizer", async () => {
    contextless = true;
    const root = tree([{ src: "/portal/refs/asset/mark", width: 80, height: 80 }]);

    expect(await flattenImages(root)).toBe(0);
  });

  it("skips an image that draws nothing", async () => {
    const root = tree([
      { src: "/portal/refs/asset/hidden", width: 0, height: 0 },
      { src: "", width: 50, height: 50, markup: `<img alt="">` },
    ]);

    expect(await flattenImages(root)).toBe(0);
    expect(draws).toEqual([]);
  });

  it("bounds the copy of a full-bleed image", async () => {
    const root = tree([{ src: "/portal/refs/asset/wall", width: 4000, height: 3000 }]);

    await flattenImages(root);

    expect(draws).toEqual([{ source: expect.anything(), width: 2048, height: 2048 }]);
  });

  it("drops the srcset and the picture sources that would put the address back", async () => {
    const root = tree([
      {
        src: "/portal/refs/asset/mark",
        width: 60,
        height: 60,
        markup:
          `<picture><source srcset="/portal/refs/asset/wide"></source>` +
          `<img src="/portal/refs/asset/mark" srcset="/portal/refs/asset/2x 2x" alt=""></picture>`,
      },
    ]);

    await flattenImages(root);

    const img = root.querySelector("img")!;
    expect(img.hasAttribute("srcset")).toBe(false);
    expect(root.querySelectorAll("source")).toHaveLength(0);
    expect(img.getAttribute("src")).toBe("data:image/png;base64,120x120");
  });

  it("puts the document's own address back when the copy will not load", async () => {
    const root = tree([{ src: "/portal/refs/asset/mark", width: 60, height: 60 }]);
    failing.add("data:image/png;base64,120x120");

    expect(await flattenImages(root)).toBe(0);
    expect(root.querySelector("img")!.getAttribute("src")).toBe("/portal/refs/asset/mark");
  });

  it("redraws every image in the document", async () => {
    const root = tree([
      { src: "/portal/refs/asset/mark", width: 180, height: 161 },
      { src: "/portal/refs/asset/mark", width: 30, height: 30 },
    ]);

    expect(await flattenImages(root)).toBe(2);
    expect(draws.map((d) => d.width)).toEqual([360, 60]);
  });
});
