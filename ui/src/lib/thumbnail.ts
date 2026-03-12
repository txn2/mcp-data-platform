import { toPng } from "html-to-image";
import { apiFetchRaw } from "@/api/portal/client";

export const THUMB_WIDTH = 400;
export const THUMB_HEIGHT = 300;

/**
 * Inject a self-capture script into HTML content that runs inside a sandboxed
 * blob: iframe. The script loads html2canvas from a CDN, captures the body,
 * and posts the data URL back to the parent via postMessage.
 */
export function injectCaptureScript(html: string): string {
  const script = `
<script>
(function() {
  function doCapture() {
    var s = document.createElement('script');
    s.src = 'https://cdnjs.cloudflare.com/ajax/libs/html2canvas/1.4.1/html2canvas.min.js';
    s.onload = function() {
      html2canvas(document.body, { width: ${THUMB_WIDTH}, height: ${THUMB_HEIGHT}, scale: 1, logging: false, useCORS: true })
        .then(function(canvas) {
          parent.postMessage({ type: 'thumbnail-capture', data: canvas.toDataURL('image/png') }, '*');
        })
        .catch(function() {
          parent.postMessage({ type: 'thumbnail-capture', data: null }, '*');
        });
    };
    s.onerror = function() {
      parent.postMessage({ type: 'thumbnail-capture', data: null }, '*');
    };
    document.head.appendChild(s);
  }
  if (document.readyState === 'complete') {
    setTimeout(doCapture, 500);
  } else {
    window.addEventListener('load', function() { setTimeout(doCapture, 500); });
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
