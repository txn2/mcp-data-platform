import { describe, it, expect } from "vitest";
import { sanitizeMermaidSvg } from "./MarkdownRenderer";

// Mermaid v11 emits node labels as HTML inside an SVG <foreignObject> (the
// default `htmlLabels: true` path), while subgraph/cluster titles are plain
// SVG <text>. Issue #521: a DOMPurify `svg`-only profile stripped the
// foreignObject HTML, so node labels rendered invisible while titles survived.
// These tests pin the sanitizer to a behavior that keeps both visible without
// reintroducing an XSS hole.
describe("sanitizeMermaidSvg", () => {
  // A representative fragment of mermaid v11 flowchart output: one node whose
  // label is an HTML span inside a foreignObject, and one cluster title as SVG
  // text.
  const mermaidSvg = `
    <svg role="graphics-document document" viewBox="0 0 200 200" class="flowchart">
      <g class="node default">
        <foreignObject width="120" height="24">
          <div xmlns="http://www.w3.org/1999/xhtml" style="display: table-cell;">
            <span class="nodeLabel"><p>Node label that should be visible</p></span>
          </div>
        </foreignObject>
      </g>
      <g class="cluster">
        <text class="cluster-label"><tspan>Subgraph title that IS visible</tspan></text>
      </g>
    </svg>`;

  it("preserves HTML node labels rendered inside a foreignObject", () => {
    const out = sanitizeMermaidSvg(mermaidSvg);
    expect(out).toContain("Node label that should be visible");
    expect(out.toLowerCase()).toContain("foreignobject");
    expect(out).toContain("nodeLabel");
  });

  it("preserves SVG-text subgraph titles (no regression)", () => {
    const out = sanitizeMermaidSvg(mermaidSvg);
    expect(out).toContain("Subgraph title that IS visible");
    expect(out).toContain("cluster-label");
  });

  it("still strips <script> elements", () => {
    const malicious = `<svg><script>window.__pwned = true;</script><text>ok</text></svg>`;
    const out = sanitizeMermaidSvg(malicious);
    expect(out).not.toContain("__pwned");
    expect(out.toLowerCase()).not.toContain("<script");
    expect(out).toContain("ok");
  });

  it("still strips inline event-handler attributes", () => {
    const malicious = `<svg><g class="node" onclick="alert(1)"><text>label</text></g></svg>`;
    const out = sanitizeMermaidSvg(malicious);
    expect(out.toLowerCase()).not.toContain("onclick");
    expect(out).toContain("label");
  });

  it("strips event handlers on HTML smuggled inside a foreignObject", () => {
    const malicious = `<svg><foreignObject><div xmlns="http://www.w3.org/1999/xhtml"><img src="x" onerror="alert(1)">payload</div></foreignObject></svg>`;
    const out = sanitizeMermaidSvg(malicious);
    expect(out.toLowerCase()).not.toContain("onerror");
    // the benign text content survives; the handler does not
    expect(out).toContain("payload");
  });

  // The fix deliberately keeps HTML alive inside foreignObject, so the most
  // important guard is that genuinely dangerous elements smuggled there are
  // still neutralized. This also fails loudly if MERMAID_FORBID_CONTENTS ever
  // stops content-stripping these tags (e.g. a careless edit or a DOMPurify
  // upgrade that changes the default forbid-contents list).
  it("strips <script> smuggled inside a foreignObject", () => {
    const malicious = `<svg><foreignObject><div xmlns="http://www.w3.org/1999/xhtml"><script>window.__pwned = true;</script>safe</div></foreignObject></svg>`;
    const out = sanitizeMermaidSvg(malicious);
    expect(out).not.toContain("__pwned");
    expect(out.toLowerCase()).not.toContain("<script");
    expect(out).toContain("safe");
  });

  it("strips <iframe> smuggled inside a foreignObject", () => {
    const malicious = `<svg><foreignObject><div xmlns="http://www.w3.org/1999/xhtml"><iframe src="javascript:alert(1)"></iframe>safe</div></foreignObject></svg>`;
    const out = sanitizeMermaidSvg(malicious);
    expect(out.toLowerCase()).not.toContain("<iframe");
    expect(out).toContain("safe");
  });
});
