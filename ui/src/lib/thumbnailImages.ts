/**
 * Redrawing the images in a document so html2canvas draws all of each one.
 *
 * html2canvas draws a replaced element with the nine-argument form of
 * drawImage: the source rectangle is `0, 0, naturalWidth, naturalHeight` and
 * the destination is the element's content box (1.4.1,
 * `CanvasRenderer.renderReplacedElement`). For a raster image that rectangle is
 * the whole file by definition. For an SVG with a viewBox and no width or
 * height it is not: the browser gives such an image the default object size --
 * 168x150 for a 378x338 viewBox -- while the drawing it rasterizes from is the
 * viewBox, so the source rectangle names a corner of it. The tile showed a
 * sliver of the brand mark stretched to fill the box, at every size it appeared
 * at, and a recapture produced the same sliver (#1771).
 *
 * The fix is to give every image an intrinsic size equal to its rendered size:
 * draw it into a canvas with the five-argument form, which draws the WHOLE
 * image whatever its intrinsic size, and hand the element that PNG. html2canvas
 * then draws a source rectangle that is the whole picture again.
 *
 * It is done to every image rather than to the ones that are SVG, because
 * nothing in the document says which those are: a declared reference is served
 * from `/portal/refs/<asset>/<token>`, which carries no extension and is
 * exactly the address the reported mark was placed by. The substitution is not
 * a compromise for the rest: html2canvas 1.4.1 implements no `object-fit`, so
 * it stretches every replaced element into its content box, and a copy drawn to
 * the rendered size and then stretched into the same box is the picture it
 * would have drawn anyway.
 */

/**
 * How many device pixels the copy carries per CSS pixel of rendered size.
 *
 * The tile is drawn at a fraction of the document's size, so one would do; two
 * costs little and leaves the copy sharp if the capture scale ever rises.
 */
const COPY_SCALE = 2;

/**
 * The largest copy that is drawn, per side, so a full-bleed image is bounded.
 *
 * Each side is bounded on its own, which can leave the copy a different shape
 * from the element. That costs resolution and nothing else: the copy is the
 * whole image, and html2canvas draws the whole copy into the content box, so
 * what lands on the tile is the whole image in the box either way.
 */
const MAX_COPY_SIDE = 2048;

/**
 * Replace every image under `root` with a PNG of itself drawn at its rendered
 * size, and report how many were replaced.
 *
 * An image that cannot be copied is left exactly as it was: a cross-origin
 * image the deployment does not answer a CORS request for taints the canvas and
 * `toDataURL` throws, a document being torn down has no layout to measure, and
 * either way the capture proceeds with what html2canvas would have drawn
 * before. The tree is mutated in place; every caller hands this an off-screen
 * container or a capture-only frame that is discarded afterwards.
 */
export async function flattenImages(root: HTMLElement): Promise<number> {
  const doc = root.ownerDocument;
  const images = [...root.querySelectorAll("img")];
  const replaced = await Promise.all(images.map((img) => flattenImage(doc, img)));
  return replaced.filter(Boolean).length;
}

/** One image, replaced by a PNG of itself, or left alone. */
async function flattenImage(doc: Document, img: HTMLImageElement): Promise<boolean> {
  const size = copySize(img);
  if (!size) return false;
  const source = await drawableSource(doc, img);
  if (!source) return false;
  const png = drawToPng(doc, source, size);
  if (!png) return false;
  return replaceSource(img, png);
}

/** The copy's pixel dimensions, or null when the element draws nothing. */
function copySize(img: HTMLImageElement): { width: number; height: number } | null {
  const rect = img.getBoundingClientRect();
  const width = Math.min(Math.round(rect.width * COPY_SCALE), MAX_COPY_SIDE);
  const height = Math.min(Math.round(rect.height * COPY_SCALE), MAX_COPY_SIDE);
  if (width < 1 || height < 1) return null;
  return { width, height };
}

/**
 * An image element whose pixels this document is allowed to read.
 *
 * The element in the tree is not it. A capture frame has an opaque origin, so
 * every address in it -- including the platform's own reference route -- is
 * cross-origin, and drawing that element taints the canvas. A fresh element
 * asking for the image with CORS is what html2canvas itself loads through
 * (`useCORS`), so an image it can already draw is an image this can copy.
 * Where that request is refused the element itself is the fallback, and a
 * canvas it taints is caught below.
 */
async function drawableSource(
  doc: Document,
  img: HTMLImageElement,
): Promise<HTMLImageElement | null> {
  const src = img.currentSrc || img.src;
  if (!src) return null;
  const copy = doc.createElement("img");
  copy.crossOrigin = "anonymous";
  const loaded = await load(copy, src);
  if (loaded) return copy;
  return img.complete && img.naturalWidth > 0 ? img : null;
}

/** The source drawn whole into a canvas of the given size, as a data URL. */
function drawToPng(
  doc: Document,
  source: HTMLImageElement,
  size: { width: number; height: number },
): string | null {
  const canvas = doc.createElement("canvas");
  canvas.width = size.width;
  canvas.height = size.height;
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  try {
    // The five-argument form: the whole image, scaled into the destination.
    // This is the call whose absence is the defect -- html2canvas names a
    // source rectangle, and for an image with no intrinsic size that rectangle
    // is not the whole drawing.
    ctx.drawImage(source, 0, 0, size.width, size.height);
    return canvas.toDataURL("image/png");
  } catch {
    // A tainted canvas refuses toDataURL. The image stays as it was.
    return null;
  }
}

/**
 * Point the element at the copy and wait for it to be readable.
 *
 * html2canvas reads `naturalWidth` off the live element while it parses the
 * tree, so the capture cannot start until the new source has loaded; and it
 * reads `currentSrc` first, so a `srcset` -- or a `<source>` in a `<picture>`
 * around the element -- has to go, or selection would put the original address
 * back and the element would keep its old intrinsic size.
 *
 * A copy the browser then refuses to load would be worse than the sliver this
 * exists to fix, so the address the document wrote goes back and the element
 * is drawn exactly as it would have been.
 */
async function replaceSource(img: HTMLImageElement, png: string): Promise<boolean> {
  const original = img.getAttribute("src");
  const srcset = img.getAttribute("srcset");
  img.removeAttribute("srcset");
  const parent = img.parentElement;
  if (parent && parent.tagName === "PICTURE") {
    for (const source of parent.querySelectorAll("source")) source.remove();
  }
  if (await load(img, png)) return true;
  if (srcset !== null) img.setAttribute("srcset", srcset);
  if (original !== null) await load(img, original);
  return false;
}

/** Set an image's source and resolve once the browser has finished with it. */
function load(img: HTMLImageElement, src: string): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    img.addEventListener("load", () => resolve(true), { once: true });
    img.addEventListener("error", () => resolve(false), { once: true });
    img.src = src;
  });
}
