import { describe, it, expect } from "vitest";
import { withCheckPolicy } from "./htmlRenderCheck";

// The policy the render check lays each copy out under (#1839). Whether the
// comparison itself sees a white-space: pre element is a question for a
// browser, and e2e/interactive/source-format.spec.ts asks it there.

const POLICY = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline' https:">`;

describe("withCheckPolicy", () => {
  it("goes after the doctype, so the document keeps its rendering mode", () => {
    expect(withCheckPolicy("<!DOCTYPE html><html><head></head></html>")).toBe(
      `<!DOCTYPE html>${POLICY}<html><head></head></html>`,
    );
    expect(withCheckPolicy("  <!doctype html>\n<p>x</p>")).toBe(`  <!doctype html>${POLICY}\n<p>x</p>`);
  });

  it("leads a document with no doctype", () => {
    expect(withCheckPolicy("<p>x</p>")).toBe(`${POLICY}<p>x</p>`);
  });

  it("lands in the head, where a policy is honored", () => {
    const doc = new DOMParser().parseFromString(withCheckPolicy("<!DOCTYPE html><p>x</p>"), "text/html");
    expect(doc.head.querySelector('meta[http-equiv="Content-Security-Policy"]')).not.toBeNull();
    expect(doc.compatMode).toBe("CSS1Compat");
  });
});
