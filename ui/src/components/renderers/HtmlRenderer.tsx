import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { FileDown, LayoutGrid, Presentation } from "lucide-react";
import { isSlideDeck, overviewMessage } from "@/lib/deck";
import { downloadPdf } from "@/lib/pdfExport";

/**
 * Whether this browser lets a page fullscreen an element it holds. Read once
 * per render rather than at module load so a test can set it.
 */
function fullscreenAvailable(): boolean {
  return typeof document !== "undefined" && document.fullscreenEnabled === true;
}

const CONTROL =
  "inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent transition-colors disabled:opacity-50";

interface Props {
  content: string;
  /**
   * Where the document's controls render when the page has a row for them
   * (#1769): the asset page's control row, the share page's header. Without
   * one they render above the frame.
   */
  controlsSlot?: HTMLElement | null;
  /**
   * The document's PDF route (#1983), beside the content route it was read
   * from. Without one there is nothing to export from, and Export PDF is not
   * offered.
   */
  pdfUrl?: string;
}

/**
 * An HTML asset, framed.
 *
 * The document is handed to the frame as `srcdoc` rather than through a blob:
 * URL. A blob document has no path to resolve against, so a root-relative URL
 * in the artifact (`/portal/vendor/reveal/reveal.js`, the presentation runtime
 * the platform serves, #1767) named nothing at all; an srcdoc document resolves
 * such a URL against the page that framed it, which is the origin the artifact
 * was saved to. The sandbox is unchanged: `allow-scripts` without
 * `allow-same-origin`, so the artifact's script runs in an opaque origin with
 * no reach into the viewer's origin or storage.
 *
 * The frame takes the height its page leaves it: both viewers lay the content
 * column out as a flex column, and the frame grows to its end (#1769). Where
 * the page does not, a minimum height keeps the document readable.
 *
 * Present fullscreens the frame from this page and moves focus into it, so a
 * slide deck's keyboard navigation works from the first keystroke. The frame
 * also carries the fullscreen permission, which is what lets a document's own
 * fullscreen request (reveal.js answers the F key with one) succeed inside a
 * sandbox. The control is offered on every HTML asset, because the viewer
 * cannot tell a deck from a dashboard and fullscreening either is harmless.
 *
 * Overview asks the runtime for its grid of every slide, through the message
 * API the runtime listens on by default; it is offered only where the document
 * names the served runtime, since nothing else answers. Export PDF downloads
 * the document printed by the platform's renderer (#1983): its own colors and
 * backgrounds, and for a deck one page per slide at its last build step. A
 * browser's own print would drop the backgrounds unless the reader asked for
 * them, and would give every build step a page.
 */
export function HtmlRenderer({ content, controlsSlot, pdfUrl }: Props) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const [canPresent, setCanPresent] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const deck = isSlideDeck(content);

  useEffect(() => {
    setCanPresent(fullscreenAvailable());
  }, []);

  const present = useCallback(() => {
    const frame = iframeRef.current;
    if (!frame) return;
    // A refused request (the browser wants a fresher gesture, or the page is
    // itself framed without permission) is not an error the reader can act on:
    // the frame stays where it was, and focus still moves into it so the
    // keyboard reaches the document.
    void frame.requestFullscreen?.().catch(() => undefined);
    frame.focus();
  }, []);

  const overview = useCallback(() => {
    const frame = iframeRef.current;
    if (!frame) return;
    // The frame's origin is opaque, so the message names no target origin; it
    // carries a method name and nothing else.
    frame.contentWindow?.postMessage(overviewMessage(), "*");
    frame.focus();
  }, []);

  const exportPdf = useCallback(() => {
    if (!pdfUrl) return;
    setExporting(true);
    setExportError(null);
    downloadPdf(pdfUrl)
      .catch((err: unknown) => setExportError(err instanceof Error ? err.message : "The PDF could not be made."))
      .finally(() => setExporting(false));
  }, [pdfUrl]);

  const controls = (
    <>
      {canPresent && (
        <button
          type="button"
          onClick={present}
          className={CONTROL}
          title="Show this document fullscreen, with the keyboard focused on it"
        >
          <Presentation className="size-4" aria-hidden="true" />
          Present
        </button>
      )}
      {deck && (
        <button
          type="button"
          onClick={overview}
          className={CONTROL}
          title="Show every slide at once; press again, or pick a slide, to return"
        >
          <LayoutGrid className="size-4" aria-hidden="true" />
          Overview
        </button>
      )}
      {pdfUrl && (
        <button
          type="button"
          onClick={exportPdf}
          disabled={exporting}
          className={CONTROL}
          title={
            deck
              ? "Download a PDF with one page per slide, in the deck's own colors"
              : "Download this document as a PDF, as it looks on screen"
          }
        >
          <FileDown className="size-4" aria-hidden="true" />
          {exporting ? "Exporting…" : "Export PDF"}
        </button>
      )}
      {exportError && (
        <span role="alert" className="text-sm text-destructive">
          {exportError}
        </span>
      )}
    </>
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      {placeControls(controls, controlsSlot)}
      <iframe
        ref={iframeRef}
        sandbox="allow-scripts"
        allow="fullscreen"
        srcDoc={content}
        className="min-h-[60vh] w-full min-w-0 flex-1 rounded-lg border border-border"
        title="HTML Preview"
      />
    </div>
  );
}

/** The controls in the page's slot when it has one, above the frame otherwise. */
function placeControls(controls: ReactNode, slot: HTMLElement | null | undefined): ReactNode {
  if (slot) return createPortal(controls, slot);
  return <div className="flex flex-wrap justify-end gap-2">{controls}</div>;
}
