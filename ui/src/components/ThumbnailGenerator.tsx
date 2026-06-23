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
  type ThumbnailVariant,
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

/** Color tokens for one thumbnail color scheme. */
interface ProseTokens {
  bg: string;
  fg: string;
  codeBg: string;
  border: string;
  blockquoteBorder: string;
  muted: string;
  link: string;
  thBg: string;
  evenRow: string;
}

interface Scheme {
  variant: ThumbnailVariant;
  mermaidTheme: "default" | "dark";
  tokens: ProseTokens;
}

const LIGHT_SCHEME: Scheme = {
  variant: "light",
  mermaidTheme: "default",
  tokens: {
    bg: "#ffffff",
    fg: "#111827",
    codeBg: "#f3f4f6",
    border: "#d1d5db",
    blockquoteBorder: "#d1d5db",
    muted: "#6b7280",
    link: "#2563eb",
    thBg: "#f1f5f9",
    evenRow: "#f8fafc",
  },
};

// Dark tokens mirror the portal's shadcn dark palette so the captured thumbnail
// blends into the dark card. html2canvas needs concrete colors (it cannot
// resolve CSS custom properties off-DOM), so these are hardcoded; keep them in
// sync with the `.dark` block in src/index.css (--card -> #020817,
// --card-foreground -> #f8fafc, --border/--muted/...) if that palette changes.
const DARK_SCHEME: Scheme = {
  variant: "dark",
  mermaidTheme: "dark",
  tokens: {
    bg: "#020817",
    fg: "#f8fafc",
    codeBg: "#1e293b",
    border: "#334155",
    blockquoteBorder: "#475569",
    muted: "#94a3b8",
    link: "#60a5fa",
    thBg: "#1e293b",
    evenRow: "#0f172a",
  },
};

// tableBaseCss is the table border/spacing shared by the markdown and CSV prose
// variants; each adds its own cell sizing on top.
//
// All prose CSS is scoped to a caller-supplied `scope` class rather than a
// shared `.thumb-prose`. The light and dark scheme containers are mounted into
// the document at the same time, so a shared global selector would let the
// later-rendered scheme's colors win for BOTH captures (the dark code/cell
// backgrounds bled into the light thumbnail, rendering inline code as near-black
// boxes). A unique scope per scheme keeps each capture's styles isolated.
function tableBaseCss(t: ProseTokens, scope: string): string {
  return `
    .${scope} table { border-collapse: collapse; margin: 0.4em 0; }
    .${scope} th, .${scope} td { border: 1px solid ${t.border}; padding: 0.25em 0.5em; }`;
}

function markdownProseCss(t: ProseTokens, scope: string): string {
  return `
    .${scope} h1 { font-size: 1.5em; font-weight: 700; margin: 0.5em 0 0.25em; }
    .${scope} h2 { font-size: 1.25em; font-weight: 600; margin: 0.5em 0 0.25em; }
    .${scope} h3 { font-size: 1.1em; font-weight: 600; margin: 0.4em 0 0.2em; }
    .${scope} p { margin: 0.4em 0; }
    .${scope} ul, .${scope} ol { padding-left: 1.5em; margin: 0.4em 0; }
    .${scope} code { background: ${t.codeBg}; padding: 0.1em 0.3em; border-radius: 3px; font-size: 0.9em; }
    .${scope} pre { background: ${t.codeBg}; padding: 0.5em; border-radius: 4px; overflow: auto; margin: 0.4em 0; }
    .${scope} blockquote { border-left: 3px solid ${t.blockquoteBorder}; padding-left: 0.75em; margin: 0.4em 0; color: ${t.muted}; }
    .${scope} a { color: ${t.link}; text-decoration: underline; }
    ${tableBaseCss(t, scope)}
    .${scope} th, .${scope} td { font-size: 0.9em; }
  `;
}

function csvProseCss(t: ProseTokens, scope: string): string {
  return `
    ${tableBaseCss(t, scope)}
    .${scope} table { width: 100%; }
    .${scope} th, .${scope} td { font-size: 0.85em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 120px; }
    .${scope} th { background: ${t.thBg}; font-weight: 600; }
    .${scope} tr:nth-child(even) { background: ${t.evenRow}; }
  `;
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
 * Captures same-origin DOM content (Markdown/CSV/SVG) using html2canvas.
 * Themeable types (markdown, CSV) are captured twice (light + dark) and uploaded
 * to their respective variants; SVG carries its own colors and is captured once.
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
  const containerRefs = useRef<(HTMLDivElement | null)[]>([]);
  const capturedRef = useRef(false);

  // Per-instance prefix for the prose scope class. Combined with the scheme
  // variant below, this isolates each capture's injected CSS so neither the
  // light/dark pair nor concurrently-mounted generators for other assets can
  // clobber each other's colors. useId() can contain ":" which is invalid in a
  // class name, so strip it.
  const scopeBase = `tg-${useId().replace(/:/g, "")}`;

  const ct = contentType.toLowerCase();
  const isSvg = ct.includes("svg");
  const isCsvThumb = ct.includes("csv");

  // Themeable types capture both schemes; single-theme types capture light only.
  const schemes = useMemo<Scheme[]>(
    () => (isThemeable(contentType) ? [LIGHT_SCHEME, DARK_SCHEME] : [LIGHT_SCHEME]),
    [contentType],
  );

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
        await uploadThumbnail(assetId, blob, scheme.variant);
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
  }, [assetId, schemes, onCaptured, onFailed]);

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
          {isCsvThumb && csvTable ? (
            <div>
              <style>{csvProseCss(scheme.tokens, scope)}</style>
              <div className={scope}>
                <table>
                  <thead>
                    <tr>
                      {csvTable.cols.map((col) => (
                        <th key={col}>{col}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {csvTable.rows.map((row, ri) => (
                      <tr key={ri}>
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
            <div style={{ maxWidth: "none" }}>
              <style>{markdownProseCss(scheme.tokens, scope)}</style>
              <div className={scope}>
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
              </div>
            </div>
          )}
        </div>
        );
      })}
    </>
  );
}
