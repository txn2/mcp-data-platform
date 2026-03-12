import { useEffect, useRef, useCallback, useMemo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import DOMPurify from "dompurify";
import {
  THUMB_WIDTH,
  THUMB_HEIGHT,
  CAPTURE_TIMEOUT_MS,
  injectCaptureScript,
  captureIframe,
  captureElement,
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

  if (isIframeType) {
    return (
      <IframeCapture
        assetId={assetId}
        content={content}
        onCaptured={onCaptured}
        onFailed={onFailed}
      />
    );
  }

  if (isMarkdown || isSvg) {
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
  onCaptured,
  onFailed,
}: {
  assetId: string;
  content: string;
  onCaptured?: () => void;
  onFailed?: () => void;
}) {
  const capturedRef = useRef(false);
  const iframeRef = useRef<HTMLIFrameElement>(null);

  const blobUrl = useMemo(() => {
    const injected = injectCaptureScript(content);
    const blob = new Blob([injected], { type: "text/html;charset=utf-8" });
    return URL.createObjectURL(blob);
  }, [content]);

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
      // blob: iframes have origin "null" — reject messages from other origins
      if (e.origin !== "null") return;
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
        width: THUMB_WIDTH,
        height: THUMB_HEIGHT,
        overflow: "hidden",
        pointerEvents: "none",
      }}
      aria-hidden="true"
    >
      <iframe
        ref={iframeRef}
        sandbox="allow-scripts allow-same-origin"
        src={blobUrl}
        width={THUMB_WIDTH}
        height={THUMB_HEIGHT}
        style={{ border: "none" }}
        title="Thumbnail capture"
      />
    </div>
  );
}

/**
 * Captures same-origin DOM content (Markdown/SVG) using html-to-image.
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
  const sanitizedSvg = useMemo(
    () => (isSvg ? DOMPurify.sanitize(content, { USE_PROFILES: { svg: true, svgFilters: true } }) : ""),
    [content, isSvg],
  );

  const doCapture = useCallback(async () => {
    if (capturedRef.current || !containerRef.current) return;
    capturedRef.current = true;
    try {
      const blob = await captureElement(containerRef.current);
      await uploadThumbnail(assetId, blob);
      onCaptured?.();
    } catch {
      onFailed?.();
    }
  }, [assetId, onCaptured, onFailed]);

  useEffect(() => {
    // Wait for render to complete
    const timer = setTimeout(doCapture, 500);
    return () => clearTimeout(timer);
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
      }}
      aria-hidden="true"
    >
      {isSvg ? (
        <div dangerouslySetInnerHTML={{ __html: sanitizedSvg }} />
      ) : (
        <div className="prose prose-sm max-w-none">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
        </div>
      )}
    </div>
  );
}
