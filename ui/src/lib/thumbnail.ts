import {
  transformJsx,
  escapeScriptClose,
  findComponentName,
  buildCSP,
  viewerOrigin,
  REF_PATH_PREFIX,
} from "@/components/renderers/JsxRenderer";

// The documents a tile of HTML or JSX is drawn from (#1787). The platform's
// renderer loads the tile page (src/tile-entry.tsx), which puts one of these in
// a frame and waits for the frame to say it has settled.

/**
 * How long the frame waits for reference loads still in flight when it would
 * otherwise report itself ready.
 *
 * A referenced CSV fetched at render time is the thing the artifact draws its
 * numbers from, so drawing before it lands stores a picture of the loading
 * state. The wait is bounded well inside the renderer's own deadline, so a
 * reference that never answers ends as a reason the tile was not stored rather
 * than as a render that timed out.
 */
const REF_SETTLE_TIMEOUT_MS = 8_000;

/**
 * How long the frame waits for the maps a document creates to finish drawing
 * (#2087), after its reference loads have settled.
 *
 * A map draws its tiles after the document has loaded, so a capture taken at
 * load shows the basemap at its lowest zooms and whichever data tiles happened
 * to land. The bound keeps a map whose tiles never arrive from holding the
 * render past the renderer's deadline: with the delay and the reference bound
 * it stays well inside it.
 */
const MAP_SETTLE_TIMEOUT_MS = 15_000;

/**
 * The map tracker the PDF print step uses too (internal/httpserver/pdfhttp,
 * PrintStep). It takes the map runtime's assignment to window.maplibregl
 * through a property setter, keeps every map the document creates, and gives
 * the notifier window.__mapsDrawn, which resolves once each has drawn
 * (MapLibre's idle: style, tiles and any transition done) or the bound passes.
 * The Go side holds the text between the markers to be the same as this.
 */
const MAP_TRACKER = `/*map-tracker:start*/
(function(){
var maps=[];
var maplibre;
Object.defineProperty(window,"maplibregl",{
  configurable:true,
  enumerable:true,
  get:function(){return maplibre;},
  set:function(value){
    maplibre=value;
    if(value&&typeof value.Map==="function"&&!value.Map.__tracked){
      var Base=value.Map;
      var Tracked=function(options){var m=new Base(options);maps.push(m);return m;};
      Tracked.prototype=Base.prototype;
      Tracked.__tracked=true;
      value.Map=Tracked;
    }
  }
});
function drawn(m){
  return new Promise(function(resolve){
    if(m.loaded()&&m.areTilesLoaded()){resolve();return;}
    m.once("idle",function(){resolve();});
  });
}
window.__mapCount=function(){return maps.length;};
window.__mapsDrawn=function(bound){
  if(!maps.length)return Promise.resolve();
  var timeout=new Promise(function(resolve){setTimeout(resolve,bound);});
  return Promise.race([Promise.all(maps.map(drawn)),timeout]);
};
})();
/*map-tracker:end*/`;

/**
 * How long the frame waits for its styles to be in (#2122): every stylesheet
 * it names loaded or failed, its fonts settled, and nothing about its styles or
 * its layout changing for STYLE_QUIET_MS. The quiet period is what catches CSS
 * written at run time -- the Tailwind Play CDN script writes its <style> only
 * after it sees the DOM, and a stylesheet a script adds after load is not in
 * the document when load fires.
 */
const STYLE_SETTLE_TIMEOUT_MS = 6_000;

/** How long nothing about a frame's styles or layout may change before they count as in. */
const STYLE_QUIET_MS = 400;

/**
 * How long a JSX frame waits for its module to run (#2122): the module runs
 * only once every module it imports from esm.sh has arrived, which is the
 * signal the fixed delay before it stood in for. A module whose import failed
 * never runs; the platform's renderer records that fetch as missing and the
 * attempt is retried rather than stored.
 */
const MODULE_RUN_TIMEOUT_MS = 15_000;

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
 * A font is not counted (#2062). A page whose referenced font did not load is
 * drawn completely in its fallback face: the layout, the numbers and the images
 * are all there, so the picture is of the page and not of an error branch.
 * Withholding it left the card on its content-type icon until the asset changed.
 * An image or a data file is different: the artifact draws something else when
 * one of those fails.
 *
 * It is a classic inline script so it runs before the module that renders the
 * artifact: fetch has to be wrapped before the artifact calls it.
 */
function refWatchScript(): string {
  return `<script>
${MAP_TRACKER}
(function() {
  var PREFIX = ${JSON.stringify(REF_PATH_PREFIX)};
  var state = { failed: 0, inflight: 0, started: 0 };
  window.__thumbnailRefs = state;
  function isRef(u) {
    return typeof u === 'string' && u.indexOf(PREFIX) !== -1;
  }
  function isSheet(t) {
    return t && t.tagName === 'LINK' && /(^|\\s)stylesheet(\\s|$)/i.test(t.rel || '');
  }
  document.addEventListener('securitypolicyviolation', function(e) {
    if (e.effectiveDirective === 'font-src') return;
    if (isRef(e.blockedURI)) state.failed++;
  });
  window.addEventListener('error', function(e) {
    var t = e.target;
    if (t && t.tagName === 'IMG' && isRef(t.currentSrc || t.src)) state.failed++;
    if (isSheet(t)) t.__thumbnailSettled = true;
    if (t && t.tagName === 'SCRIPT') window.__thumbnailScriptFailed = true;
  }, true);
  // On the document, not the window: an element's load event never reaches
  // the window, whose listeners hear only the document's own load.
  document.addEventListener('load', function(e) {
    var t = e.target;
    if (t && t.tagName === 'IMG' && isRef(t.currentSrc || t.src)) state.started++;
    if (isSheet(t)) t.__thumbnailSettled = true;
  }, true);
  // Resolves once the document's styles are in (#2122): every stylesheet it
  // names, including one a script added after load, has loaded or failed, its
  // fonts have settled, and neither its styles nor its layout have changed for
  // a quiet period -- or the bound has passed.
  function styleSignature() {
    var text = 0;
    var styles = document.getElementsByTagName('style');
    for (var i = 0; i < styles.length; i++) text += (styles[i].textContent || '').length;
    var root = document.documentElement;
    var body = document.body;
    return [document.styleSheets.length, styles.length, text,
      root ? root.scrollWidth : 0, root ? root.scrollHeight : 0,
      body ? body.getBoundingClientRect().height : 0].join(',');
  }
  function sheetsPending() {
    var links = document.getElementsByTagName('link');
    for (var i = 0; i < links.length; i++) {
      var l = links[i];
      if (isSheet(l) && !l.disabled && !l.sheet && !l.__thumbnailSettled) return true;
    }
    return false;
  }
  window.__stylesSettled = function(bound, quiet) {
    var fonts = document.fonts && document.fonts.ready ? document.fonts.ready : Promise.resolve();
    var fontsDone = false;
    fonts.then(function() { fontsDone = true; }, function() { fontsDone = true; });
    return new Promise(function(resolve) {
      var waited = 0;
      var last = styleSignature();
      var still = 0;
      function look() {
        var sig = styleSignature();
        if (sig !== last) { last = sig; still = 0; } else { still += 50; }
        if (waited >= bound || (fontsDone && !sheetsPending() && still >= quiet)) { resolve(); return; }
        waited += 50;
        setTimeout(look, 50);
      }
      look();
    });
  };
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
 * `delayMs`, after a JSX frame's module ran (`awaitModule`), after the
 * document's styles are in, and after its reference loads have settled.
 *
 * Styles come first (#2122): a capture taken before a stylesheet a script
 * added after load arrived, or before run-time CSS was written, is a picture
 * of unstyled text that the worker had no way to tell from the page.
 *
 * Waiting only on what is in flight at the delay would miss the common shape:
 * a dashboard fetches its referenced data from an effect that runs after the
 * module graph resolves, so at the delay nothing has been requested yet and the
 * capture would store a picture of the loading state. So a document that has
 * asked for nothing yet is given a grace period first, and one that has is
 * waited on until its requests settle.
 *
 * Once they have, it waits for every map the document created to finish
 * drawing (MAP_TRACKER), bounded by MAP_SETTLE_TIMEOUT_MS.
 *
 * It reports how many reference loads failed so the parent can throw the
 * capture away, and names the asset so a frame's report cannot be read as
 * another frame's. A document with no watcher on it (the transform-failure page
 * below) reports none, which is right: there is nothing referenced to fail.
 */
function notifierScript(assetId: string, delayMs: number, awaitModule = false): string {
  return `
(function() {
  var waited = 0;
  function state() { return window.__thumbnailRefs || { failed: 0, inflight: 0, started: 0 }; }
  function ready() {
    var maps = window.__mapsDrawn ? window.__mapsDrawn(${MAP_SETTLE_TIMEOUT_MS}) : Promise.resolve();
    maps.then(function() {
      parent.postMessage({
        type: 'thumbnail-ready',
        assetId: ${JSON.stringify(assetId)},
        refFailures: state().failed
      }, '*');
    });
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
  function styled() {
    var styles = window.__stylesSettled ? window.__stylesSettled(${STYLE_SETTLE_TIMEOUT_MS}, ${STYLE_QUIET_MS}) : Promise.resolve();
    styles.then(settle);
  }
  var moduleWaited = 0;
  function moduleRan() {
    var done = !${awaitModule} || window.__thumbnailModuleRan || window.__thumbnailScriptFailed;
    if (!done && moduleWaited < ${MODULE_RUN_TIMEOUT_MS}) {
      moduleWaited += 50;
      setTimeout(moduleRan, 50);
      return;
    }
    styled();
  }
  setTimeout(moduleRan, ${delayMs});
})();`;
}

/**
 * Put the reference watcher and the ready notifier into an HTML document, so
 * the frame it is drawn in reports when it has settled and how many of the
 * files it links to failed to load.
 */
export function injectCaptureScript(html: string, assetId = ""): string {
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

/** Thumbnail color-scheme variant. Light is the default/shared variant. */
export type ThumbnailVariant = "light" | "dark";

/**
 * Build a complete HTML document that transpiles and renders JSX content,
 * then notifies the parent when ready for capture. Reuses the same pipeline
 * as JsxRenderer (sucrase transform, import map, auto-mount, and its CSP) but
 * adds a postMessage notifier that waits for the module to run -- which is
 * when every esm.sh module it imports has arrived -- rather than for a fixed
 * delay (#2122).
 *
 * The asset id travels into the frame so its ready message names the document
 * it is about.
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
  window.__thumbnailModuleRan = true;
  var el = document.createElement('pre');
  el.textContent = err && err.stack ? err.stack : msg;
  document.getElementById('root').appendChild(el);
};
window.addEventListener('unhandledrejection', function(e) {
  window.__thumbnailModuleRan = true;
  var el = document.createElement('pre');
  el.textContent = 'Module load error: ' + (e.reason && e.reason.stack ? e.reason.stack : e.reason);
  document.getElementById('root').appendChild(el);
});

${mountSection}
window.__thumbnailModuleRan = true;
  </script>
  <script>${notifierScript(assetId, 0, true)}</script>
</body>
</html>`;
}
