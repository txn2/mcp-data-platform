import { toPng } from "html-to-image";
import html2canvas from "html2canvas";
import { apiFetchRaw } from "@/api/portal/client";
import { transformJsx, escapeScriptClose, findComponentName } from "@/components/renderers/JsxRenderer";

export const THUMB_WIDTH = 400;
export const THUMB_HEIGHT = 300;

/** Desktop viewport dimensions used for rendering before scaling down. */
export const RENDER_WIDTH = 1280;
export const RENDER_HEIGHT = 960;

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
    width: RENDER_WIDTH,
    height: RENDER_HEIGHT,
    windowWidth: RENDER_WIDTH,
    windowHeight: RENDER_HEIGHT,
    scale: THUMB_WIDTH / RENDER_WIDTH,
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
 * Build a complete HTML document that transpiles and renders JSX content,
 * then notifies the parent when ready for capture. Reuses the same pipeline
 * as JsxRenderer (sucrase transform, import map, auto-mount) but adds a
 * postMessage notifier with a longer delay for async esm.sh loads.
 */
export function buildJsxThumbnailHtml(content: string): string {
  const CSP = [
    "default-src 'none'",
    "script-src 'unsafe-eval' 'unsafe-inline' https://esm.sh https://fonts.googleapis.com https://fonts.gstatic.com",
    "style-src 'unsafe-inline' https://fonts.googleapis.com",
    "img-src data: blob:",
    "font-src data: https://fonts.gstatic.com",
    "connect-src https://esm.sh https://fonts.googleapis.com https://fonts.gstatic.com",
  ].join("; ");

  const BARE_IMPORT_MAP: Record<string, string> = {
    react: "https://esm.sh/react@19",
    "react/": "https://esm.sh/react@19/",
    "react-dom": "https://esm.sh/react-dom@19",
    "react-dom/": "https://esm.sh/react-dom@19/",
    "react-dom/client": "https://esm.sh/react-dom@19/client",
    recharts: "https://esm.sh/recharts@2?bundle&external=react,react-dom",
    "lucide-react": "https://esm.sh/lucide-react@0.469?bundle&external=react",
  };

  const importMap = JSON.stringify({ imports: BARE_IMPORT_MAP });

  let transformed: string;
  try {
    transformed = escapeScriptClose(transformJsx(content));
  } catch {
    // If transform fails, return a simple error page that still notifies ready
    return `<!DOCTYPE html><html><head><meta charset="UTF-8"></head><body>
<pre style="color:#ef4444;padding:16px;font-family:monospace">JSX transform failed</pre>
<script>setTimeout(function(){parent.postMessage({type:'thumbnail-ready'},'*');},500);</script>
</body></html>`;
  }

  const hasMountCode =
    /\bcreateRoot\s*\(/.test(content) ||
    /\bReactDOM\s*\.\s*render\s*\(/.test(content);

  const componentName = findComponentName(content);
  const mountSection = hasMountCode
    ? transformed
    : `import React from 'react';
import { createRoot } from 'react-dom/client';

${transformed}

try {
  ${componentName ? `createRoot(document.getElementById('root')).render(React.createElement(${componentName}));` : ""}
} catch(e) {
  document.getElementById('root').textContent = e.message;
}`;

  const notifierScript = `
setTimeout(function() {
  parent.postMessage({ type: 'thumbnail-ready' }, '*');
}, 2000);`;

  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta http-equiv="Content-Security-Policy" content="${CSP}">
  <script type="importmap">${importMap}</script>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; padding: 16px; }
  </style>
</head>
<body>
  <div id="root"></div>
  <script type="module">
window.onerror = function(msg, src, line, col, err) {
  var el = document.createElement('pre');
  el.textContent = err && err.stack ? err.stack : msg;
  document.getElementById('root').appendChild(el);
};
window.addEventListener('unhandledrejection', function(e) {
  var el = document.createElement('pre');
  el.textContent = 'Module load error: ' + (e.reason && e.reason.stack ? e.reason.stack : e.reason);
  document.getElementById('root').appendChild(el);
});

${mountSection}
  </script>
  <script>${notifierScript}</script>
</body>
</html>`;
}

/**
 * Returns true if the content type supports thumbnail generation.
 */
export function isThumbnailSupported(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("html") || ct.includes("jsx") || ct.includes("svg") || ct.includes("markdown");
}
