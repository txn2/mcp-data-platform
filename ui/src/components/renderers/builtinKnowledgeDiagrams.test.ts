import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import mermaid from "mermaid";

// The platform's built-in knowledge pages are rendered by MarkdownRenderer,
// which hands a ```mermaid fence to MermaidBlock. A fence mermaid cannot parse
// renders as an error box in the portal and reaches an agent as noise, and the
// pages are shipped in the binary rather than authored in a surface that would
// have caught it. This is where the two sides meet: the renderer's own parser,
// run over every page the Go side embeds.
const PAGES_DIR = path.resolve(
  __dirname,
  "../../../../internal/platform/knowledgebuiltin/pages",
);

function mermaidFences(body: string): string[] {
  const out: string[] = [];
  const re = /^```mermaid\n([\s\S]*?)^```/gm;
  let m: RegExpExecArray | null;
  while ((m = re.exec(body)) !== null) {
    const fence = m[1];
    if (fence !== undefined) out.push(fence);
  }
  return out;
}

const pages = readdirSync(PAGES_DIR).filter((f) => f.endsWith(".md"));

describe("built-in knowledge pages", () => {
  it("ships pages to check", () => {
    expect(pages.length).toBeGreaterThan(0);
  });

  // That every page HAS a diagram is asserted on the Go side, beside the set
  // that declares them; this is only whether the ones there are parse.
  it.each(pages)("%s has diagrams mermaid can parse", async (file) => {
    const body = readFileSync(path.join(PAGES_DIR, file), "utf8");
    const fences = mermaidFences(body);
    expect(fences.length).toBeGreaterThan(0);
    mermaid.initialize({ startOnLoad: false });
    for (const fence of fences) {
      await expect(mermaid.parse(fence)).resolves.toBeTruthy();
    }
  });
});
