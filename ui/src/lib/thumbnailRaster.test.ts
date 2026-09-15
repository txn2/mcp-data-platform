import { describe, it, expect, vi, beforeEach } from "vitest";

// html2canvas is what throws, so it is what is stood in for here: the module
// under test decides WHICH boxes to take the background off before calling it,
// and what to do when it throws anyway.
//
// That a gradient under one pixel wide actually throws in a browser, and that
// the pass below stops it, is not assertable in jsdom -- there is no rasterizer
// -- and is held by e2e/thumbnails/capture.spec.ts against a real one (#1751).
const html2canvas = vi.hoisted(() => vi.fn());
vi.mock("html2canvas", () => ({ default: html2canvas }));

const { rasterize } = await import("./thumbnailRaster");

/** A box of the given size, with the styles a capture reads off it. */
function box(
  width: number,
  height: number,
  style: Partial<Record<string, string>> = {},
): HTMLElement {
  const el = document.createElement("div");
  el.style.backgroundImage = style["background-image"] ?? "linear-gradient(90deg, #4aa3ff, #7bc0ff)";
  el.getBoundingClientRect = () => ({ width, height }) as DOMRect;
  // jsdom computes no layout, so the properties a capture measures are fed in
  // through the computed style the real browser would have produced.
  const computed: Record<string, string> = {
    "background-origin": "padding-box",
    "background-size": "auto",
    "border-left-width": "0px",
    "border-right-width": "0px",
    "border-top-width": "0px",
    "border-bottom-width": "0px",
    "padding-left": "0px",
    "padding-right": "0px",
    "padding-top": "0px",
    "padding-bottom": "0px",
    ...style,
  };
  Object.defineProperty(el, "__computed", { value: computed });
  return el;
}

/** A root holding the boxes, with getComputedStyle answering from each box. */
function tree(...boxes: HTMLElement[]): HTMLElement {
  const root = document.createElement("div");
  root.getBoundingClientRect = () => ({ width: 400, height: 300 }) as DOMRect;
  Object.defineProperty(root, "__computed", {
    value: { "background-image": "none" },
  });
  for (const b of boxes) root.appendChild(b);
  document.body.appendChild(root);
  return root;
}

beforeEach(() => {
  document.body.innerHTML = "";
  html2canvas.mockReset();
  html2canvas.mockResolvedValue({ width: 400, height: 300 } as HTMLCanvasElement);
  vi.spyOn(window, "getComputedStyle").mockImplementation((el) => {
    const computed = (el as unknown as { __computed?: Record<string, string> }).__computed ?? {};
    return {
      backgroundImage: computed["background-image"] ?? (el as HTMLElement).style.backgroundImage,
      backgroundOrigin: computed["background-origin"] ?? "padding-box",
      backgroundSize: computed["background-size"] ?? "auto",
      getPropertyValue: (name: string) => computed[name] ?? "",
    } as unknown as CSSStyleDeclaration;
  });
});

describe("rasterize", () => {
  // The exact shape #1751 was filed for: a bar-chart fill whose width computes
  // to a fraction of a pixel. html2canvas fills a canvas of the background
  // positioning area and hands it to createPattern; the dimensions truncate to
  // integers, so 0.69px is a zero-width canvas and createPattern throws.
  it("takes the background off a box under a pixel wide", async () => {
    const bar = box(0.69, 14);
    const outcome = await rasterize(tree(bar), { width: 400, height: 300 });

    expect(bar.style.backgroundImage).toBe("none");
    expect(outcome.neutralized).toBe(1);
    expect(outcome.degraded).toBe(false);
  });

  it("takes the background off a box under a pixel tall", async () => {
    const bar = box(100, 0.4);
    await rasterize(tree(bar), { width: 400, height: 300 });
    expect(bar.style.backgroundImage).toBe("none");
  });

  // Measured in a real browser: 1.03px draws, 0.69px throws. Removing more of
  // the document than the throw requires is removing it for nothing.
  it("leaves a box of a pixel or more alone", async () => {
    const bar = box(1.03, 14);
    const outcome = await rasterize(tree(bar), { width: 400, height: 300 });

    expect(bar.style.backgroundImage).toContain("linear-gradient");
    expect(outcome.neutralized).toBe(0);
  });

  // html2canvas guards a dimension of exactly zero itself, and a box of no
  // width paints nothing either way.
  it("leaves a box of exactly no width alone", async () => {
    const bar = box(0, 14);
    await rasterize(tree(bar), { width: 400, height: 300 });
    expect(bar.style.backgroundImage).toContain("linear-gradient");
  });

  // The tile is the background positioning area unless background-size names
  // one of its own, and a half-pixel tile on a wide box throws just the same
  // (verified against a real browser).
  it("takes the background off a tile under a pixel on a box that is not", async () => {
    const bar = box(100, 20, { "background-size": "0.5px 4px" });
    await rasterize(tree(bar), { width: 400, height: 300 });
    expect(bar.style.backgroundImage).toBe("none");
  });

  it("measures the padding box, not the border box", async () => {
    // 2.4px wide with 0.8px of border on each side: the area a background is
    // laid out in is 0.8px, which is what throws.
    const bar = box(2.4, 14, { "border-left-width": "0.8px", "border-right-width": "0.8px" });
    await rasterize(tree(bar), { width: 400, height: 300 });
    expect(bar.style.backgroundImage).toBe("none");
  });

  it("measures the border box when background-origin says so", async () => {
    const bar = box(2.4, 14, {
      "background-origin": "border-box",
      "border-left-width": "0.8px",
      "border-right-width": "0.8px",
    });
    await rasterize(tree(bar), { width: 400, height: 300 });
    expect(bar.style.backgroundImage).toContain("linear-gradient");
  });

  // The point of the whole module: the reader gets a picture of their document
  // rather than nothing, whatever else it turns out to hold.
  it("draws the document without its backgrounds when the first attempt throws", async () => {
    const bar = box(120, 14);
    html2canvas.mockRejectedValueOnce(new Error("InvalidStateError: something else entirely"));

    const outcome = await rasterize(tree(bar), { width: 400, height: 300 });

    expect(outcome.degraded).toBe(true);
    expect(String(outcome.firstError)).toContain("something else entirely");
    expect(bar.style.backgroundImage).toBe("none");
    expect(html2canvas).toHaveBeenCalledTimes(2);
  });

  // The first exception is the one that names what the document holds; the
  // second is a consequence of the degraded pass and says less.
  it("reports the first exception when the degraded pass fails too", async () => {
    html2canvas.mockRejectedValueOnce(new Error("the real cause"));
    html2canvas.mockRejectedValueOnce(new Error("and then this"));

    await expect(rasterize(tree(box(120, 14)), { width: 400, height: 300 })).rejects.toThrow(
      "the real cause",
    );
  });

  it("does not call the rasterizer a second time when the first attempt worked", async () => {
    await rasterize(tree(box(120, 14)), { width: 400, height: 300 });
    expect(html2canvas).toHaveBeenCalledTimes(1);
  });
});
