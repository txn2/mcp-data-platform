import { describe, it, expect, vi, beforeEach } from "vitest";

const { fetchRaw } = vi.hoisted(() => ({ fetchRaw: vi.fn() }));
vi.mock("@/api/portal/client", () => ({ apiFetchRaw: fetchRaw }));

import { buildJsxThumbnailHtml, injectCaptureScript, isThemeable, uploadThumbnail } from "./thumbnail";

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

  it("appends the notifier when no </body> is present", () => {
    const html = "<div>content</div>";
    const out = injectCaptureScript(html);
    expect(out.startsWith(html)).toBe(true);
    expect(out).toContain("thumbnail-ready");
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
  beforeEach(() => {
    fetchRaw.mockReset();
    fetchRaw.mockResolvedValue({ ok: true });
  });

  it("uploads the light variant with no query param by default", async () => {
    await uploadThumbnail("ast-1", new Blob(["x"]));
    expect(fetchRaw).toHaveBeenCalledTimes(1);
    expect(fetchRaw.mock.calls[0]![0]).toBe("/assets/ast-1/thumbnail");
    expect(fetchRaw.mock.calls[0]![1]).toMatchObject({ method: "PUT" });
  });

  it("appends ?variant=dark for the dark variant", async () => {
    await uploadThumbnail("ast-1", new Blob(["x"]), "dark");
    expect(fetchRaw.mock.calls[0]![0]).toBe("/assets/ast-1/thumbnail?variant=dark");
  });

  it("throws when the upload response is not ok", async () => {
    fetchRaw.mockResolvedValue({ ok: false });
    await expect(uploadThumbnail("ast-1", new Blob(["x"]))).rejects.toThrow("Failed to upload thumbnail");
  });
});
