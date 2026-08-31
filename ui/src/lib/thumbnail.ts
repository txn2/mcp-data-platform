import html2canvas from "html2canvas";
import { useAuthStore } from "@/stores/auth";
import { applyCsrfHeader } from "@/api/csrf";
import {
  transformJsx,
  escapeScriptClose,
  findComponentName,
  buildCSP,
  viewerOrigin,
  REF_PATH_PREFIX,
} from "@/components/renderers/JsxRenderer";
import { THUMB_WIDTH, THUMB_HEIGHT } from "@/lib/thumbnailSupport";

// Re-exported so the capturer has one import for everything it needs. Callers
// that only ask which types are supported import lib/thumbnailSupport directly
// and stay clear of html2canvas.
export { THUMB_WIDTH, THUMB_HEIGHT, THUMBNAIL_SOURCE_LIMIT, isThumbnailSupported, isThemeable } from "@/lib/thumbnailSupport";

/** Desktop viewport dimensions used for rendering before scaling down. */
export const RENDER_WIDTH = 1280;
export const RENDER_HEIGHT = 960;

/** Capture timeout in milliseconds. */
export const CAPTURE_TIMEOUT_MS = 15_000;

/**
 * How long the frame waits for reference loads still in flight when it would
 * otherwise report itself ready.
 *
 * A referenced CSV fetched at render time is the thing the artifact draws its
 * numbers from, so capturing before it lands stores a picture of the loading
 * state. The wait is bounded well inside CAPTURE_TIMEOUT_MS so a reference that
 * never answers ends as a discarded capture rather than as the parent's timeout.
 */
const REF_SETTLE_TIMEOUT_MS = 8_000;

/**
 * How long the frame waits for the artifact's FIRST reference request before
 * deciding there is none coming.
 *
 * An artifact that references nothing pays this once, which is why it is short
 * next to the settle bound above: the delay before the notifier runs has
 * already let the module graph resolve, and what is being waited for here is
 * the effect that runs after it.
 */
const REF_FIRST_REQUEST_GRACE_MS = 1_500;

/**
 * The frame-side script that watches what the artifact loads through the
 * reference route, and the notifier that reports the frame ready.
 *
 * A capture cannot tell a rendered artifact from a rendered error by looking at
 * the pixels: an artifact whose references were blocked draws its own failure
 * branch and rasterizes to a perfectly valid PNG (#1497). So the frame counts
 * what failed and says so, and the parent discards a capture that reports any
 * failure rather than storing a picture of the error state.
 *
 * Three ways a reference load fails are counted, because all three produce the
 * same drawing: the policy refusing the request, an <img> erroring, and a
 * fetch() rejecting or answering a non-2xx. Only URLs under the reference route
 * count -- an artifact whose own analytics beacon fails still gets its picture
 * taken.
 *
 * It is a classic inline script so it runs before the module that renders the
 * artifact: fetch has to be wrapped before the artifact calls it.
 */
function refWatchScript(): string {
  return `<script>
(function() {
  var PREFIX = ${JSON.stringify(REF_PATH_PREFIX)};
  var state = { failed: 0, inflight: 0, started: 0 };
  window.__thumbnailRefs = state;
  function isRef(u) {
    return typeof u === 'string' && u.indexOf(PREFIX) !== -1;
  }
  document.addEventListener('securitypolicyviolation', function(e) {
    if (isRef(e.blockedURI)) state.failed++;
  });
  window.addEventListener('error', function(e) {
    var t = e.target;
    if (t && t.tagName === 'IMG' && isRef(t.currentSrc || t.src)) state.failed++;
  }, true);
  window.addEventListener('load', function(e) {
    var t = e.target;
    if (t && t.tagName === 'IMG' && isRef(t.currentSrc || t.src)) state.started++;
  }, true);
  var nativeFetch = window.fetch;
  window.fetch = function(input) {
    // A Request carries the address as .url and a URL object as .href, so both
    // forms an artifact may pass are read rather than only the plain string.
    var url = typeof input === 'string' ? input : (input && (input.url || input.href)) || '';
    if (!isRef(url)) return nativeFetch.apply(window, arguments);
    state.started++;
    state.inflight++;
    return nativeFetch.apply(window, arguments).then(function(res) {
      state.inflight--;
      if (!res.ok) state.failed++;
      return res;
    }, function(err) {
      state.inflight--;
      state.failed++;
      throw err;
    });
  };
})();
</script>`;
}

/**
 * The notifier that tells the parent the frame is ready to be captured, after
 * `delayMs` and after the artifact's reference loads have settled.
 *
 * Waiting only on what is in flight at the delay would miss the common shape:
 * a dashboard fetches its referenced data from an effect that runs after the
 * module graph resolves, so at the delay nothing has been requested yet and the
 * capture would store a picture of the loading state. So a document that has
 * asked for nothing yet is given a grace period first, and one that has is
 * waited on until its requests settle.
 *
 * It reports how many reference loads failed so the parent can throw the
 * capture away, and names the asset so a frame's report cannot be read as
 * another frame's. A document with no watcher on it (the transform-failure page
 * below) reports none, which is right: there is nothing referenced to fail.
 */
function notifierScript(assetId: string, delayMs: number): string {
  return `
(function() {
  var waited = 0;
  function state() { return window.__thumbnailRefs || { failed: 0, inflight: 0, started: 0 }; }
  function ready() {
    parent.postMessage({
      type: 'thumbnail-ready',
      assetId: ${JSON.stringify(assetId)},
      refFailures: state().failed
    }, '*');
  }
  function settle() {
    var s = state();
    var waiting = s.inflight > 0 || (s.started === 0 && waited < ${REF_FIRST_REQUEST_GRACE_MS});
    if (waiting && waited < ${REF_SETTLE_TIMEOUT_MS}) {
      waited += 100;
      setTimeout(settle, 100);
      return;
    }
    ready();
  }
  setTimeout(settle, ${delayMs});
})();`;
}

/**
 * Inject the reference watcher and a self-capture notifier into HTML content
 * that runs inside a sandboxed blob: iframe. The script uses a bundled copy of
 * html2canvas (injected as an inline script) instead of loading from a CDN,
 * avoiding supply-chain risk and CSP issues.
 *
 * The injected code posts a "thumbnail-ready" message back to the parent via
 * postMessage with origin "null" (blob: iframe), carrying the count of
 * reference loads that failed.
 */
export function injectCaptureScript(html: string, assetId = ""): string {
  // We serialize the html2canvas entry point path so the iframe can import it.
  // Since the iframe is sandboxed with a blob: URL, we can't use ES module
  // imports. Instead, we render the content and use the parent to capture.
  // The iframe posts a "thumbnail-ready" message when loaded, and the parent
  // captures it using html2canvas on the iframe's contentDocument.
  const script = `
<script>
(function() {
  function start() {${notifierScript(assetId, 500)}}
  if (document.readyState === 'complete') {
    start();
  } else {
    window.addEventListener('load', start);
  }
})();
</script>`;

  const watched = insertRefWatch(html);
  // Insert before </body> if present, otherwise append
  const idx = watched.toLowerCase().lastIndexOf("</body>");
  if (idx >= 0) {
    return watched.slice(0, idx) + script + watched.slice(idx);
  }
  return watched + script;
}

/**
 * Put the reference watcher at the front of an HTML document, before anything
 * the document itself runs.
 *
 * Appending it beside the notifier would be too late: an HTML report that
 * fetches a referenced JSON from a script in its own body would have called
 * fetch before the wrapper existed, and the failure would go uncounted. Where
 * there is a <head> or a <body> tag it goes just inside; a fragment with
 * neither gets it in front, which is where the parser would put it anyway.
 */
function insertRefWatch(html: string): string {
  const watch = refWatchScript();
  // The lookahead is what keeps <header> from satisfying the <head>
  // alternative: "er" fits [^>]* and the watcher would land after the scripts
  // it exists to precede.
  const at = /<head(?=[\s>])[^>]*>|<body(?=[\s>])[^>]*>/i.exec(html);
  if (at) {
    const idx = at.index + at[0].length;
    return html.slice(0, idx) + watch + html.slice(idx);
  }
  return watch + html;
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


/** Thumbnail color-scheme variant. Light is the default/shared variant. */
export type ThumbnailVariant = "light" | "dark";

/**
 * Upload a PNG thumbnail blob for an asset. The optional variant selects the
 * color scheme; "dark" is only captured for themeable content types (see
 * isThemeable). Defaults to the light/shared variant.
 */
/**
 * What a capture belongs to: a portal asset, or a managed resource (#1554).
 *
 * The capturer is the same for both -- nothing on a server can rasterize a
 * document -- so the kind travels with the id rather than being forked into a
 * second component.
 */
export interface ThumbnailTarget {
  kind: "asset" | "resource";
  id: string;
}

/**
 * The route a target's capture is uploaded to and served from, in full.
 *
 * An absolute path rather than a fragment for one client to prefix: an asset
 * lives under /api/v1/portal and a resource under /api/v1/resources, so a
 * fragment handed to the wrong client is a 404 -- which is exactly what every
 * resource capture did until this was written out (#1554). The test that was
 * supposed to catch it asserted the fragment the mock received instead of the
 * URL that would be requested, and so agreed with the bug.
 */
export function thumbnailPath(target: ThumbnailTarget): string {
  return target.kind === "resource"
    ? `/api/v1/resources/${target.id}/thumbnail`
    : `/api/v1/portal/assets/${target.id}/thumbnail`;
}

/** The route a target's own bytes are read from. */
export function contentPath(target: ThumbnailTarget): string {
  return target.kind === "resource"
    ? `/api/v1/resources/${target.id}/content`
    : `/api/v1/portal/assets/${target.id}/content`;
}

/**
 * Downscale an image to tile size, as a PNG.
 *
 * The image is drawn to COVER the tile -- scaled until it fills both axes and
 * centred -- because that is how the card displays it. Storing a letterboxed
 * copy would mean the card cropping an image that was already cropped.
 *
 * The element loads the source itself, so the browser does the decoding, and
 * `crossOrigin` is deliberately not set: the content route is same-origin and
 * asking for CORS on it would taint the canvas and make toBlob throw.
 */
export async function downscaleImage(src: string, contentType: string): Promise<Blob> {
  const img = await loadImage(src);
  const canvas = document.createElement("canvas");
  canvas.width = THUMB_WIDTH;
  canvas.height = THUMB_HEIGHT;

  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error(`no 2d context for ${contentType}`);

  const scale = Math.max(THUMB_WIDTH / img.naturalWidth, THUMB_HEIGHT / img.naturalHeight);
  const w = img.naturalWidth * scale;
  const h = img.naturalHeight * scale;
  ctx.drawImage(img, (THUMB_WIDTH - w) / 2, (THUMB_HEIGHT - h) / 2, w, h);

  return await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error("canvas produced no image"))),
      "image/png",
    );
  });
}

/**
 * Load an image element from an authenticated route.
 *
 * The bytes are fetched with the session's own credentials and handed to the
 * element as a blob URL, rather than pointing the element at the route: an
 * `<img src>` carries no `X-API-Key`, so on an API-key session -- which is how
 * the dev portal and every API-key deployment sign in -- the load 401s and the
 * capture fails silently. It is the same reason AuthImg exists.
 *
 * A blob URL is also same-origin, so the canvas it is drawn onto stays untainted
 * and toBlob can read it back.
 */
async function loadImage(src: string): Promise<HTMLImageElement> {
  const res = await authedFetch(src);
  if (!res.ok) throw new Error(`could not read ${src}: HTTP ${res.status}`);
  const url = URL.createObjectURL(await res.blob());
  try {
    return await new Promise<HTMLImageElement>((resolve, reject) => {
      const img = new Image();
      img.onload = () => resolve(img);
      img.onerror = () => reject(new Error(`could not decode ${src}`));
      img.src = url;
    });
  } finally {
    // The element holds the decoded bitmap; the URL has done its job either
    // way, and leaving it allocated leaks for the life of the document.
    URL.revokeObjectURL(url);
  }
}

/**
 * Fetch a full URL with whatever credentials this session carries.
 *
 * It takes the whole path rather than a fragment because the two kinds live
 * under different API roots, and a helper that prefixed one root would send
 * half its traffic to the wrong place -- which is how every resource capture
 * came to PUT at /api/v1/portal/resources/... and 404 (#1554).
 */
function authedFetch(url: string, init?: RequestInit): Promise<Response> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string>),
  };
  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }
  applyCsrfHeader(headers, init?.method);
  return fetch(url, { ...init, headers, credentials: "include" });
}

export async function uploadThumbnail(
  target: ThumbnailTarget,
  blob: Blob,
  variant: ThumbnailVariant = "light",
  version?: number,
): Promise<void> {
  // The version the capture was rendered from travels with it, so the asset row
  // records what the image actually shows. Without it the server can only date
  // the capture to whatever version the asset is on when the upload lands, and
  // an asset rewritten mid-capture would be marked current while showing the
  // version before it (#1431). A resource sends none: its row has no version
  // column, and the server stamps the capture with the resource's own
  // updated_at, which says the same thing without the round trip.
  const params = new URLSearchParams();
  if (variant === "dark") params.set("variant", "dark");
  if (version != null) params.set("version", String(version));
  const query = params.toString();
  const url = `${thumbnailPath(target)}${query ? `?${query}` : ""}`;
  const res = await authedFetch(url, {
    method: "PUT",
    headers: { "Content-Type": "image/png" },
    body: blob,
  });
  if (!res.ok) {
    throw new Error(`Failed to upload thumbnail: ${url} answered ${res.status}`);
  }
}

/**
 * Build a complete HTML document that transpiles and renders JSX content,
 * then notifies the parent when ready for capture. Reuses the same pipeline
 * as JsxRenderer (sucrase transform, import map, auto-mount, and its CSP) but
 * adds a postMessage notifier with a longer delay for async esm.sh loads.
 *
 * The asset id travels into the frame so its ready message names the asset it
 * is about: a queue capture and a viewer capture can be mounted at once, and a
 * message that named neither was read by both.
 *
 * The origin is the one the reference route is granted under, defaulted to the
 * viewer's own the way the live renderer defaults it. It is a parameter only so
 * a test can build the document without a window.
 */
export function buildJsxThumbnailHtml(
  content: string,
  assetId = "",
  origin: string = viewerOrigin(),
): string {
  // The live frame's policy, from the live frame's builder. A hand-written copy
  // of it drifted: the reference route was in JsxRenderer's img-src and
  // connect-src and in no copy here, so every artifact naming a managed
  // resource or another asset was captured with its references blocked and the
  // stored tile was a picture of the artifact's error branch (#1497).
  const CSP = buildCSP(origin);

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
<script>setTimeout(function(){parent.postMessage({type:'thumbnail-ready',assetId:${JSON.stringify(assetId)},refFailures:0},'*');},500);</script>
</body></html>`;
  }

  const hasMountCode =
    /\bcreateRoot\s*\(/.test(content) ||
    /\bReactDOM\s*\.\s*render\s*\(/.test(content);

  const componentName = findComponentName(content);
  // Mount helpers use collision-proof namespaced aliases so an artifact that
  // already imports React (preserved by Sucrase's automatic runtime) does not
  // produce "Identifier 'React' has already been declared". See JsxRenderer.
  const mountSection = hasMountCode
    ? transformed
    : `import * as __artifactReact from 'react';
import { createRoot as __artifactCreateRoot } from 'react-dom/client';

${transformed}

try {
  ${componentName ? `__artifactCreateRoot(document.getElementById('root')).render(__artifactReact.createElement(${componentName}));` : ""}
} catch(e) {
  document.getElementById('root').textContent = e.message;
}`;


  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta http-equiv="Content-Security-Policy" content="${CSP}">
  ${refWatchScript()}
  <script type="importmap">${importMap}</script>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; }
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
  <script>${notifierScript(assetId, 2000)}</script>
</body>
</html>`;
}
