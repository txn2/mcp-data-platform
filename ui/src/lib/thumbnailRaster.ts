import html2canvas from "html2canvas";

/**
 * Rasterizing a document that holds something html2canvas cannot draw.
 *
 * One box html2canvas throws on aborts the whole capture, and the tile is a
 * picture of the WHOLE document, so an asset containing such a box never had a
 * tile and never would: the exception was swallowed, the queue spent its
 * attempts on it, and Recapture repeated the same throw (#1751).
 *
 * The box that does it is an ordinary bar in a bar chart. html2canvas draws a
 * gradient by filling a canvas the size of the background positioning area and
 * handing that canvas to ctx.createPattern; the canvas dimensions are
 * truncated to integers, so an area under one pixel wide gives a zero-width
 * canvas and createPattern throws InvalidStateError. Its own guard is
 * `width > 0 && height > 0` (html2canvas 1.4.1, CanvasRenderer.
 * renderBackgroundImage), which an area of 0.69px passes and a canvas of 0
 * does not survive.
 *
 * Measured in a real browser over the eight shapes the ticket names: 0.09px,
 * 0.33px and 0.69px throw; exactly 0 does not (the guard catches it) and
 * 1.03px does not. Which is the rule this implements.
 */

/** The options a caller hands html2canvas, less the element. */
export type RasterOptions = Parameters<typeof html2canvas>[1];

/**
 * What was done to a document to get it drawn, for the caller to report.
 *
 * A capture that needed the degraded pass drew a document with no background
 * images at all, which is worth saying in the console beside the exception that
 * forced it (#1752).
 */
export interface RasterOutcome {
  canvas: HTMLCanvasElement;
  /** Boxes whose background was dropped before the first attempt. */
  neutralized: number;
  /** Whether the first attempt threw and the capture fell back to a bare one. */
  degraded: boolean;
  /** The exception the first attempt threw, when it did. */
  firstError?: unknown;
}

/**
 * Draw an element, leaving out what cannot be rasterized rather than
 * abandoning the capture.
 *
 * Two passes, in the order of how much they cost the picture. The first drops
 * the background of every box whose painted area is under a pixel -- which is
 * invisible at any scale, so the tile is unchanged -- and draws. Only if that
 * still throws does the second drop every background image in the document and
 * draw again: a flatter picture of the document, which is the thing the reader
 * wanted, instead of no picture at all.
 *
 * The tree is mutated in place and not restored. Every caller hands this an
 * off-screen container or a capture-only iframe document that is torn down
 * afterwards; nothing the reader is looking at is touched.
 */
export async function rasterize(
  element: HTMLElement,
  options: RasterOptions,
): Promise<RasterOutcome> {
  const neutralized = dropUndrawableBackgrounds(element);
  try {
    return { canvas: await html2canvas(element, options), neutralized, degraded: false };
  } catch (firstError) {
    dropAllBackgroundImages(element);
    const canvas = await html2canvas(element, options).catch(() => {
      // The degraded pass failed too, so the document holds something else
      // this cannot draw. The first exception is the one that names it.
      throw firstError;
    });
    return { canvas, neutralized, degraded: true, firstError };
  }
}

/** A canvas as a PNG blob, which is what every capture path uploads. */
export function canvasToPng(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error("canvas.toBlob returned null"))),
      "image/png",
    );
  });
}

/**
 * Drop the background image of every box whose painted area is under a pixel.
 *
 * Returns how many were dropped, which is what a caller reports when a document
 * needed this.
 */
function dropUndrawableBackgrounds(root: HTMLElement): number {
  let dropped = 0;
  forEachStyledElement(root, (el, style) => {
    if (backgroundTileIsUndrawable(el, style)) {
      el.style.backgroundImage = "none";
      dropped++;
    }
  });
  return dropped;
}

/** Drop every background image under `root`, the degraded pass. */
function dropAllBackgroundImages(root: HTMLElement): void {
  forEachStyledElement(root, (el) => {
    el.style.backgroundImage = "none";
  });
}

/**
 * Walk every element under `root` that carries a background image, with its
 * computed style.
 *
 * The style comes from the element's OWN window: an iframe capture walks the
 * frame's document, and the parent's getComputedStyle cannot read it.
 */
function forEachStyledElement(
  root: HTMLElement,
  visit: (el: HTMLElement, style: CSSStyleDeclaration) => void,
): void {
  const view = root.ownerDocument.defaultView;
  if (!view) return;
  for (const el of [root, ...root.querySelectorAll<HTMLElement>("*")]) {
    // A style object is not readable for an element in a document that has been
    // torn down, and a capture races a queue that moves on; a document that
    // will not answer has nothing to neutralize either.
    const style = view.getComputedStyle(el);
    if (!style || style.backgroundImage === "none" || style.backgroundImage === "") continue;
    visit(el, style);
  }
}

/**
 * Whether a box's background tile would be drawn into a zero-dimension canvas.
 *
 * The tile is the background positioning area -- the padding box by default,
 * the border box or content box under background-origin -- unless
 * background-size names lengths of its own, in which case that is the tile.
 * Either way, a dimension that truncates to zero is the throw.
 *
 * A dimension of exactly zero is left alone: html2canvas guards that case
 * itself, and this exists to remove as little of the document as possible.
 */
function backgroundTileIsUndrawable(el: HTMLElement, style: CSSStyleDeclaration): boolean {
  const area = backgroundPositioningArea(el, style);
  if (underOnePixel(area.width) || underOnePixel(area.height)) return true;
  const sized = backgroundTileSize(style, area);
  return sized !== null && (underOnePixel(sized.width) || underOnePixel(sized.height));
}

/** A length html2canvas would truncate to a canvas dimension of zero. */
function underOnePixel(value: number): boolean {
  return value > 0 && value < 1;
}

interface Area {
  width: number;
  height: number;
}

/**
 * The area a background is laid out in: the border box less whatever
 * background-origin excludes.
 *
 * getBoundingClientRect is the same measurement html2canvas takes (parseBounds),
 * so the two agree on a transformed or fractionally-positioned box.
 */
function backgroundPositioningArea(el: HTMLElement, style: CSSStyleDeclaration): Area {
  const rect = el.getBoundingClientRect();
  const origin = firstLayer(style.backgroundOrigin);
  if (origin === "border-box") return { width: rect.width, height: rect.height };
  const border = inset(style, "border");
  const area = {
    width: rect.width - border.x,
    height: rect.height - border.y,
  };
  if (origin !== "content-box") return area;
  const padding = inset(style, "padding");
  return { width: area.width - padding.x, height: area.height - padding.y };
}

/**
 * The tile size background-size asks for, or null when it is auto, cover or
 * contain -- for a gradient, all three mean the positioning area, which the
 * caller has already measured.
 */
function backgroundTileSize(style: CSSStyleDeclaration, area: Area): Area | null {
  const size = firstLayer(style.backgroundSize);
  if (size === "" || size === "auto" || size === "cover" || size === "contain") return null;
  const parts = size.split(/\s+/);
  const width = resolveLength(parts[0], area.width);
  const height = resolveLength(parts[1] ?? "auto", area.height);
  if (width === null && height === null) return null;
  return { width: width ?? area.width, height: height ?? area.height };
}

/** One CSS length or percentage against the axis it is measured on. */
function resolveLength(value: string | undefined, against: number): number | null {
  if (!value || value === "auto") return null;
  if (value.endsWith("%")) {
    const pct = Number.parseFloat(value);
    return Number.isFinite(pct) ? (pct / 100) * against : null;
  }
  const px = Number.parseFloat(value);
  return Number.isFinite(px) ? px : null;
}

/**
 * The first layer of a comma-separated background property.
 *
 * A box can carry several background layers and html2canvas draws each in turn,
 * so any one of them can throw. The first layer is the one the shorthand every
 * such box is written with produces, and a box whose layers disagree about
 * their size is a box under a pixel either way -- the area test above has
 * already caught it.
 */
function firstLayer(value: string): string {
  return (value.split(",")[0] ?? "").trim();
}

/** The horizontal and vertical total of a box's border or padding. */
function inset(style: CSSStyleDeclaration, box: "border" | "padding"): { x: number; y: number } {
  const side = (name: string): number => {
    const property = box === "border" ? `border-${name}-width` : `padding-${name}`;
    const px = Number.parseFloat(style.getPropertyValue(property));
    return Number.isFinite(px) ? px : 0;
  };
  return { x: side("left") + side("right"), y: side("top") + side("bottom") };
}
