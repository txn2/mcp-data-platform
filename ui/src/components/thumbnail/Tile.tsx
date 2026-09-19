import { useEffect, useId, useMemo, useRef } from "react";
import Papa from "papaparse";
import DOMPurify from "dompurify";
import mermaid from "mermaid";
import { buildJsxThumbnailHtml, injectCaptureScript } from "@/lib/thumbnail";
import { THUMB_HEIGHT, THUMB_WIDTH, captureFamily } from "@/lib/thumbnailSupport";
import { CT, normalizeContentType } from "@/lib/contentType";
import { resolveRenderer } from "@/components/renderers/registry";
import { DARK_SCHEME, LIGHT_SCHEME } from "@/components/thumbnail/schemes";
import { buildJsonLines, buildNdjsonRecords } from "@/components/thumbnail/JsonThumbnailBody";
import { DomBody, type DomKind } from "@/components/thumbnail/DomThumbnailBody";

/**
 * One document, as the platform hands it to the page its tile is drawn from
 * (internal/platform/thumbworker, tilePage). A raster image travels by URL,
 * because its bytes are not a JSON string; everything else travels inline.
 */
export interface TileData {
  contentType: string;
  name: string;
  content?: string;
  contentURL: string;
  serveFromURL: boolean;
}

/**
 * Called once the tile is on screen: with "" when it is drawn, or with why it
 * cannot be. The platform screenshots the page on "" and records anything else
 * as the reason the file has no tile.
 */
export type OnDrawn = (reason: string) => void;

/**
 * The page a tile is drawn from.
 *
 * A headless Chrome beside the platform loads this page and takes a screenshot
 * of it (#1787). The browser paints it, so what the tile shows is what the
 * viewer shows: the families here are the same renderers and stylesheets, and
 * nothing reimplements CSS to get from them to pixels.
 */
export function Tile({ data, dark, onDrawn }: { data: TileData; dark: boolean; onDrawn: OnDrawn }) {
  const family = captureFamily(data.contentType);
  if (family === "iframe") return <DocumentTile data={data} onDrawn={onDrawn} />;
  if (family === "image") return <ImageTile data={data} onDrawn={onDrawn} />;
  const kind = domKind(data.contentType);
  if (kind) return <DomTile data={data} kind={kind} dark={dark} onDrawn={onDrawn} />;
  return <Unsupported contentType={data.contentType} onDrawn={onDrawn} />;
}

function Unsupported({ contentType, onDrawn }: { contentType: string; onDrawn: OnDrawn }) {
  useEffect(() => {
    onDrawn(`nothing draws ${contentType}`);
  }, [contentType, onDrawn]);
  return null;
}

/**
 * The family a DOM-drawn type is laid out as. NDJSON is told from JSON by the
 * normalized type: both spellings of it contain "json", and it is drawn as a
 * list of records rather than as one document.
 */
function domKind(contentType: string): DomKind | null {
  switch (captureFamily(contentType)) {
    case "svg":
      return "svg";
    case "csv":
      return "csv";
    case "json":
      return normalizeContentType(contentType) === CT.ndjson ? "ndjson" : "json";
    case "markdown":
      return "markdown";
    case "text":
      return "text";
    default:
      return null;
  }
}

/** Resolves after the browser has painted what is in the DOM now. */
function afterPaint(): Promise<void> {
  return new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
}

/**
 * A document that carries its own page: HTML and JSX, drawn in the frame the
 * viewer draws them in, at page size. The frame says when it has settled and
 * how many of the files it links to failed to load; a tile of a document whose
 * data never arrived is a picture of its error branch, so that is a reason, not
 * a tile.
 */
function DocumentTile({ data, onDrawn }: { data: TileData; onDrawn: OnDrawn }) {
  const content = data.content ?? "";
  const isJsx = data.contentType.toLowerCase().includes("jsx");
  const doc = useMemo(
    () => (isJsx ? buildJsxThumbnailHtml(content) : injectCaptureScript(content)),
    [content, isJsx],
  );

  useEffect(() => {
    function onMessage(e: MessageEvent) {
      if (e.origin !== window.location.origin || e.data?.type !== "thumbnail-ready") return;
      const failures = typeof e.data.refFailures === "number" ? e.data.refFailures : 0;
      if (failures > 0) {
        onDrawn(`${failures} file(s) this document links to could not be loaded`);
        return;
      }
      void afterPaint().then(() => onDrawn(""));
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [onDrawn]);

  return (
    <iframe
      sandbox="allow-scripts allow-same-origin"
      srcDoc={doc}
      title={data.name}
      style={{ display: "block", width: "100vw", height: "100vh", border: "none" }}
    />
  );
}

/** A raster image, scaled to cover the tile the way a card shows it. */
function ImageTile({ data, onDrawn }: { data: TileData; onDrawn: OnDrawn }) {
  return (
    <img
      src={data.contentURL}
      alt=""
      style={{ display: "block", width: THUMB_WIDTH, height: THUMB_HEIGHT, objectFit: "cover" }}
      onLoad={() => void afterPaint().then(() => onDrawn(""))}
      onError={() => onDrawn("the image could not be decoded")}
    />
  );
}

/**
 * The families the portal lays out on its own surface -- markdown, a table,
 * JSON, plain text, SVG -- drawn at tile size in the color scheme the page was
 * opened in.
 */
function DomTile({
  data,
  kind,
  dark,
  onDrawn,
}: {
  data: TileData;
  kind: DomKind;
  dark: boolean;
  onDrawn: OnDrawn;
}) {
  const content = data.content ?? "";
  const scheme = dark ? DARK_SCHEME : LIGHT_SCHEME;
  const scope = `tile-${useId().replace(/:/g, "")}`;
  const container = useRef<HTMLDivElement>(null);

  const csvTable = useMemo(() => {
    if (kind !== "csv") return null;
    const result = Papa.parse<Record<string, unknown>>(content, {
      header: true,
      skipEmptyLines: true,
      dynamicTyping: true,
      // The delimiter the viewer lays this file out with, so the tile is of the
      // table the reader opens (#1754).
      delimiter: resolveRenderer({ contentType: data.contentType }).delimiter ?? "",
    });
    return { cols: result.meta.fields ?? [], rows: result.data.slice(0, 10) };
  }, [content, data.contentType, kind]);
  const sanitizedSvg = useMemo(
    () => (kind === "svg" ? DOMPurify.sanitize(content, { USE_PROFILES: { svg: true, svgFilters: true } }) : ""),
    [content, kind],
  );
  const jsonLines = useMemo(() => (kind === "json" ? buildJsonLines(content) : null), [content, kind]);
  const ndjsonRecords = useMemo(() => (kind === "ndjson" ? buildNdjsonRecords(content) : null), [content, kind]);

  useEffect(() => {
    const el = container.current;
    if (!el) return;
    void renderMermaidIn(el, scheme.mermaidTheme, `${scope}-mermaid`)
      .then(afterPaint)
      .then(() => onDrawn(""), (err: unknown) => onDrawn(String(err)));
  }, [scheme.mermaidTheme, scope, onDrawn]);

  return (
    <div
      ref={container}
      style={{
        width: THUMB_WIDTH,
        height: THUMB_HEIGHT,
        overflow: "hidden",
        boxSizing: "border-box",
        background: scheme.tokens.bg,
        color: scheme.tokens.fg,
        fontSize: 12,
        padding: 16,
        lineHeight: 1.6,
        fontFamily: "system-ui, -apple-system, sans-serif",
      }}
    >
      <DomBody
        kind={kind}
        tokens={scheme.tokens}
        scope={scope}
        content={content}
        csvTable={csvTable}
        sanitizedSvg={sanitizedSvg}
        jsonLines={jsonLines}
        ndjsonRecords={ndjsonRecords}
      />
    </div>
  );
}

/** Replaces a markdown document's mermaid blocks with their diagrams. */
async function renderMermaidIn(
  container: HTMLElement,
  theme: "default" | "dark",
  idPrefix: string,
): Promise<void> {
  const blocks = container.querySelectorAll<HTMLElement>("code.language-mermaid");
  if (blocks.length === 0) return;
  mermaid.initialize({ startOnLoad: false, theme, fontFamily: "system-ui, sans-serif" });
  for (let i = 0; i < blocks.length; i++) {
    const codeEl = blocks[i]!;
    const preEl = codeEl.parentElement;
    if (!preEl || preEl.tagName !== "PRE") continue;
    try {
      const { svg } = await mermaid.render(`${idPrefix}-${i}`, codeEl.textContent || "");
      const wrapper = document.createElement("div");
      wrapper.innerHTML = svg;
      wrapper.style.display = "flex";
      wrapper.style.justifyContent = "center";
      wrapper.style.margin = "0.5em 0";
      preEl.replaceWith(wrapper);
    } catch {
      // A diagram that does not parse stays a code block, which is what the
      // viewer shows for it too.
    }
  }
}
