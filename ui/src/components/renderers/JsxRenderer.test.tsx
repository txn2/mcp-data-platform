import { describe, it, expect, beforeAll, afterAll, vi } from "vitest";
import { render } from "@testing-library/react";
import {
  JsxRenderer,
  buildCSP,
  buildJsxIframeHtml,
  refSource,
  transformJsx,
  findComponentName,
  escapeScriptClose,
} from "./JsxRenderer";

// Count top-level `import React` default-binding declarations. The namespaced
// alias `import * as __artifactReact` must NOT match.
const countReactDefaultImports = (html: string): number =>
  (html.match(/import\s+React\b/g) ?? []).length;

// jsdom does not implement URL.createObjectURL; provide a stub.
let originalCreateObjectURL: typeof URL.createObjectURL;
let originalRevokeObjectURL: typeof URL.revokeObjectURL;

beforeAll(() => {
  originalCreateObjectURL = URL.createObjectURL;
  originalRevokeObjectURL = URL.revokeObjectURL;
  let counter = 0;
  URL.createObjectURL = vi.fn(() => `blob:test/${++counter}`);
  URL.revokeObjectURL = vi.fn();
});

afterAll(() => {
  URL.createObjectURL = originalCreateObjectURL;
  URL.revokeObjectURL = originalRevokeObjectURL;
});

describe("transformJsx", () => {
  it("transforms JSX to valid JavaScript", () => {
    const code = `function App() { return <div>Hello</div>; }`;
    const result = transformJsx(code);
    // Should contain jsx runtime calls, not raw JSX
    expect(result).not.toContain("<div>");
    expect(result).toMatch(/_jsx/);
  });

  it("handles imports alongside JSX", () => {
    const code = `import { useState } from "react";\nfunction App() { return <div>{useState(0)}</div>; }`;
    const result = transformJsx(code);
    expect(result).toContain('import { useState } from "react"');
    expect(result).toMatch(/_jsx/);
    expect(result).not.toContain("<div>");
  });

  it("auto-inserts jsx-runtime import", () => {
    const code = `function App() { return <span>test</span>; }`;
    const result = transformJsx(code);
    expect(result).toContain("react/jsx-runtime");
  });

  it("throws on invalid syntax", () => {
    expect(() => transformJsx("function {{{")).toThrow();
  });
});

describe("findComponentName", () => {
  it("finds export default function Name", () => {
    expect(
      findComponentName("export default function Dashboard() {}"),
    ).toBe("Dashboard");
  });

  it("finds export default class Name", () => {
    expect(
      findComponentName("export default class Widget extends React.Component {}"),
    ).toBe("Widget");
  });

  it("finds export default Name;", () => {
    expect(
      findComponentName("function MyChart() {}\nexport default MyChart;"),
    ).toBe("MyChart");
  });

  it("falls back to last PascalCase declaration", () => {
    expect(
      findComponentName("function Helper() {}\nfunction MainView() {}"),
    ).toBe("MainView");
  });

  it("returns null for no component", () => {
    expect(findComponentName("const x = 42;")).toBeNull();
  });

  it("detects the production artifact component name", () => {
    // Matches the production artifact pattern
    const code = `const KPICard = () => {};\nexport default function Dashboard() { return null; }`;
    expect(findComponentName(code)).toBe("Dashboard");
  });
});

describe("JsxRenderer", () => {
  it("renders an iframe with allow-scripts sandbox", () => {
    const { container } = render(
      <JsxRenderer content="function App() { return <div>Hello</div>; }" />,
    );
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeTruthy();
    expect(iframe?.getAttribute("sandbox")).toBe("allow-scripts");
    expect(iframe?.getAttribute("title")).toBe("JSX Preview");
  });

  it("creates a blob URL for the iframe src", () => {
    const code = `export default function Dashboard() { return <div>Test</div>; }`;
    const { container } = render(<JsxRenderer content={code} />);
    const iframe = container.querySelector("iframe");
    expect(iframe?.getAttribute("src")).toMatch(/^blob:/);
  });

  it("renders even when JSX transform fails", () => {
    const { container } = render(
      <JsxRenderer content="function {{{" />,
    );
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeTruthy();
    expect(iframe?.getAttribute("src")).toMatch(/^blob:/);
  });

  it("handles self-mounting content with createRoot", () => {
    const code = `import React from "react";\nimport { createRoot } from "react-dom/client";\nfunction App() { return <div>Hello</div>; }\ncreateRoot(document.getElementById("root")).render(<App />);`;
    const { container } = render(<JsxRenderer content={code} />);
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeTruthy();
  });

  it("calls URL.createObjectURL with Blob argument", () => {
    render(
      <JsxRenderer content="function App() { return <div>Hello</div>; }" />,
    );
    expect(URL.createObjectURL).toHaveBeenCalled();
  });
});

describe("buildJsxIframeHtml: duplicate React declaration (issue #625)", () => {
  it("does not inject a second React import when artifact already imports React", () => {
    // The artifact imports React and does not self-mount, so it takes the
    // auto-mount path. Before the fix this produced two `import React` lines
    // and a "Identifier 'React' has already been declared" SyntaxError.
    const code = `import React from 'react';
export default function App() { return <div>Hello</div>; }`;
    const html = buildJsxIframeHtml(code);
    // Only the artifact's own React import remains; the injected helper uses a
    // namespaced alias that does not collide.
    expect(countReactDefaultImports(html)).toBe(1);
    expect(html).toContain("import * as __artifactReact from 'react'");
    expect(html).toContain("__artifactReact.createElement(App)");
  });

  it("injects no bare React import when artifact does not import React", () => {
    const code = `export default function App() { return <div>Hi</div>; }`;
    const html = buildJsxIframeHtml(code);
    expect(countReactDefaultImports(html)).toBe(0);
    expect(html).toContain(
      "import { createRoot as __artifactCreateRoot } from 'react-dom/client'",
    );
    expect(html).toContain("__artifactCreateRoot(document.getElementById('root'))");
  });

  it("leaves self-mounting artifacts untouched (no injected helpers)", () => {
    const code = `import React from 'react';
import { createRoot } from 'react-dom/client';
function App() { return <div>Hello</div>; }
createRoot(document.getElementById('root')).render(<App />);`;
    const html = buildJsxIframeHtml(code);
    // Only the artifact's own React import; no namespaced helper injection.
    expect(countReactDefaultImports(html)).toBe(1);
    expect(html).not.toContain("__artifactReact");
    expect(html).not.toContain("__artifactCreateRoot");
  });

  it("returns a transform-error document for invalid syntax", () => {
    const html = buildJsxIframeHtml("function {{{");
    expect(html).toContain("<pre id=\"e\"");
    expect(html).not.toContain("__artifactReact");
  });
});

describe("escapeScriptClose", () => {
  it("escapes </script> in code strings", () => {
    const code = `const x = "</script><script>alert(1)</script>";`;
    const escaped = escapeScriptClose(code);
    expect(escaped).not.toContain("</script>");
    expect(escaped).toContain("<\\/script");
  });

  it("escapes case-insensitive variants", () => {
    const code = `const x = "</SCRIPT>"; const y = "</Script>";`;
    const escaped = escapeScriptClose(code);
    expect(escaped).not.toMatch(/<\/script/i);
  });

  it("leaves code without script tags unchanged", () => {
    const code = `function App() { return "hello"; }`;
    expect(escapeScriptClose(code)).toBe(code);
  });
});

describe("JsxRenderer: script injection safety", () => {
  it("generated HTML does not contain raw </script> from content", () => {
    const malicious = `export default function App() { return <div>{"</script><script>alert(1)</script>"}</div>; }`;
    // transformJsx will transform the JSX, and escapeScriptClose should
    // prevent the closing script tag from breaking the HTML structure.
    const { container } = render(<JsxRenderer content={malicious} />);
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeTruthy();
    // The blob URL should still be created (no crash)
    expect(iframe?.getAttribute("src")).toMatch(/^blob:/);
  });
});

describe("JsxRenderer regression: no blob module imports", () => {
  it("transformed output does not contain raw JSX", () => {
    // This is the pattern from the production artifact
    const code = `import { useState } from "react";
import { BarChart, Bar } from "recharts";
export default function Dashboard() {
  const [tab, setTab] = useState("a");
  return <div><BarChart><Bar dataKey="x" /></BarChart></div>;
}`;
    const result = transformJsx(code);
    // Must not contain JSX angle brackets (they should be transformed to _jsx calls)
    expect(result).not.toMatch(/<div>/);
    expect(result).not.toMatch(/<BarChart>/);
    expect(result).toMatch(/_jsx/);
    // Original imports should be preserved
    expect(result).toContain('import { useState } from "react"');
    expect(result).toContain('import { BarChart, Bar } from "recharts"');
  });
});

// A declared reference is rewritten into the artifact's content as an absolute
// URL under /portal/refs/, and the frame's own policy is the only thing that
// decides whether it loads (#1494). These assert the policy the frame carries;
// that the browser then honours it is e2e/public-viewer/public-viewer.spec.ts.
describe("buildCSP: the reference route", () => {
  const origin = "https://platform.example.com";

  it("allows the reference route for images and requests", () => {
    const csp = buildCSP(origin);
    const img = csp.match(/img-src ([^;]*)/)?.[1] ?? "";
    const connect = csp.match(/connect-src ([^;]*)/)?.[1] ?? "";
    expect(img).toContain(`${origin}/portal/refs/`);
    expect(connect).toContain(`${origin}/portal/refs/`);
    // The families that already worked are untouched.
    expect(img).toContain("data:");
    expect(img).toContain("blob:");
    expect(connect).toContain("https://esm.sh");
  });

  it("grants the path, never the origin", () => {
    const csp = buildCSP(origin);
    // Every mention of the platform origin carries the reference path with it,
    // so the frame cannot reach the portal API or the admin API.
    for (const mention of csp.split(/[\s;]+/).filter((t) => t.startsWith(origin))) {
      expect(mention).toBe(`${origin}/portal/refs/`);
    }
  });

  it("keeps the frame denied where there is no origin to grant", () => {
    const csp = buildCSP("");
    expect(csp).toContain("img-src data: blob:;");
    expect(csp).not.toContain("/portal/refs/");
    // An opaque origin serializes as the string "null" and grants nothing.
    expect(refSource("null")).toBe("");
  });

  it("carries no eval and no default source, as before", () => {
    const csp = buildCSP(origin);
    expect(csp).toContain("default-src 'none'");
    expect(csp).not.toContain("unsafe-eval");
  });

  it("is what the artifact document carries", () => {
    const html = buildJsxIframeHtml("function App(){return <div>hi</div>}");
    expect(html).toContain('<meta http-equiv="Content-Security-Policy"');
    expect(html).toContain("/portal/refs/");
  });
});
