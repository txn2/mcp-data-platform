import { lazy, Suspense, useMemo, type ReactNode } from "react";
import { JsxRenderer } from "./JsxRenderer";
import { HtmlRenderer } from "./HtmlRenderer";
import { MarkdownRenderer } from "./MarkdownRenderer";
import { SvgRenderer } from "./SvgRenderer";
import { CsvRenderer } from "./CsvRenderer";
import { BinaryRenderer } from "./BinaryRenderer";
import { resolveRenderer, type Resolution } from "./registry";

// The heavy viewers load on demand: CodeMirror, the virtualizer and the JSON
// tree are only pulled in when an asset of that family is actually opened.
const JsonRenderer = lazy(() => import("./JsonRenderer").then((m) => ({ default: m.JsonRenderer })));
const NdjsonRenderer = lazy(() => import("./NdjsonRenderer").then((m) => ({ default: m.NdjsonRenderer })));
const CodeRenderer = lazy(() => import("./CodeRenderer").then((m) => ({ default: m.CodeRenderer })));
const ImageRenderer = lazy(() => import("./ImageRenderer").then((m) => ({ default: m.ImageRenderer })));
const AudioRenderer = lazy(() => import("./MediaRenderer").then((m) => ({ default: m.AudioRenderer })));
const VideoRenderer = lazy(() => import("./MediaRenderer").then((m) => ({ default: m.VideoRenderer })));
const PdfRenderer = lazy(() => import("./MediaRenderer").then((m) => ({ default: m.PdfRenderer })));

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
export function ContentRenderer({ contentType, content, fileName, contentUrl, sizeBytes }: Props) {
  // Resolution scans a prefix of the content when the declared type is
  // generic, so it is memoized rather than repeated on every parent re-render.
  const entry = useMemo(
    () => resolveRenderer({ contentType, fileName, content }),
    [contentType, fileName, content],
  );

  if (entry.source === "url") {
    return renderFromURL(entry, { fileName, contentUrl, sizeBytes });
  }
  return renderFromText(entry, content ?? "", fileName);
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
  };

  return <Suspense fallback={<Loading />}>{media[entry.kind] ?? card}</Suspense>;
}

/** The families that render from text already in hand. */
function renderFromText(entry: Resolution, text: string, fileName?: string): ReactNode {
  switch (entry.kind) {
    case "json":
      return (
        <Suspense fallback={<Loading />}>
          <JsonRenderer content={text} fileName={fileName} />
        </Suspense>
      );
    case "ndjson":
      return (
        <Suspense fallback={<Loading />}>
          <NdjsonRenderer content={text} fileName={fileName} />
        </Suspense>
      );
    case "code":
      return (
        <Suspense fallback={<Loading />}>
          <CodeRenderer content={text} language={entry.language} fileName={fileName} />
        </Suspense>
      );
    case "table":
      return <CsvRenderer content={text} fileName={fileName} delimiter={entry.delimiter} />;
    case "jsx":
      return <JsxRenderer content={text} />;
    case "svg":
      return <SvgRenderer content={text} />;
    case "markdown":
      return <MarkdownRenderer content={text} />;
    case "html":
      return <HtmlRenderer content={text} />;
    default:
      return (
        <pre
          data-feedback-anchorable
          className="overflow-auto whitespace-pre-wrap rounded-lg border bg-card p-6 text-sm"
        >
          {text}
        </pre>
      );
  }
}
