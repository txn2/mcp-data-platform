import { useEffect, useRef, useCallback, useMemo } from "react";
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
} from "@/lib/thumbnail";

interface Props {
  assetId: string;
  content: string;
  contentType: string;
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
export function ThumbnailGenerator({ assetId, content, contentType, onCaptured, onFailed }: Props) {
  const ct = contentType.toLowerCase();
  const isIframeType = ct.includes("html") || ct.includes("jsx");
  const isMarkdown = ct.includes("markdown");
  const isSvg = ct.includes("svg");
  const isCsv = ct.includes("csv");

  if (isIframeType) {
    return (
      <IframeCapture
        assetId={assetId}
        content={content}
        contentType={contentType}
        onCaptured={onCaptured}
        onFailed={onFailed}
      />
    );
  }

  if (isMarkdown || isSvg || isCsv) {
    return (
      <DomCapture
        assetId={assetId}
        content={content}
        contentType={contentType}
        onCaptured={onCaptured}
        onFailed={onFailed}
      />
    );
  }

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
  onCaptured,
  onFailed,
}: {
  assetId: string;
  content: string;
  contentType: string;
  onCaptured?: () => void;
  onFailed?: () => void;
}) {
  const capturedRef = useRef(false);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const isJsx = contentType.toLowerCase().includes("jsx");

  const blobUrl = useMemo(() => {
    const html = isJsx ? buildJsxThumbnailHtml(content) : injectCaptureScript(content);
    const blob = new Blob([html], { type: "text/html;charset=utf-8" });
    return URL.createObjectURL(blob);
  }, [content, isJsx]);

  const doCapture = useCallback(async () => {
    if (capturedRef.current || !iframeRef.current) return;
    capturedRef.current = true;
    try {
      const blob = await captureIframe(iframeRef.current);
      await uploadThumbnail(assetId, blob);
      onCaptured?.();
    } catch {
      onFailed?.();
    }
  }, [assetId, onCaptured, onFailed]);

  useEffect(() => {
    function handleMessage(e: MessageEvent) {
      // With allow-same-origin, blob: iframes inherit the parent's origin
      if (e.origin !== window.location.origin) return;
      if (e.data?.type !== "thumbnail-ready") return;
      void doCapture();
    }

    window.addEventListener("message", handleMessage);
    return () => window.removeEventListener("message", handleMessage);
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

/**
 * Captures same-origin DOM content (Markdown/SVG) using html2canvas.
 */
function DomCapture({
  assetId,
  content,
  contentType,
  onCaptured,
  onFailed,
}: {
  assetId: string;
  content: string;
  contentType: string;
  onCaptured?: () => void;
  onFailed?: () => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const capturedRef = useRef(false);

  const isSvg = contentType.toLowerCase().includes("svg");
  const isCsvThumb = contentType.toLowerCase().includes("csv");

  const csvTable = useMemo(() => {
    if (!isCsvThumb) return null;
    const result = Papa.parse<Record<string, unknown>>(content, {
      header: true,
      skipEmptyLines: true,
      dynamicTyping: true,
    });
    const cols = result.meta.fields ?? [];
    const rows = result.data.slice(0, 10);
    return { cols, rows };
  }, [content, isCsvThumb]);

  const sanitizedSvg = useMemo(
    () => (isSvg ? DOMPurify.sanitize(content, { USE_PROFILES: { svg: true, svgFilters: true } }) : ""),
    [content, isSvg],
  );

  const doCapture = useCallback(async () => {
    if (capturedRef.current || !containerRef.current) return;
    capturedRef.current = true;
    try {
      const canvas = await html2canvas(containerRef.current, {
        width: THUMB_WIDTH,
        height: THUMB_HEIGHT,
        scale: 1,
        logging: false,
      });
      const blob = await new Promise<Blob>((resolve, reject) => {
        canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob returned null"))), "image/png");
      });
      await uploadThumbnail(assetId, blob);
      onCaptured?.();
    } catch {
      onFailed?.();
    }
  }, [assetId, onCaptured, onFailed]);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    // Capture as non-null for the async closure (TS can't narrow across await).
    const container: HTMLDivElement = el;

    async function renderMermaidAndCapture() {
      // Wait for ReactMarkdown to render child nodes
      await new Promise<void>((resolve) => {
        if (container.querySelector("p, h1, h2, h3, li, pre, blockquote, table, svg")) {
          resolve();
          return;
        }
        const observer = new MutationObserver(() => {
          if (container.querySelector("p, h1, h2, h3, li, pre, blockquote, table, svg")) {
            observer.disconnect();
            resolve();
          }
        });
        observer.observe(container, { childList: true, subtree: true });
      });

      // Render mermaid code blocks if present
      const mermaidBlocks = container.querySelectorAll<HTMLElement>("code.language-mermaid");
      if (mermaidBlocks.length > 0) {
        mermaid.initialize({ startOnLoad: false, theme: "default", fontFamily: "system-ui, sans-serif" });
        for (let i = 0; i < mermaidBlocks.length; i++) {
          const codeEl = mermaidBlocks[i]!;
          const preEl = codeEl.parentElement;
          if (!preEl || preEl.tagName !== "PRE") continue;
          try {
            const { svg } = await mermaid.render(`thumb-mermaid-${i}`, codeEl.textContent || "");
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

      // Let layout settle after mermaid SVGs are inserted
      await new Promise((r) => requestAnimationFrame(r));
      void doCapture();
    }

    void renderMermaidAndCapture();
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
    <div
      ref={containerRef}
      style={{
        position: "fixed",
        left: -9999,
        top: -9999,
        width: THUMB_WIDTH,
        height: THUMB_HEIGHT,
        overflow: "hidden",
        pointerEvents: "none",
        background: "white",
        color: "black",
        fontSize: 12,
        padding: 16,
        lineHeight: 1.6,
        fontFamily: "system-ui, -apple-system, sans-serif",
      }}
      aria-hidden="true"
    >
      {isCsvThumb && csvTable ? (
        <div>
          <style>{`
            .thumb-prose table { border-collapse: collapse; margin: 0.4em 0; width: 100%; }
            .thumb-prose th, .thumb-prose td { border: 1px solid #d1d5db; padding: 0.25em 0.5em; font-size: 0.85em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 120px; }
            .thumb-prose th { background: #f1f5f9; font-weight: 600; }
            .thumb-prose tr:nth-child(even) { background: #f8fafc; }
          `}</style>
          <div className="thumb-prose">
            <table>
              <thead>
                <tr>
                  {csvTable.cols.map((col) => (
                    <th key={col}>{col}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {csvTable.rows.map((row, i) => (
                  <tr key={i}>
                    {csvTable.cols.map((col) => (
                      <td key={col}>{String(row[col] ?? "")}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : isSvg ? (
        <div dangerouslySetInnerHTML={{ __html: sanitizedSvg }} />
      ) : (
        <div
          style={{
            maxWidth: "none",
          }}
        >
          <style>{`
            .thumb-prose h1 { font-size: 1.5em; font-weight: 700; margin: 0.5em 0 0.25em; }
            .thumb-prose h2 { font-size: 1.25em; font-weight: 600; margin: 0.5em 0 0.25em; }
            .thumb-prose h3 { font-size: 1.1em; font-weight: 600; margin: 0.4em 0 0.2em; }
            .thumb-prose p { margin: 0.4em 0; }
            .thumb-prose ul, .thumb-prose ol { padding-left: 1.5em; margin: 0.4em 0; }
            .thumb-prose code { background: #f3f4f6; padding: 0.1em 0.3em; border-radius: 3px; font-size: 0.9em; }
            .thumb-prose pre { background: #f3f4f6; padding: 0.5em; border-radius: 4px; overflow: auto; margin: 0.4em 0; }
            .thumb-prose blockquote { border-left: 3px solid #d1d5db; padding-left: 0.75em; margin: 0.4em 0; color: #6b7280; }
            .thumb-prose a { color: #2563eb; text-decoration: underline; }
            .thumb-prose table { border-collapse: collapse; margin: 0.4em 0; }
            .thumb-prose th, .thumb-prose td { border: 1px solid #d1d5db; padding: 0.25em 0.5em; font-size: 0.9em; }
          `}</style>
          <div className="thumb-prose">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
          </div>
        </div>
      )}
    </div>
  );
}
