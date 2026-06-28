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

import { render } from "@testing-library/react";
import { MarkdownRenderer } from "./MarkdownRenderer";
import type { ResolvedRef } from "@/lib/entityRefs";

describe("MarkdownRenderer entity chips", () => {
  it("renders an mcp: link as a chip (urlTransform must preserve the ref href)", () => {
    const { container } = render(
      <MarkdownRenderer content="See [Sales](mcp:asset:asset-001) here." />,
    );
    // The fallback label (the id) appears, and it is NOT a raw mcp: anchor.
    expect(container.textContent).toContain("asset-001");
    expect(container.querySelector('a[href^="mcp:"]')).toBeNull();
  });

  it("chips a BARE mcp: token in body text (not just markdown links) (#678)", () => {
    const { container } = render(
      <MarkdownRenderer content="Configured in mcp:connection:(trino,acme) for promotions." />,
    );
    // The bare token becomes a chip showing the derived label, not raw text.
    expect(container.textContent).toContain("acme (trino)");
    expect(container.textContent).not.toContain("mcp:connection:(trino,acme)");
  });

  it("chips a BARE urn:li: dataset token in body text (#678)", () => {
    const { container } = render(
      <MarkdownRenderer content="Query urn:li:dataset:(urn:li:dataPlatform:trino,opensearch.default.os_acme_transactions,PROD) carefully." />,
    );
    expect(container.textContent).toContain("os_acme_transactions");
  });

  it("splits trailing sentence punctuation out of a bare-token chip (#704)", () => {
    // The token abuts a period in prose; the chip must resolve against the
    // trimmed ref (so the server-resolved label shows) and the period must
    // remain as ordinary text, not be swallowed into the chip's id.
    const refs = new Map<string, ResolvedRef>([
      ["mcp:asset:asset-001", { urn: "mcp:asset:asset-001", type: "asset", label: "Q4 Dashboard", exists: true, accessible: true }],
    ]);
    const { container } = render(
      <MarkdownRenderer content="The fact lives in mcp:asset:asset-001. Done." refs={refs} />,
    );
    // Resolved label appears (proves the chip url was trimmed to match the key).
    expect(container.textContent).toContain("Q4 Dashboard");
    // The punctuated id never renders, and the sentence period survives as prose.
    expect(container.textContent).not.toContain("asset-001.");
    expect(container.textContent).toContain(". Done.");
  });

  it("does not chip a ref token inside an inline code span", () => {
    const { container } = render(
      <MarkdownRenderer content="Use the literal `mcp:asset:asset-001` token." />,
    );
    // Inside code it stays literal text, not a chip.
    expect(container.querySelector("code")?.textContent).toContain("mcp:asset:asset-001");
  });

  it("renders the server-resolved label when provided", () => {
    const refs = new Map<string, ResolvedRef>([
      ["mcp:asset:asset-001", { urn: "mcp:asset:asset-001", type: "asset", label: "Q4 Dashboard", exists: true, accessible: true }],
    ]);
    const { container } = render(
      <MarkdownRenderer content="[x](mcp:asset:asset-001)" refs={refs} />,
    );
    expect(container.textContent).toContain("Q4 Dashboard");
  });

  it("renders an inaccessible ref as plain link text, not a chip", () => {
    const refs = new Map<string, ResolvedRef>([
      ["mcp:asset:secret", { urn: "mcp:asset:secret", type: "asset", label: "mcp:asset:secret", exists: false, accessible: false }],
    ]);
    const { container } = render(
      <MarkdownRenderer content="see [the dashboard](mcp:asset:secret)" refs={refs} />,
    );
    expect(container.textContent).toContain("the dashboard");
    expect(container.textContent).not.toContain("mcp:asset:secret");
    expect(container.querySelector("span.not-prose")).toBeNull();
  });

  it("leaves ordinary links untouched", () => {
    const { container } = render(
      <MarkdownRenderer content="[home](https://example.com)" />,
    );
    expect(container.querySelector('a[href="https://example.com"]')).not.toBeNull();
  });
});
