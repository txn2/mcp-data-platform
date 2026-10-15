import { afterEach, describe, it, expect, vi } from "vitest";

import { buildCSP, REF_PATH_PREFIX } from "@/components/renderers/JsxRenderer";
import { buildJsxThumbnailHtml, injectCaptureScript } from "./thumbnail";
import { isThemeable } from "./thumbnailSupport";

/** The policy the document declares, as the frame would read it. */
const cspOf = (html: string): string =>
  /<meta http-equiv="Content-Security-Policy" content="([^"]*)">/.exec(html)?.[1] ?? "";

// Count top-level `import React` default-binding declarations. The namespaced
// alias `import * as __artifactReact` must NOT match.
const countReactDefaultImports = (html: string): number =>
  (html.match(/import\s+React\b/g) ?? []).length;

describe("buildJsxThumbnailHtml: duplicate React declaration (issue #625)", () => {
  it("does not inject a second React import when artifact already imports React", () => {
    const code = `import React from 'react';
export default function App() { return <div>Hello</div>; }`;
    const html = buildJsxThumbnailHtml(code);
    expect(countReactDefaultImports(html)).toBe(1);
    expect(html).toContain("import * as __artifactReact from 'react'");
    expect(html).toContain("__artifactReact.createElement(App)");
  });

  it("injects no bare React import when artifact does not import React", () => {
    const code = `export default function App() { return <div>Hi</div>; }`;
    const html = buildJsxThumbnailHtml(code);
    expect(countReactDefaultImports(html)).toBe(0);
    expect(html).toContain(
      "import { createRoot as __artifactCreateRoot } from 'react-dom/client'",
    );
  });

  it("leaves self-mounting artifacts untouched (no injected helpers)", () => {
    const code = `import React from 'react';
import { createRoot } from 'react-dom/client';
function App() { return <div>Hello</div>; }
createRoot(document.getElementById('root')).render(<App />);`;
    const html = buildJsxThumbnailHtml(code);
    expect(countReactDefaultImports(html)).toBe(1);
    expect(html).not.toContain("__artifactReact");
    expect(html).not.toContain("__artifactCreateRoot");
  });

  it("returns a notifier document on transform failure", () => {
    const html = buildJsxThumbnailHtml("function {{{");
    expect(html).toContain("thumbnail-ready");
    expect(html).not.toContain("__artifactReact");
  });
});

describe("injectCaptureScript", () => {
  it("inserts the ready notifier before </body>", () => {
    const html = "<html><body><div>content</div></body></html>";
    const out = injectCaptureScript(html);
    expect(out).toContain("thumbnail-ready");
    expect(out.indexOf("thumbnail-ready")).toBeLessThan(out.indexOf("</body>"));
  });

  // A fragment keeps its content between the watcher that goes in front of it
  // and the notifier that follows it.
  it("appends the notifier when no </body> is present", () => {
    const html = "<div>content</div>";
    const out = injectCaptureScript(html);
    expect(out).toContain(html);
    expect(out.indexOf(html)).toBeLessThan(out.indexOf("thumbnail-ready"));
  });
});

describe("isThemeable", () => {
  it("is true for content drawn once per color scheme", () => {
    for (const ct of ["text/markdown", "text/csv", "TEXT/MARKDOWN", "text/html", "text/jsx"]) {
      expect(isThemeable(ct)).toBe(true);
    }
  });

  it("is false for content drawn as stored", () => {
    for (const ct of ["image/svg+xml", "image/png"]) {
      expect(isThemeable(ct)).toBe(false);
    }
  });
});

// A thumbnail is captured in a frame of its own, and for as long as that frame
// had a hand-written copy of the renderer's policy the two drifted: the
// reference route was in the renderer's img-src and connect-src and in no copy
// here, so an artifact naming a managed resource or another asset was captured
// with every reference blocked and the stored tile was a picture of the
// artifact's error branch (#1497).
describe("the capture frame's policy", () => {
  const ORIGIN = "https://platform.example.com";
  const CODE = `export default function App() { return <div>Hi</div>; }`;

  // The parity assertion, not two copies of one expectation: a directive added
  // to the live frame and not to this one fails here.
  it("is the policy the live renderer builds, for the same origin", () => {
    expect(cspOf(buildJsxThumbnailHtml(CODE, "ast-1", ORIGIN))).toBe(buildCSP(ORIGIN));
  });

  it("grants the reference route, which is how a referenced logo, font and data file load", () => {
    const csp = cspOf(buildJsxThumbnailHtml(CODE, "ast-1", ORIGIN));
    const ref = ORIGIN + REF_PATH_PREFIX;
    expect(csp).toContain(`img-src data: blob: ${ref}`);
    expect(csp).toContain(`font-src data: https://fonts.gstatic.com ${ref}`);
    expect(csp).toContain(`connect-src`);
    expect(csp.slice(csp.indexOf("connect-src"))).toContain(ref);
  });

  it("grants nothing when there is no origin to grant, as the live frame does", () => {
    expect(cspOf(buildJsxThumbnailHtml(CODE, "ast-1", ""))).toBe(buildCSP(""));
  });
});

// The capture cannot tell a rendered artifact from a rendered error by looking
// at the pixels, so the frame counts what failed and says so in the message the
// parent captures on (#1497).
describe("the capture frame's reference watch", () => {
  const CODE = `export default function App() { return <div>Hi</div>; }`;

  it("wraps fetch and watches images before the artifact runs", () => {
    const html = buildJsxThumbnailHtml(CODE, "ast-1", "https://platform.example.com");
    const watch = html.indexOf("window.__thumbnailRefs");
    const artifact = html.indexOf('<script type="module">');
    expect(watch).toBeGreaterThan(-1);
    expect(watch).toBeLessThan(artifact);
    expect(html).toContain(JSON.stringify(REF_PATH_PREFIX));
  });

  it("reports the failure count with the ready message, naming the asset", () => {
    const html = buildJsxThumbnailHtml(CODE, "ast-1", "https://platform.example.com");
    expect(html).toContain("refFailures: state().failed");
    // The queue and the viewer can both have a frame open, so a report that
    // named no asset was read by both of them.
    expect(html).toContain('assetId: "ast-1"');
  });

  // <header> satisfies /<head[^>]*>/, which would put the watcher after the
  // scripts it exists to run before.
  it("is not fooled into landing after a <header>", () => {
    const html = injectCaptureScript(
      "<header>title</header><script>fetch('/portal/refs/a/b')</script>",
    );
    expect(html.indexOf("window.__thumbnailRefs")).toBeLessThan(html.indexOf("<header>"));
  });

  it("watches an HTML document too, ahead of the scripts it carries", () => {
    const html = injectCaptureScript(
      "<html><head><title>t</title></head><body><script>fetch('/portal/refs/a/b')</script></body></html>",
    );
    const watch = html.indexOf("window.__thumbnailRefs");
    expect(watch).toBeGreaterThan(-1);
    expect(watch).toBeLessThan(html.indexOf("fetch('/portal/refs/a/b')"));
  });

  it("watches a fragment with no head or body", () => {
    const html = injectCaptureScript("<div>content</div>");
    expect(html.indexOf("window.__thumbnailRefs")).toBeLessThan(html.indexOf("<div>content</div>"));
  });
});

// The watcher run for real against this document: what it counts decides
// whether a tile is stored or the asset is recorded as not drawable.
describe("the capture frame's reference watch, run", () => {
  type Watched = { failed: number };
  const win = window as unknown as { __thumbnailRefs?: Watched };

  /** Runs the watcher script injectCaptureScript puts in a document. */
  function runWatcher(): Watched {
    const html = injectCaptureScript("<div>content</div>");
    const body = /<script>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? "";
    expect(body).toContain("__thumbnailRefs");
    new Function(body)();
    return win.__thumbnailRefs as Watched;
  }

  function violation(directive: string, blockedURI: string) {
    const e = new Event("securitypolicyviolation");
    Object.assign(e, { effectiveDirective: directive, blockedURI });
    document.dispatchEvent(e);
  }

  afterEach(() => {
    delete win.__thumbnailRefs;
  });

  it("counts a refused referenced image as a failure", () => {
    const state = runWatcher();
    violation("img-src", `https://platform.example.com${REF_PATH_PREFIX}ast-1/abc`);
    expect(state.failed).toBe(1);
  });

  // A page drawn in its fallback face is a picture of the page (#2062).
  it("does not count a refused referenced font", () => {
    const state = runWatcher();
    violation("font-src", `https://platform.example.com${REF_PATH_PREFIX}ast-1/abc`);
    expect(state.failed).toBe(0);
  });

  // An element's load event stops at the document; a listener on the window
  // never hears it, which left a loaded image uncounted.
  it("counts a loaded referenced image as started", () => {
    const state = runWatcher() as Watched & { started: number };
    const img = document.createElement("img");
    img.src = `https://platform.example.com${REF_PATH_PREFIX}ast-1/abc`;
    document.body.appendChild(img);
    img.dispatchEvent(new Event("load"));
    expect(state.started).toBe(1);
    img.remove();
  });

  it("does not count a refusal outside the reference route", () => {
    const state = runWatcher();
    violation("img-src", "https://beacon.example.com/pixel.gif");
    expect(state.failed).toBe(0);
  });
});

// A map draws its tiles after the document has loaded, so the frame waits for
// every map the document created to report itself drawn before it says it is
// ready, as the PDF print step does (#2087), and gives up on one whose tiles
// never arrive.
describe("the capture frame's map wait, run", () => {
  type FakeMap = { idle: () => void };
  const win = window as unknown as {
    maplibregl?: { Map: new (o: unknown) => FakeMap };
    __mapsDrawn?: unknown;
    __mapCount?: unknown;
    __thumbnailRefs?: unknown;
  };

  /** A runtime whose maps are drawn only when the test says so. */
  function fakeRuntime() {
    class Map {
      private drawn = false;
      private waiting: (() => void)[] = [];
      loaded() {
        return this.drawn;
      }
      areTilesLoaded() {
        return this.drawn;
      }
      once(event: string, fn: () => void) {
        if (event === "idle") this.waiting.push(fn);
      }
      idle() {
        this.drawn = true;
        this.waiting.splice(0).forEach((fn) => fn());
      }
    }
    return { Map };
  }

  /** Runs the scripts injectCaptureScript puts in a map document. */
  function capture(): { map: FakeMap; posted: () => number } {
    const html = injectCaptureScript("<div id='map'></div>", "ast-map");
    const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1] ?? "");
    new Function(scripts[0] ?? "")();
    win.maplibregl = fakeRuntime() as unknown as typeof win.maplibregl;
    const map = new win.maplibregl!.Map({ container: "map" });
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => undefined);
    new Function(scripts[scripts.length - 1] ?? "")();
    return { map, posted: () => post.mock.calls.filter((c) => (c[0] as { type?: string }).type === "thumbnail-ready").length };
  }

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    // The tracker defines maplibregl as a configurable accessor.
    delete win.maplibregl;
    delete win.__mapsDrawn;
    delete win.__mapCount;
    delete win.__thumbnailRefs;
  });

  it("is not ready until the map has drawn", async () => {
    vi.useFakeTimers();
    const { map, posted } = capture();
    await vi.advanceTimersByTimeAsync(5_000);
    expect(posted()).toBe(0);
    map.idle();
    await vi.advanceTimersByTimeAsync(0);
    expect(posted()).toBe(1);
  });

  it("is ready at its bound when the map never draws", async () => {
    vi.useFakeTimers();
    const { posted } = capture();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(posted()).toBe(0);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(posted()).toBe(1);
  });
});

// The frame reports ready only once its styles are in (#2122): a capture taken
// while a stylesheet is loading, or while run-time CSS is still being written,
// is a picture of unstyled text that nothing downstream can tell from the page.
describe("the capture frame's style wait, run", () => {
  const win = window as unknown as {
    __stylesSettled?: unknown;
    __thumbnailRefs?: unknown;
    __thumbnailModuleRan?: unknown;
    __thumbnailScriptFailed?: unknown;
    __mapsDrawn?: unknown;
    __mapCount?: unknown;
    maplibregl?: unknown;
  };

  function scriptsOf(html: string): string[] {
    return [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1] ?? "");
  }

  /** Runs an HTML capture document's watcher and notifier; counts ready posts. */
  function captureHtml(): () => number {
    const scripts = scriptsOf(injectCaptureScript("<div>content</div>", "ast-style"));
    new Function(scripts[0] ?? "")();
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => undefined);
    new Function(scripts[scripts.length - 1] ?? "")();
    return () => post.mock.calls.filter((c) => (c[0] as { type?: string }).type === "thumbnail-ready").length;
  }

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    document.head.innerHTML = "";
    document.body.innerHTML = "";
    delete win.__stylesSettled;
    delete win.__thumbnailRefs;
    delete win.__thumbnailModuleRan;
    delete win.__thumbnailScriptFailed;
    delete win.__mapsDrawn;
    delete win.__mapCount;
    delete win.maplibregl;
  });

  it("is not ready while a stylesheet a script added after load is still loading", async () => {
    vi.useFakeTimers();
    const posted = captureHtml();
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = "https://cdn.example.com/late.css";
    document.head.appendChild(link);
    await vi.advanceTimersByTimeAsync(4_000);
    expect(posted()).toBe(0);
    link.dispatchEvent(new Event("load"));
    await vi.advanceTimersByTimeAsync(3_000);
    expect(posted()).toBe(1);
  });

  // The watcher's text is a template: a rel with more than one token is
  // matched as the browser reads it.
  it("waits on a stylesheet whose rel carries more than one token", async () => {
    vi.useFakeTimers();
    const posted = captureHtml();
    const link = document.createElement("link");
    link.rel = "preload stylesheet";
    document.head.appendChild(link);
    await vi.advanceTimersByTimeAsync(4_000);
    expect(posted()).toBe(0);
    link.dispatchEvent(new Event("load"));
    await vi.advanceTimersByTimeAsync(3_000);
    expect(posted()).toBe(1);
  });

  it("is ready once a failed stylesheet has failed: its absence is the platform's to judge", async () => {
    vi.useFakeTimers();
    const posted = captureHtml();
    const link = document.createElement("link");
    link.rel = "stylesheet";
    document.head.appendChild(link);
    link.dispatchEvent(new Event("error"));
    await vi.advanceTimersByTimeAsync(3_000);
    expect(posted()).toBe(1);
  });

  it("waits for CSS written at run time to stop changing", async () => {
    vi.useFakeTimers();
    const posted = captureHtml();
    const style = document.createElement("style");
    document.head.appendChild(style);
    // A script writing its CSS in pieces, as the Tailwind Play CDN does.
    for (let i = 0; i < 10; i++) {
      style.textContent += `.c${i}{color:red}`;
      await vi.advanceTimersByTimeAsync(200);
    }
    expect(posted()).toBe(0);
    await vi.advanceTimersByTimeAsync(3_000);
    expect(posted()).toBe(1);
  });

  it("is ready at its bound when a stylesheet never answers", async () => {
    vi.useFakeTimers();
    const posted = captureHtml();
    const link = document.createElement("link");
    link.rel = "stylesheet";
    document.head.appendChild(link);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(posted()).toBe(0);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(posted()).toBe(1);
  });

  it("waits for a JSX frame's module to run instead of a fixed delay", async () => {
    vi.useFakeTimers();
    const html = buildJsxThumbnailHtml("export default function App(){ return <div/>; }", "ast-jsx", "https://p.example.com");
    expect(html).not.toContain("2000");
    const scripts = scriptsOf(html);
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => undefined);
    new Function(scripts[scripts.length - 1] ?? "")();
    const posted = () => post.mock.calls.filter((c) => (c[0] as { type?: string }).type === "thumbnail-ready").length;
    // esm.sh is slow: the module has not run after 3 seconds.
    await vi.advanceTimersByTimeAsync(3_000);
    expect(posted()).toBe(0);
    win.__thumbnailModuleRan = true;
    await vi.advanceTimersByTimeAsync(3_000);
    expect(posted()).toBe(1);
  });

  it("sets the module flag at the end of the module the JSX frame runs", () => {
    const html = buildJsxThumbnailHtml("export default function App(){ return <div/>; }", "ast-jsx", "https://p.example.com");
    const module = /<script type="module">([\s\S]*?)<\/script>/.exec(html)?.[1] ?? "";
    expect(module.trimEnd().endsWith("window.__thumbnailModuleRan = true;")).toBe(true);
  });
});
