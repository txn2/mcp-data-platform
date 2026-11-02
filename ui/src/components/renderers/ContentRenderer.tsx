import { lazy, Suspense, useMemo, type ReactNode } from "react";
import { BinaryRenderer } from "./BinaryRenderer";
import { resolveRenderer, type Resolution } from "./registry";
import { pdfURLFor } from "@/lib/pdfExport";
import { usePortalAccess } from "@/hooks/usePortalAccess";

// Every family's viewer loads on demand. Only the registry, which decides
// which family a piece of content belongs to, and the metadata card that
// stands in for a family with nothing to show are resolved eagerly.
//
// The split matters most on the public share page, which serves one asset to
// someone who has never used the platform: a markdown document there now
// fetches the markdown viewer and nothing else, rather than CodeMirror, the
// JSX transformer, the CSV parser and the diagram engine as well.
const JsonRenderer = lazy(() => import("./JsonRenderer").then((m) => ({ default: m.JsonRenderer })));
const NdjsonRenderer = lazy(() => import("./NdjsonRenderer").then((m) => ({ default: m.NdjsonRenderer })));
const CodeRenderer = lazy(() => import("./CodeRenderer").then((m) => ({ default: m.CodeRenderer })));
const ImageRenderer = lazy(() => import("./ImageRenderer").then((m) => ({ default: m.ImageRenderer })));
const AudioRenderer = lazy(() => import("./MediaRenderer").then((m) => ({ default: m.AudioRenderer })));
const VideoRenderer = lazy(() => import("./MediaRenderer").then((m) => ({ default: m.VideoRenderer })));
// Its own chunk, not MediaRenderer's: the PDF viewer carries pdf.js and its
// worker, which an audio or video preview must not pay for.
const PdfRenderer = lazy(() => import("./PdfRenderer").then((m) => ({ default: m.PdfRenderer })));
// Its own chunk too: the Parquet viewer carries hyparquet and its decompressors.
const ParquetRenderer = lazy(() => import("./ParquetRenderer").then((m) => ({ default: m.ParquetRenderer })));
const JsxRenderer = lazy(() => import("./JsxRenderer").then((m) => ({ default: m.JsxRenderer })));
const HtmlRenderer = lazy(() => import("./HtmlRenderer").then((m) => ({ default: m.HtmlRenderer })));
const MarkdownRenderer = lazy(() => import("./MarkdownRenderer").then((m) => ({ default: m.MarkdownRenderer })));
const SvgRenderer = lazy(() => import("./SvgRenderer").then((m) => ({ default: m.SvgRenderer })));
const CsvRenderer = lazy(() => import("./CsvRenderer").then((m) => ({ default: m.CsvRenderer })));
// Each its own chunk: Graphviz carries its WebAssembly build, Vega its runtime.
const GraphvizRenderer = lazy(() => import("./GraphvizRenderer").then((m) => ({ default: m.GraphvizRenderer })));
const VegaLiteRenderer = lazy(() => import("./VegaLiteRenderer").then((m) => ({ default: m.VegaLiteRenderer })));
// The drawn sources of #2302, #2306 and #2308, each its own chunk.
const D2Renderer = lazy(() => import("./D2Renderer").then((m) => ({ default: m.D2Renderer })));
const ExcalidrawRenderer = lazy(() => import("./ExcalidrawRenderer").then((m) => ({ default: m.ExcalidrawRenderer })));
const LottieRenderer = lazy(() => import("./LottieRenderer").then((m) => ({ default: m.LottieRenderer })));

interface Props {
  contentType: string;
  /** The content itself, for families rendered from embedded text. */
  content?: string;
  fileName?: string;
  /**
   * URL of the raw content endpoint. Required for the binary families, which
   * are never embedded in the page; supplied by every surface that has one.
   */
  contentUrl?: string;
  sizeBytes?: number;
  /**
   * Where an HTML document's controls render when the page has a row for them
   * (#1769); the other families carry no controls.
   */
  controlsSlot?: HTMLElement | null;
  /**
   * An editor's change to a family edited in place, an Excalidraw scene
   * (#2306). Absent, the family is drawn read-only.
   */
  onEdit?: (text: string) => void;
}

const Loading = () => (
  <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">Loading viewer...</div>
);

/**
 * Renders a piece of content with the viewer its family calls for.
 *
 * The family comes from the shared registry, which resolves the declared type,
 * then the filename, then the content itself, so an asset stored under a
 * generic type before the server settled types at write time still reaches the
 * right viewer.
 */
export function ContentRenderer({ contentType, content, fileName, contentUrl, sizeBytes, controlsSlot, onEdit }: Props) {
  // Resolution scans a prefix of the content when the declared type is
  // generic, so it is memoized rather than repeated on every parent re-render.
  const entry = useMemo(
    () => resolveRenderer({ contentType, fileName, content }),
    [contentType, fileName, content],
  );

  // Export PDF is offered only to a persona with PDF export (#2225, #2257);
  // the PDF route refuses the rest.
  const { download } = usePortalAccess();

  if (entry.source === "url") {
    return renderFromURL(entry, { fileName, contentUrl, sizeBytes });
  }
  return renderFromText(entry, content ?? "", {
    fileName,
    controlsSlot,
    pdfUrl: download ? pdfURLFor(contentUrl) : undefined,
    contentUrl,
    onEdit,
  });
}

interface URLProps {
  fileName?: string;
  contentUrl?: string;
  sizeBytes?: number;
}

/**
 * The families whose renderers point an element at the content endpoint. A
 * missing URL leaves nothing to render, so the metadata card stands in rather
 * than a broken element.
 */
function renderFromURL(entry: Resolution, { fileName, contentUrl, sizeBytes }: URLProps): ReactNode {
  const card = (
    <BinaryRenderer
      contentType={entry.contentType}
      contentUrl={contentUrl}
      fileName={fileName}
      sizeBytes={sizeBytes}
    />
  );
  if (!contentUrl || entry.kind === "binary") return card;

  const common = { contentUrl, fileName, sizeBytes };
  const media: Partial<Record<Resolution["kind"], ReactNode>> = {
    image: <ImageRenderer {...common} contentType={entry.contentType} />,
    audio: <AudioRenderer {...common} contentType={entry.contentType} />,
    video: <VideoRenderer {...common} contentType={entry.contentType} />,
    pdf: <PdfRenderer {...common} />,
    parquet: <ParquetRenderer {...common} />,
    dotlottie: <LottieRenderer contentUrl={contentUrl} />,
  };

  return <Suspense fallback={<Loading />}>{media[entry.kind] ?? card}</Suspense>;
}

/** What a family that renders from text is handed. */
interface TextViewProps {
  entry: Resolution;
  text: string;
  fileName?: string;
  controlsSlot?: HTMLElement | null;
  pdfUrl?: string;
  /** The stored document's route, which a D2 drawing is read from. */
  contentUrl?: string;
  /** An editor's change to a scene edited in place (#2306). */
  onEdit?: (text: string) => void;
}

/** The viewer each family that renders from text already in hand is drawn by. */
const TEXT_VIEWERS: Partial<Record<Resolution["kind"], (p: TextViewProps) => ReactNode>> = {
  json: ({ text, fileName }) => <JsonRenderer content={text} fileName={fileName} />,
  ndjson: ({ text, fileName }) => <NdjsonRenderer content={text} fileName={fileName} />,
  code: ({ entry, text, fileName }) => <CodeRenderer content={text} language={entry.language} fileName={fileName} />,
  table: ({ entry, text, fileName }) => <CsvRenderer content={text} fileName={fileName} delimiter={entry.delimiter} />,
  jsx: ({ text }) => <JsxRenderer content={text} />,
  svg: ({ text }) => <SvgRenderer content={text} />,
  markdown: ({ text }) => <MarkdownRenderer content={text} />,
  graphviz: ({ text }) => <GraphvizRenderer content={text} />,
  vegalite: ({ text }) => <VegaLiteRenderer content={text} />,
  d2: ({ text, contentUrl }) => <D2Renderer content={text} contentUrl={contentUrl} />,
  excalidraw: ({ text, onEdit }) => <ExcalidrawRenderer content={text} onEdit={onEdit} />,
  lottie: ({ text }) => <LottieRenderer content={text} />,
  html: ({ text, controlsSlot, pdfUrl }) => <HtmlRenderer content={text} controlsSlot={controlsSlot} pdfUrl={pdfUrl} />,
};

/** The families that render from text already in hand. */
function renderFromText(entry: Resolution, text: string, opts: Omit<TextViewProps, "entry" | "text">): ReactNode {
  const view = TEXT_VIEWERS[entry.kind];
  if (view) {
    return <Suspense fallback={<Loading />}>{view({ entry, text, ...opts })}</Suspense>;
  }
  return (
    <pre
      data-feedback-anchorable
      className="overflow-auto whitespace-pre-wrap rounded-lg border bg-card p-6 text-sm"
    >
      {text}
    </pre>
  );
}
