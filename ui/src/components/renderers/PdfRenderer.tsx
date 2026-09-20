import { useCallback, useEffect, useRef, useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  Download,
  FileText,
  Search,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { formatBytes } from "@/lib/format";
import { pdfFailureReason } from "@/lib/pdfPage";
import { loadPdfViewer } from "@/lib/pdfViewer";

interface PdfRendererProps {
  contentUrl: string;
  fileName?: string;
  sizeBytes?: number;
}

/** What the toolbar reads off the open document. */
interface ViewerState {
  page: number;
  pages: number;
  scale: number;
}

/** Matches reported by the find controller, for the search box's counter. */
interface MatchState {
  current: number;
  total: number;
}

const ZOOM_MIN = 0.25;
const ZOOM_MAX = 5;
const ZOOM_STEP = 1.1;

/**
 * PDF viewer, rendered by Mozilla's PDF.js rather than the browser's plugin.
 *
 * The plugin honoured the document's `/OpenAction`, so a file exported with
 * "print on open" raised the print dialog at a reader who had asked only to
 * look at it (#1783). PDF.js does not execute document-level actions at all;
 * lib/pdfViewer.ts records why that holds by construction.
 *
 * Two further reductions are made here, both because this is a preview of a
 * file someone else uploaded and not a document the reader owns:
 *
 *   - `annotationMode: ENABLE` instead of the component's default
 *     `ENABLE_FORMS`. Links still render and still work; interactive form
 *     widgets do not render, so there is no form for a `/SubmitForm` action to
 *     act on.
 *   - No `scriptingManager` is passed, so the viewer instantiates none and the
 *     document's JavaScript never runs.
 *
 * External links open in a new tab with `noopener`, so a link in an uploaded
 * document cannot reach back into the portal tab that framed it.
 */
export function PdfRenderer({ contentUrl, fileName, sizeBytes }: PdfRendererProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  // The live viewer objects. They are refs rather than state because the
  // toolbar drives them imperatively and re-rendering must never rebuild them:
  // a second PDFViewer over the same container would paint pages twice.
  const viewerRef = useRef<InstanceType<
    Awaited<ReturnType<typeof loadPdfViewer>>["viewer"]["PDFViewer"]
  > | null>(null);
  const findRef = useRef<{
    dispatch: (query: string, findPrevious: boolean) => void;
  } | null>(null);

  const [state, setState] = useState<ViewerState>({ page: 0, pages: 0, scale: 1 });
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [matches, setMatches] = useState<MatchState>({ current: 0, total: 0 });

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    // Guards the async setup against a container that unmounted, or a
    // contentUrl that changed, while the chunk and the document were loading.
    let live = true;
    let destroy: (() => void) | null = null;

    setLoading(true);
    setError(null);
    setState({ page: 0, pages: 0, scale: 1 });
    setMatches({ current: 0, total: 0 });

    void (async () => {
      try {
        const { pdfjs, viewer: components } = await loadPdfViewer();
        if (!live) return;

        const eventBus = new components.EventBus();
        const linkService = new components.PDFLinkService({
          eventBus,
          externalLinkTarget: components.LinkTarget.BLANK,
          externalLinkRel: "noopener noreferrer nofollow",
        });
        const findController = new components.PDFFindController({ eventBus, linkService });
        const pdfViewer = new components.PDFViewer({
          container,
          eventBus,
          linkService,
          findController,
          annotationMode: pdfjs.AnnotationMode.ENABLE,
        });
        linkService.setViewer(pdfViewer);

        eventBus.on("pagesinit", () => {
          // Page-width is the reading default: a portrait page fills the frame
          // and the reader scrolls, rather than landing on a page scaled to
          // whatever the document declared.
          pdfViewer.currentScaleValue = "page-width";
          if (!live) return;
          setState({
            page: pdfViewer.currentPageNumber,
            pages: pdfViewer.pagesCount,
            scale: pdfViewer.currentScale,
          });
          setLoading(false);
        });
        eventBus.on("pagechanging", (e: { pageNumber: number }) => {
          if (live) setState((s) => ({ ...s, page: e.pageNumber }));
        });
        eventBus.on("scalechanging", (e: { scale: number }) => {
          if (live) setState((s) => ({ ...s, scale: e.scale }));
        });
        eventBus.on("updatefindmatchescount", (e: { matchesCount: { current: number; total: number } }) => {
          if (live) setMatches({ current: e.matchesCount.current, total: e.matchesCount.total });
        });
        eventBus.on(
          "updatefindcontrolstate",
          (e: { matchesCount: { current: number; total: number }; state: number }) => {
            // FindState.NOT_FOUND still carries a zeroed count, which is what
            // the counter should show rather than the previous query's total.
            if (live) setMatches({ current: e.matchesCount.current, total: e.matchesCount.total });
          },
        );

        const task = pdfjs.getDocument({ url: contentUrl });
        const doc = await task.promise;
        if (!live) {
          await task.destroy().catch(() => {});
          return;
        }

        pdfViewer.setDocument(doc);
        linkService.setDocument(doc, null);
        viewerRef.current = pdfViewer;
        findRef.current = {
          dispatch: (q: string, findPrevious: boolean) =>
            eventBus.dispatch("find", {
              source: null,
              type: findPrevious ? "again" : "",
              query: q,
              caseSensitive: false,
              entireWord: false,
              highlightAll: true,
              findPrevious,
              matchDiacritics: false,
            }),
        };

        destroy = () => {
          // A viewer is torn down by being handed a null document, which is
          // what resets its page views and stops them observing the scroll
          // container; PDF.js's own viewer app does the same. The bundled
          // declaration types the parameter as non-null, which is stricter
          // than the implementation, so the null is cast here rather than the
          // teardown being skipped.
          (pdfViewer.setDocument as (doc: unknown) => void)(null);
          (linkService.setDocument as (doc: unknown) => void)(null);
          void task.destroy().catch(() => {});
        };
      } catch (err) {
        if (live) {
          setError(pdfFailureReason(err));
          setLoading(false);
        }
      }
    })();

    return () => {
      live = false;
      viewerRef.current = null;
      findRef.current = null;
      destroy?.();
    };
  }, [contentUrl]);

  const goto = useCallback((page: number) => {
    const v = viewerRef.current;
    if (!v || page < 1 || page > v.pagesCount) return;
    v.currentPageNumber = page;
  }, []);

  const zoom = useCallback((factor: number) => {
    const v = viewerRef.current;
    if (!v) return;
    v.currentScaleValue = String(
      Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, v.currentScale * factor)),
    );
  }, []);

  const fitWidth = useCallback(() => {
    const v = viewerRef.current;
    if (v) v.currentScaleValue = "page-width";
  }, []);

  const find = useCallback(
    (q: string, findPrevious: boolean) => {
      setQuery(q);
      if (!q) setMatches({ current: 0, total: 0 });
      findRef.current?.dispatch(q, findPrevious);
    },
    [],
  );

  const ready = state.pages > 0;

  return (
    <div className="space-y-2" data-feedback-anchorable>
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>PDF document</span>
        {sizeBytes ? <span>· {formatBytes(sizeBytes)}</span> : null}

        {ready ? (
          <>
            <span className="ml-2 flex items-center gap-1">
              <Button
                variant="outline"
                size="icon-xs"
                onClick={() => goto(state.page - 1)}
                disabled={state.page <= 1}
                aria-label="Previous page"
              >
                <ChevronLeft />
              </Button>
              <span className="tabular-nums" data-testid="pdf-page-indicator">
                {state.page} / {state.pages}
              </span>
              <Button
                variant="outline"
                size="icon-xs"
                onClick={() => goto(state.page + 1)}
                disabled={state.page >= state.pages}
                aria-label="Next page"
              >
                <ChevronRight />
              </Button>
            </span>

            <span className="flex items-center gap-1">
              <Button variant="outline" size="icon-xs" onClick={() => zoom(1 / ZOOM_STEP)} aria-label="Zoom out">
                <ZoomOut />
              </Button>
              <Button
                variant="outline"
                size="xs"
                onClick={fitWidth}
                className="tabular-nums text-foreground"
                aria-label="Fit to width"
              >
                {Math.round(state.scale * 100)}%
              </Button>
              <Button variant="outline" size="icon-xs" onClick={() => zoom(ZOOM_STEP)} aria-label="Zoom in">
                <ZoomIn />
              </Button>
            </span>

            <span className="relative flex items-center">
              <Search className="pointer-events-none absolute left-2 size-3 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => find(e.target.value, false)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    find(query, true);
                  }
                }}
                placeholder="Find"
                aria-label="Find in document"
                className="h-6 w-32 pl-7 text-xs"
              />
              {query ? (
                <span className="ml-1 tabular-nums" data-testid="pdf-find-count">
                  {matches.total > 0 ? `${matches.current}/${matches.total}` : "none"}
                </span>
              ) : null}
            </span>
          </>
        ) : null}

        <Button asChild variant="outline" size="xs" className="ml-auto text-foreground">
          <a href={contentUrl} download={fileName}>
            <Download />
            Download
          </a>
        </Button>
      </div>

      {error ? (
        <div className="flex flex-col items-center gap-3 rounded-lg border bg-card p-8 text-center">
          <FileText className="h-8 w-8 text-muted-foreground" />
          <div>
            <p className="text-sm font-medium">This document could not be displayed</p>
            <p className="mt-1 text-xs text-muted-foreground">{error}</p>
          </div>
          <Button asChild>
            <a href={contentUrl} download={fileName}>
              <Download />
              Download
            </a>
          </Button>
        </div>
      ) : (
        // PDF.js positions pages absolutely inside the scroll container, so the
        // container needs a height of its own and `position: absolute` is what
        // its stylesheet expects; the wrapper supplies the box that height is
        // measured against.
        <div
          className="relative w-full overflow-hidden rounded-lg border bg-card"
          style={{ height: "min(80vh, 900px)" }}
        >
          <div ref={containerRef} className="absolute inset-0 overflow-auto" aria-label={fileName || "PDF document"}>
            <div className="pdfViewer" />
          </div>
          {loading ? (
            <div className="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground">
              Loading document...
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
