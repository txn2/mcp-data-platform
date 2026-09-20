/**
 * Page one of a PDF, drawn onto a canvas.
 *
 * The viewer shows a PDF in the browser's own plugin, which the platform's
 * headless renderer does not ship, so a PDF had no tile at all and a library of
 * them was a wall of identical icons (#1794). pdf.js rasterizes a page in plain
 * JavaScript and needs no plugin, which is what makes a tile possible here.
 *
 * It is loaded through a dynamic import so it is a chunk of its own: pdf.js and
 * its worker are about 1.6 MB, nothing but a PDF tile needs them, and the share
 * viewer shares this build's chunk graph.
 */

// The worker's URL rather than its module: pdf.js starts it as a Worker, so it
// must be a file the page can fetch from its own origin. Vite emits it beside
// the other chunks, under the prefix the platform answers in-process while a
// tile is being drawn (internal/platform/thumbworker, inProcessPrefixes).
import pdfWorkerURL from "pdfjs-dist/build/pdf.worker.min.mjs?url";

import { THUMB_HEIGHT, THUMB_WIDTH } from "@/lib/thumbnailSupport";

/**
 * Draws the document's first page onto canvas, scaled to cover it.
 *
 * Cover rather than fit, which is how a raster tile is shown: a portrait page
 * fills the tile's width and is cropped at its foot, so what a person scanning
 * a folder sees is the top of the document at a readable size rather than a
 * small page floating in margins.
 *
 * The canvas is sized in device pixels and displayed at tile size. A canvas has
 * a fixed backing store, so one painted at CSS size would be the thing the
 * renderer then upscales, and the tile would be soft in exactly the way drawing
 * at 2x exists to prevent (#1789).
 *
 * Rejects when the document cannot be read at all; the caller records that as
 * the reason the file has no tile.
 */
export async function drawFirstPage(canvas: HTMLCanvasElement, url: string): Promise<void> {
  const pdfjs = await import("pdfjs-dist");
  pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerURL;

  const task = pdfjs.getDocument({ url });
  try {
    const doc = await task.promise;
    if (doc.numPages < 1) throw new Error("the document has no pages");
    const page = await doc.getPage(1);
    const ratio = window.devicePixelRatio || 1;
    const width = Math.round(THUMB_WIDTH * ratio);
    const height = Math.round(THUMB_HEIGHT * ratio);
    const unscaled = page.getViewport({ scale: 1 });
    const viewport = page.getViewport({
      scale: Math.max(width / unscaled.width, height / unscaled.height),
    });

    canvas.width = width;
    canvas.height = height;
    canvas.style.width = `${THUMB_WIDTH}px`;
    canvas.style.height = `${THUMB_HEIGHT}px`;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("the tile page has no 2d canvas context");
    // A PDF page is transparent where it declares no fill, and the tile is
    // stored as a PNG that keeps that transparency: a scanned page would show
    // through onto whatever sits behind the card.
    context.fillStyle = "#ffffff";
    context.fillRect(0, 0, width, height);
    await page.render({ canvas, canvasContext: context, viewport }).promise;
  } finally {
    // Ends the worker and the request behind it. The renderer holds the whole
    // document while it is open, and a tile is one page of it.
    //
    // A failure to tear down is swallowed deliberately: this runs on the way
    // out of a rejection too, and an await that throws in a finally block
    // REPLACES the rejection it was unwinding -- which would turn "the
    // document is password-protected" into whatever the teardown said.
    await task.destroy().catch(() => {});
  }
}

/**
 * Why a document could not be drawn, in words for the person reading the
 * thumbnail panel.
 *
 * pdf.js names its own refusals, and two of them are common and worth saying
 * plainly: an encrypted document reports "No password given", which reads as a
 * platform fault rather than as a property of the file. Anything else is
 * reported as pdf.js put it, which is more use than a sentence written here
 * that covers every remaining case equally badly.
 */
export function pdfFailureReason(err: unknown): string {
  const e = err as { name?: string; message?: string } | undefined;
  switch (e?.name) {
    case "PasswordException":
      return "the document is password-protected";
    case "InvalidPDFException":
      return "the file could not be read as a PDF";
    case "MissingPDFException":
      return "the document could not be loaded";
    default:
      return e?.message || String(err);
  }
}
