import { useEffect, useRef, useCallback, useMemo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import DOMPurify from "dompurify";
import {
  THUMB_WIDTH,
  THUMB_HEIGHT,
  injectCaptureScript,
  captureElement,
  uploadThumbnail,
} from "@/lib/thumbnail";

interface Props {
  assetId: string;
  content: string;
  contentType: string;
  onCaptured?: () => void;
}

/**
 * Hidden off-screen component that renders content, captures a PNG thumbnail,
 * and uploads it to the server. Renders nothing visible to the user.
 */
export function ThumbnailGenerator({ assetId, content, contentType, onCaptured }: Props) {
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
      />
    );
  }

  return null;
}

/**
 * Captures iframe-based content (HTML/JSX) by injecting a self-capture
 * script that uses html2canvas inside the sandboxed iframe.
 */
function IframeCapture({
  assetId,
  content,
  onCaptured,
}: {
  assetId: string;
  content: string;
  onCaptured?: () => void;
}) {
  const capturedRef = useRef(false);

  const blobUrl = useMemo(() => {
    const injected = injectCaptureScript(content);
    const blob = new Blob([injected], { type: "text/html;charset=utf-8" });
    return URL.createObjectURL(blob);
  }, [content]);

  useEffect(() => {
    function handleMessage(e: MessageEvent) {
      if (capturedRef.current) return;
      if (e.data?.type !== "thumbnail-capture") return;
      capturedRef.current = true;

      const dataUrl = e.data.data as string | null;
      if (!dataUrl) return;

      fetch(dataUrl)
        .then((r) => r.blob())
        .then((blob) => uploadThumbnail(assetId, blob))
        .then(() => onCaptured?.())
        .catch(() => { /* best-effort */ });
    }

    window.addEventListener("message", handleMessage);
    return () => window.removeEventListener("message", handleMessage);
  }, [assetId, onCaptured]);

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
        sandbox="allow-scripts"
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
}: {
  assetId: string;
  content: string;
  contentType: string;
  onCaptured?: () => void;
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
      // best-effort
    }
  }, [assetId, onCaptured]);

  useEffect(() => {
    // Wait for render to complete
    const timer = setTimeout(doCapture, 500);
    return () => clearTimeout(timer);
  }, [doCapture]);

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
