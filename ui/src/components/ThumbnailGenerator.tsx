import { useEffect, useRef, useCallback, useMemo, useId } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import Papa from "papaparse";
import DOMPurify from "dompurify";
import html2canvas from "html2canvas";
import mermaid from "mermaid";
import {
  THUMB_WIDTH,
  THUMB_HEIGHT,
  RENDER_WIDTH,
  RENDER_HEIGHT,
  CAPTURE_TIMEOUT_MS,
  injectCaptureScript,
  buildJsxThumbnailHtml,
  captureIframe,
  uploadThumbnail,
  isThemeable,
} from "@/lib/thumbnail";
import { CT, normalizeContentType } from "@/lib/contentType";
import {
  LIGHT_SCHEME,
  DARK_SCHEME,
  csvProseCss,
  markdownProseCss,
  type ProseTokens,
  type Scheme,
} from "@/components/thumbnail/schemes";
import {
  buildJsonLines,
  buildNdjsonRecords,
  JsonThumbnailBody,
  NdjsonThumbnailBody,
  type JsonThumbnailLines,
  type NdjsonThumbnailRecord,
} from "@/components/thumbnail/JsonThumbnailBody";

interface Props {
  assetId: string;
  content: string;
  contentType: string;
  /**
   * The asset version `content` was read at. Recorded with the capture so the
   * asset row dates the image to what it actually shows; omitted, the server
   * dates it to whatever version the asset is on when the upload lands.
   */
  version?: number;
  onCaptured?: () => void;
  onFailed?: () => void;
}

/**
 * Hidden off-screen component that renders content, captures a PNG thumbnail,
 * and uploads it to the server. Renders nothing visible to the user.
 *
 * Calls onFailed (or onCaptured) after CAPTURE_TIMEOUT_MS if capture hasn't
 * completed, so the caller can move on.
 */
export function ThumbnailGenerator({ assetId, content, contentType, version, onCaptured, onFailed }: Props) {
  const ct = contentType.toLowerCase();

  if (ct.includes("html") || ct.includes("jsx")) {
    return (
      <IframeCapture
        assetId={assetId}
        content={content}
        contentType={contentType}
        version={version}
        onCaptured={onCaptured}
        onFailed={onFailed}
      />
    );
  }

  if (domKind(contentType) !== null) {
    return (
      <DomCapture
        assetId={assetId}
        content={content}
        contentType={contentType}
        version={version}
        onCaptured={onCaptured}
        onFailed={onFailed}
      />
    );
  }

  return null;
}

/** The families drawn into the page and rasterized, rather than into an iframe. */
type DomKind = "csv" | "svg" | "json" | "ndjson" | "markdown";

/**
 * The family a content type is drawn as, or null when the capturer has no
 * rendering for it. The tests are substring tests for the same reason
 * lib/thumbnailSupport's are: a stored type carries parameters and vendor
 * prefixes ("text/markdown; charset=utf-8", "application/vnd.acme+json").
 *
 * NDJSON is separated from JSON by the normalized type rather than by a
 * substring, because it is a stream of independent documents drawn as a list of
 * records and its spellings ("application/x-ndjson", "application/jsonl") both
 * contain "json". What this returns must stay a subset of what
 * isThumbnailSupported admits, or the queue offers an asset nothing can draw.
 */
function domKind(contentType: string): DomKind | null {
  const ct = contentType.toLowerCase();
  if (ct.includes("svg")) return "svg";
  if (ct.includes("csv")) return "csv";
  if (normalizeContentType(contentType) === CT.ndjson) return "ndjson";
  if (ct.includes("json")) return "json";
  if (ct.includes("markdown")) return "markdown";
  return null;
}

/**
 * Captures iframe-based content (HTML/JSX) using the bundled html2canvas.
 * The iframe sends a "thumbnail-ready" postMessage when loaded; the parent
 * then captures the iframe content directly.
 */
function IframeCapture({
  assetId,
  content,
  contentType,
  version,
  onCaptured,
  onFailed,
}: {
  assetId: string;
  content: string;
  contentType: string;
  version?: number;
  onCaptured?: () => void;
  onFailed?: () => void;
}) {
  const capturedRef = useRef(false);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const isJsx = contentType.toLowerCase().includes("jsx");

  const blobUrl = useMemo(() => {
    const html = isJsx
      ? buildJsxThumbnailHtml(content, assetId)
      : injectCaptureScript(content, assetId);
    const blob = new Blob([html], { type: "text/html;charset=utf-8" });
    return URL.createObjectURL(blob);
  }, [assetId, content, isJsx]);

  // A frame that reported a failed reference load is not stored. The pixels
  // cannot say so -- an artifact whose referenced logo and data file did not
  // load draws its own failure branch and rasterizes to a valid PNG, which is
  // what used to be uploaded and shown on the card (#1497). Discarding leaves
  // the asset on the server's pending list, so the next tab to go idle over it
  // tries again rather than the reader being stuck with a picture of an error.
  const doCapture = useCallback(async (refFailures: number) => {
    if (capturedRef.current || !iframeRef.current) return;
    capturedRef.current = true;
    if (refFailures > 0) {
      onFailed?.();
      return;
    }
    try {
      const blob = await captureIframe(iframeRef.current);
      await uploadThumbnail(assetId, blob, "light", version);
      onCaptured?.();
    } catch {
      onFailed?.();
    }
  }, [assetId, version, onCaptured, onFailed]);

  useEffect(() => {
    function handleMessage(e: MessageEvent) {
      // With allow-same-origin, blob: iframes inherit the parent's origin
      if (e.origin !== window.location.origin) return;
      if (e.data?.type !== "thumbnail-ready") return;
      // The refresh queue and the viewer both mount a capturer, so two frames
      // can be listening on this window at once. A message that named no asset
      // was read by both, which now means one artifact's failed references
      // could discard another's good capture.
      if (e.data.assetId && e.data.assetId !== assetId) return;
      const failures = typeof e.data.refFailures === "number" ? e.data.refFailures : 0;
      void doCapture(failures);
    }

    window.addEventListener("message", handleMessage);
    return () => window.removeEventListener("message", handleMessage);
  }, [assetId, doCapture]);

  // Timeout: if capture hasn't completed, give up
  useEffect(() => {
    const timer = setTimeout(() => {
      if (!capturedRef.current) {
        capturedRef.current = true;
        onFailed?.();
      }
    }, CAPTURE_TIMEOUT_MS);
    return () => clearTimeout(timer);
  }, [onFailed]);

  useEffect(() => {
    return () => URL.revokeObjectURL(blobUrl);
  }, [blobUrl]);

  return (
    <div
      style={{
        position: "fixed",
        left: -9999,
        top: -9999,
        width: RENDER_WIDTH,
        height: RENDER_HEIGHT,
        overflow: "hidden",
        pointerEvents: "none",
      }}
      aria-hidden="true"
    >
      <iframe
        ref={iframeRef}
        sandbox="allow-scripts allow-same-origin"
        src={blobUrl}
        width={RENDER_WIDTH}
        height={RENDER_HEIGHT}
        style={{ border: "none" }}
        title="Thumbnail capture"
      />
    </div>
  );
}

const SETTLE_SELECTOR = "p, h1, h2, h3, li, pre, blockquote, table, svg";

/** Resolves once the container has rendered capturable content. */
function waitForContent(container: HTMLElement): Promise<void> {
  return new Promise<void>((resolve) => {
    if (container.querySelector(SETTLE_SELECTOR)) {
      resolve();
      return;
    }
    const observer = new MutationObserver(() => {
      if (container.querySelector(SETTLE_SELECTOR)) {
        observer.disconnect();
        resolve();
      }
    });
    observer.observe(container, { childList: true, subtree: true });
  });
}

/** Replaces mermaid code blocks in a container with rendered SVG in the given theme. */
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
      // Leave as code block on failure
    }
  }
}

/** Captures a container to a PNG blob on the given background color. */
async function captureContainer(container: HTMLElement, bg: string): Promise<Blob> {
  const canvas = await html2canvas(container, {
    width: THUMB_WIDTH,
    height: THUMB_HEIGHT,
    scale: 1,
    logging: false,
    backgroundColor: bg,
  });
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob returned null"))), "image/png");
  });
}

/**
 * Captures same-origin DOM content (Markdown, CSV, SVG, JSON, NDJSON) using
 * html2canvas. Themeable families are captured twice (light + dark) and uploaded
 * to their respective variants; SVG carries its own colors and is captured once.
 */
function DomCapture({
  assetId,
  content,
  contentType,
  version,
  onCaptured,
  onFailed,
}: {
  assetId: string;
  content: string;
  contentType: string;
  version?: number;
  onCaptured?: () => void;
  onFailed?: () => void;
}) {
  const containerRefs = useRef<(HTMLDivElement | null)[]>([]);
  const capturedRef = useRef(false);

  // Per-instance prefix for the prose scope class. Combined with the scheme
  // variant below, this isolates each capture's injected CSS so neither the
  // light/dark pair nor concurrently-mounted generators for other assets can
  // clobber each other's colors. useId() can contain ":" which is invalid in a
  // class name, so strip it.
  const scopeBase = `tg-${useId().replace(/:/g, "")}`;

  // Markdown is the fallback the dispatch above already ruled a family for; the
  // coalesce is here only so the kind is not nullable downstream.
  const kind = domKind(contentType) ?? "markdown";

  // Themeable types capture both schemes; single-theme types capture light only.
  const schemes = useMemo<Scheme[]>(
    () => (isThemeable(contentType) ? [LIGHT_SCHEME, DARK_SCHEME] : [LIGHT_SCHEME]),
    [contentType],
  );

  // The document is parsed once per capture, not once per scheme: both scheme
  // containers are mounted at the same time and draw the same content.
  const csvTable = useMemo(() => {
    if (kind !== "csv") return null;
    const result = Papa.parse<Record<string, unknown>>(content, {
      header: true,
      skipEmptyLines: true,
      dynamicTyping: true,
    });
    const cols = result.meta.fields ?? [];
    const rows = result.data.slice(0, 10);
    return { cols, rows };
  }, [content, kind]);

  const sanitizedSvg = useMemo(
    () => (kind === "svg" ? DOMPurify.sanitize(content, { USE_PROFILES: { svg: true, svgFilters: true } }) : ""),
    [content, kind],
  );

  const jsonLines = useMemo(() => (kind === "json" ? buildJsonLines(content) : null), [content, kind]);

  const ndjsonRecords = useMemo(
    () => (kind === "ndjson" ? buildNdjsonRecords(content) : null),
    [content, kind],
  );

  const doCapture = useCallback(async () => {
    if (capturedRef.current) return;
    capturedRef.current = true;
    // Capture each variant independently so a failure on one (e.g. the dark
    // pass throwing in html2canvas) does not discard a variant that already
    // uploaded. Report success if ANY variant landed, so the queue invalidates
    // and shows what we have; a still-missing variant is re-queued on next load.
    let anySucceeded = false;
    for (let i = 0; i < schemes.length; i++) {
      const container = containerRefs.current[i];
      const scheme = schemes[i];
      if (!container || !scheme) continue;
      try {
        await waitForContent(container);
        await renderMermaidIn(container, scheme.mermaidTheme, `thumb-mermaid-${scheme.variant}`);
        // Let layout settle after mermaid SVGs are inserted
        await new Promise((r) => requestAnimationFrame(r));
        const blob = await captureContainer(container, scheme.tokens.bg);
        await uploadThumbnail(assetId, blob, scheme.variant, version);
        anySucceeded = true;
      } catch {
        // Skip this variant; other variants and a later retry can still fill it.
      }
    }
    if (anySucceeded) {
      onCaptured?.();
    } else {
      onFailed?.();
    }
  }, [assetId, schemes, version, onCaptured, onFailed]);

  useEffect(() => {
    void doCapture();
  }, [doCapture]);

  // Timeout: if capture hasn't completed, give up
  useEffect(() => {
    const timer = setTimeout(() => {
      if (!capturedRef.current) {
        capturedRef.current = true;
        onFailed?.();
      }
    }, CAPTURE_TIMEOUT_MS);
    return () => clearTimeout(timer);
  }, [onFailed]);

  return (
    <>
      {schemes.map((scheme, i) => {
        const scope = `${scopeBase}-${scheme.variant}`;
        return (
        <div
          key={scheme.variant}
          ref={(el) => {
            containerRefs.current[i] = el;
          }}
          style={{
            position: "fixed",
            left: -9999,
            top: -9999,
            width: THUMB_WIDTH,
            height: THUMB_HEIGHT,
            overflow: "hidden",
            pointerEvents: "none",
            background: scheme.tokens.bg,
            color: scheme.tokens.fg,
            fontSize: 12,
            padding: 16,
            lineHeight: 1.6,
            fontFamily: "system-ui, -apple-system, sans-serif",
          }}
          aria-hidden="true"
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
      })}
    </>
  );
}

/** One asset's content as the family it belongs to, for one color scheme. */
function DomBody({
  kind,
  tokens,
  scope,
  content,
  csvTable,
  sanitizedSvg,
  jsonLines,
  ndjsonRecords,
}: {
  kind: DomKind;
  tokens: ProseTokens;
  scope: string;
  content: string;
  csvTable: CsvTable | null;
  sanitizedSvg: string;
  jsonLines: JsonThumbnailLines | null;
  ndjsonRecords: NdjsonThumbnailRecord[] | null;
}) {
  if (kind === "csv" && csvTable) return <CsvBody table={csvTable} tokens={tokens} scope={scope} />;
  if (kind === "svg") return <div dangerouslySetInnerHTML={{ __html: sanitizedSvg }} />;
  if (kind === "json" && jsonLines) return <JsonThumbnailBody lines={jsonLines} tokens={tokens} scope={scope} />;
  if (kind === "ndjson" && ndjsonRecords) {
    return <NdjsonThumbnailBody records={ndjsonRecords} tokens={tokens} scope={scope} />;
  }
  return (
    <div style={{ maxWidth: "none" }}>
      <style>{markdownProseCss(tokens, scope)}</style>
      <div className={scope}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
      </div>
    </div>
  );
}

/** The parsed head of a CSV document: its header row and the rows drawn under it. */
interface CsvTable {
  cols: string[];
  rows: Record<string, unknown>[];
}

function CsvBody({ table, tokens, scope }: { table: CsvTable; tokens: ProseTokens; scope: string }) {
  return (
    <div>
      <style>{csvProseCss(tokens, scope)}</style>
      <div className={scope}>
        <table>
          <thead>
            <tr>
              {table.cols.map((col) => (
                <th key={col}>{col}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {table.rows.map((row, ri) => (
              <tr key={ri}>
                {table.cols.map((col) => (
                  <td key={col}>{String(row[col] ?? "")}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
