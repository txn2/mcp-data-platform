import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { fetchRaw } = vi.hoisted(() => ({ fetchRaw: vi.fn() }));
vi.mock("@/api/portal/client", () => ({ apiFetchRaw: fetchRaw }));

import { buildCSP, REF_PATH_PREFIX } from "@/components/renderers/JsxRenderer";
import { buildJsxThumbnailHtml, injectCaptureScript, isThemeable, uploadThumbnail } from "./thumbnail";

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
  it("is true for content rendered on a forced background", () => {
    expect(isThemeable("text/markdown")).toBe(true);
    expect(isThemeable("text/csv")).toBe(true);
    expect(isThemeable("TEXT/MARKDOWN")).toBe(true);
  });

  it("is false for self-themed content types", () => {
    for (const ct of ["text/html", "text/jsx", "image/svg+xml", "image/png"]) {
      expect(isThemeable(ct)).toBe(false);
    }
  });
});

describe("uploadThumbnail", () => {
  // The uploader builds the whole URL and calls fetch itself, so the test
  // watches fetch. Watching a client mock instead is what let the wrong API
  // root through: the mock supplied the base, so the assertion agreed with a
  // path that 404s in a browser (#1554).
  const fetchMock = vi.fn();

  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({ ok: true, status: 200 });
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const asset = { kind: "asset" as const, id: "ast-1" };
  const resource = { kind: "resource" as const, id: "res-1" };

  it("uploads the light variant with no query param by default", async () => {
    await uploadThumbnail(asset, new Blob(["x"]));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]![0]).toBe("/api/v1/portal/assets/ast-1/thumbnail");
    expect(fetchMock.mock.calls[0]![1]).toMatchObject({ method: "PUT" });
  });

  it("appends ?variant=dark for the dark variant", async () => {
    await uploadThumbnail(asset, new Blob(["x"]), "dark");
    expect(fetchMock.mock.calls[0]![0]).toBe("/api/v1/portal/assets/ast-1/thumbnail?variant=dark");
  });

  // The capturer is the same for both kinds; only the route differs (#1554).
  it("uploads a resource capture to the resource's own route", async () => {
    await uploadThumbnail(resource, new Blob(["x"]));
    expect(fetchMock.mock.calls[0]![0]).toBe("/api/v1/resources/res-1/thumbnail");
  });

  it("sends a resource's dark variant to the same route", async () => {
    await uploadThumbnail(resource, new Blob(["x"]), "dark");
    expect(fetchMock.mock.calls[0]![0]).toBe("/api/v1/resources/res-1/thumbnail?variant=dark");
  });

  // A resource row has no version column: the server stamps the capture with
  // the resource's own updated_at, so nothing is sent.
  it("carries a version only when one is given", async () => {
    await uploadThumbnail(asset, new Blob(["x"]), "light", 4);
    expect(fetchMock.mock.calls[0]![0]).toBe("/api/v1/portal/assets/ast-1/thumbnail?version=4");
  });

  it("throws when the upload response is not ok", async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 404 });
    await expect(uploadThumbnail(asset, new Blob(["x"]))).rejects.toThrow(
      "/api/v1/portal/assets/ast-1/thumbnail answered 404",
    );
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

  it("grants the reference route, which is how a referenced logo and data file load", () => {
    const csp = cspOf(buildJsxThumbnailHtml(CODE, "ast-1", ORIGIN));
    const ref = ORIGIN + REF_PATH_PREFIX;
    expect(csp).toContain(`img-src data: blob: ${ref}`);
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
