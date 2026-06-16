import { describe, it, expect } from "vitest";
import { buildJsxThumbnailHtml, injectCaptureScript } from "./thumbnail";

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
