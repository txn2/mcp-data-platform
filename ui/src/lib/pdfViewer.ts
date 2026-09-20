/**
 * Mozilla's PDF.js, loaded as the platform's PDF viewer.
 *
 * The viewer used to be `<object type="application/pdf">`, which hands the file
 * to the browser's own plugin with full honours -- including the document's
 * `/OpenAction`. A file carrying `<< /S /Named /N /Print >>` raised the print
 * dialog the moment a reader opened it (#1783). Sandboxing is not an option:
 * a sandboxed frame cannot instantiate the plugin at all in Chrome, so it
 * blocks the print by blocking the viewer.
 *
 * PDF.js closes that door by construction rather than by configuration:
 *
 *   - The core parses and rasterizes. It has no `window.print`, and the
 *     document-level open action is only *exposed*, through `getOpenAction()`,
 *     which something has to ask for. Nothing here asks.
 *   - `PDFLinkService.executeNamedAction` implements exactly six navigation
 *     actions (GoBack, GoForward, NextPage, PrevPage, FirstPage, LastPage).
 *     `Print` falls to its `default: break`. Its only caller binds an
 *     annotation's onclick, so even a named action inside the document needs a
 *     reader to click it.
 *
 * What we give up is Chrome's polished plugin UI, which is why this module's
 * consumer builds a toolbar (PdfRenderer.tsx). What we gain beyond the fix is a
 * viewer that renders identically in every browser and in the platform's own
 * headless renderer, which ships no PDF plugin (see lib/pdfPage.ts, #1794).
 */

// The worker's URL rather than its module: pdf.js starts it as a Worker, so it
// must be a file the page can fetch from its own origin. Vite emits it beside
// the other chunks. This is the same worker the tile path uses (lib/pdfPage.ts).
import pdfWorkerURL from "pdfjs-dist/build/pdf.worker.min.mjs?url";

// The component layer's stylesheet: page canvases, the text layer that makes a
// PDF selectable, and the annotation layer that makes its links clickable.
// Imported here rather than globally so it is emitted into this chunk's CSS and
// costs nothing on a page that never opens a PDF.
import "pdfjs-dist/web/pdf_viewer.css";
// Imported after it, and the one rule in it that reaches past the viewer is
// put back the way the portal wants it. See the file for what and why.
import "./pdfViewer.css";

type PdfjsModule = typeof import("pdfjs-dist");
type PdfViewerModule = typeof import("pdfjs-dist/web/pdf_viewer.mjs");

export interface PdfLib {
  pdfjs: PdfjsModule;
  viewer: PdfViewerModule;
}

let pending: Promise<PdfLib> | null = null;

/**
 * Loads the core and the viewer components, in that order, once per page.
 *
 * The order is not stylistic. `pdfjs-dist/web/pdf_viewer.mjs` does not import
 * the core: it destructures 62 names off `globalThis.pdfjsLib` at module
 * evaluation time (`const { ... } = globalThis.pdfjsLib;`). Importing it before
 * that global is populated throws while the module is being evaluated, which
 * surfaces as a chunk that fails to load rather than as a clear error. So the
 * core is imported first, published under the name the components read, and
 * only then are the components pulled in.
 *
 * The promise is memoized: a reader who opens three PDFs pays one import and
 * assigns the global once.
 */
export function loadPdfViewer(): Promise<PdfLib> {
  pending ??= (async () => {
    const pdfjs = await import("pdfjs-dist");
    pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerURL;
    (globalThis as { pdfjsLib?: PdfjsModule }).pdfjsLib = pdfjs;
    const viewer = await import("pdfjs-dist/web/pdf_viewer.mjs");
    return { pdfjs, viewer };
  })().catch((err: unknown) => {
    // A failed import must not poison every later attempt: a chunk that failed
    // to arrive once (a deploy mid-session, a dropped connection) is worth
    // retrying when the reader opens the next document.
    pending = null;
    throw err;
  });
  return pending;
}
