import { toPng } from "html-to-image";
import html2canvas from "html2canvas";
import { apiFetchRaw } from "@/api/portal/client";

export const THUMB_WIDTH = 400;
export const THUMB_HEIGHT = 300;

/** Capture timeout in milliseconds. */
export const CAPTURE_TIMEOUT_MS = 15_000;

/**
 * Inject a self-capture script into HTML content that runs inside a sandboxed
 * blob: iframe. The script uses a bundled copy of html2canvas (injected as an
 * inline script) instead of loading from a CDN, avoiding supply-chain risk
 * and CSP issues.
 *
 * The injected code captures the body and posts the data URL back to the
 * parent via postMessage with origin "null" (blob: iframe).
 */
export function injectCaptureScript(html: string): string {
  // We serialize the html2canvas entry point path so the iframe can import it.
  // Since the iframe is sandboxed with a blob: URL, we can't use ES module
  // imports. Instead, we render the content and use the parent to capture.
  // The iframe posts a "thumbnail-ready" message when loaded, and the parent
  // captures it using html2canvas on the iframe's contentDocument.
  const script = `
<script>
(function() {
  function notifyReady() {
    parent.postMessage({ type: 'thumbnail-ready' }, '*');
  }
  if (document.readyState === 'complete') {
    setTimeout(notifyReady, 500);
  } else {
    window.addEventListener('load', function() { setTimeout(notifyReady, 500); });
  }
})();
</script>`;

  // Insert before </body> if present, otherwise append
  const idx = html.toLowerCase().lastIndexOf("</body>");
  if (idx >= 0) {
    return html.slice(0, idx) + script + html.slice(idx);
  }
  return html + script;
}

/**
 * Capture an iframe element's content using the bundled html2canvas.
 * The iframe must have same-origin access (blob: URL satisfies this when
 * the sandbox includes allow-same-origin).
 */
export async function captureIframe(iframe: HTMLIFrameElement): Promise<Blob> {
  const doc = iframe.contentDocument;
  if (!doc?.body) throw new Error("Cannot access iframe content");

  const canvas = await html2canvas(doc.body, {
    width: THUMB_WIDTH,
    height: THUMB_HEIGHT,
    scale: 1,
    logging: false,
    useCORS: true,
  });

  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error("canvas.toBlob returned null"));
    }, "image/png");
  });
}

/**
 * Capture a same-origin DOM element as a PNG blob using html-to-image.
 */
export async function captureElement(element: HTMLElement): Promise<Blob> {
  const dataUrl = await toPng(element, {
    width: THUMB_WIDTH,
    height: THUMB_HEIGHT,
    canvasWidth: THUMB_WIDTH,
    canvasHeight: THUMB_HEIGHT,
  });
  const res = await fetch(dataUrl);
  return res.blob();
}

/**
 * Upload a PNG thumbnail blob for an asset.
 */
export async function uploadThumbnail(assetId: string, blob: Blob): Promise<void> {
  const res = await apiFetchRaw(`/assets/${assetId}/thumbnail`, {
    method: "PUT",
    headers: { "Content-Type": "image/png" },
    body: blob,
  });
  if (!res.ok) {
    throw new Error("Failed to upload thumbnail");
  }
}

/**
 * Returns true if the content type supports thumbnail generation.
 */
export function isThumbnailSupported(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("html") || ct.includes("jsx") || ct.includes("svg") || ct.includes("markdown");
}
