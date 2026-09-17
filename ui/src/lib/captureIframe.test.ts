import { describe, it, expect, vi, afterEach } from "vitest";

// The rasterizer needs a real canvas, which jsdom does not have; what this
// file asserts is what captureIframe hands it, so the rasterizer is replaced
// by a recorder and the PNG encoder by a stub.
const seen: { element?: HTMLElement; options?: Record<string, unknown> } = {};
vi.mock("@/lib/thumbnailRaster", () => ({
  rasterize: vi.fn(async (element: HTMLElement, options: Record<string, unknown>) => {
    seen.element = element;
    seen.options = options;
    return { canvas: options["canvas"], neutralized: 0, degraded: false };
  }),
  canvasToPng: vi.fn(async () => new Blob(["png"], { type: "image/png" })),
}));

import { captureIframe, RENDER_WIDTH, RENDER_HEIGHT } from "./thumbnail";
import { THUMB_WIDTH } from "./thumbnailSupport";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("captureIframe", () => {
  it("draws on a canvas the frame's own document created, sized to the tile (#1767)", async () => {
    const iframe = document.createElement("iframe");
    document.body.appendChild(iframe);
    const doc = iframe.contentDocument!;
    doc.write("<html><body><h1>Presenting from the portal</h1></body></html>");
    doc.close();

    const result = await captureIframe(iframe);

    expect(seen.element).toBe(doc.body);
    const canvas = seen.options?.["canvas"] as HTMLCanvasElement;
    expect(canvas).toBeInstanceOf(doc.defaultView!.HTMLCanvasElement);
    // The frame's document, not this one: a context of this document knows
    // none of the fonts the artifact's stylesheets declare, and draws a
    // fallback face over positions measured in the artifact's own.
    expect(canvas.ownerDocument).toBe(doc);
    expect(canvas.ownerDocument).not.toBe(document);
    const scale = THUMB_WIDTH / RENDER_WIDTH;
    expect(canvas.width).toBe(Math.floor(RENDER_WIDTH * scale));
    expect(canvas.height).toBe(Math.floor(RENDER_HEIGHT * scale));
    expect(seen.options?.["scale"]).toBe(scale);
    expect(result.outcome.canvas).toBe(canvas);
  });

  it("refuses a frame whose document it cannot reach", async () => {
    const iframe = document.createElement("iframe");
    Object.defineProperty(iframe, "contentDocument", { value: null });
    await expect(captureIframe(iframe)).rejects.toThrow("Cannot access iframe content");
  });
});
