import { describe, it, expect, vi } from "vitest";
import agentReport from "@/test/fixtures/source-format/agent-report.html?raw";
import whitespace from "@/test/fixtures/source-format/whitespace.html?raw";
import {
  formatErrorMessage,
  formatForEditor,
  formatParserFor,
  formatSource,
  longestLineLength,
  overflowsEditor,
  RENDER_CHANGED_MESSAGE,
} from "./sourceFormat";

// The Format action of the source editor (#1839), against the real Prettier
// plugins the portal loads. What the browser renders from the output is held
// by e2e/interactive/source-format.spec.ts; these hold what the text is.

/** The text between an element's tags, as written. */
function bodyOf(html: string, tag: string): string {
  const match = new RegExp(`<${tag}[^>]*>([\\s\\S]*?)</${tag}>`).exec(html);
  expect(match, `no <${tag}> in the output`).not.toBeNull();
  return match![1]!;
}

describe("formatParserFor", () => {
  it.each([
    ["text/html", undefined, "html"],
    ["application/json", undefined, "json"],
    ["application/vnd.acme.report+json", undefined, "json"],
    ["text/jsx", undefined, "babel"],
    ["text/javascript", undefined, "babel"],
    ["application/yaml", undefined, "yaml"],
    ["text/markdown", undefined, "markdown"],
    ["text/plain", "report.md", "markdown"],
  ])("formats %s (%s) with %s", (contentType, fileName, parser) => {
    expect(formatParserFor(contentType, fileName)).toBe(parser);
  });

  it.each([
    // One document per line: reindenting it would no longer be JSON Lines.
    ["application/x-ndjson"],
    ["text/x-python"],
    ["application/sql"],
    ["application/xml"],
    ["image/svg+xml"],
    ["text/csv"],
    ["text/plain"],
  ])("offers no formatter for %s", (contentType) => {
    expect(formatParserFor(contentType)).toBeUndefined();
  });
});

describe("formatSource: HTML", () => {
  it("puts a single-line report one element per line, nested, with its style and script formatted", async () => {
    expect(agentReport.split("\n")[0]!.length).toBeGreaterThan(3000);

    const out = await formatSource(agentReport, "html");

    expect(out).toContain('\n  <head>\n    <meta charset="utf-8" />');
    expect(out).toContain("\n    <div class=\"kpis\">\n      <div class=\"kpi\">\n        <div class=\"label\">Revenue</div>");
    // The <style> body as CSS: one declaration per line.
    expect(bodyOf(out, "style")).toContain("      .kpis {\n        display: grid;\n");
    // The <script> body as JavaScript: one statement per line.
    expect(bodyOf(out, "script")).toContain(
      '\n      const total = rows.reduce((s, r) => s + r.revenue, 0);\n',
    );
    expect(longestLineLength(out)).toBeLessThan(260);
  });

  it("leaves <pre> and <textarea> bodies as written", async () => {
    const out = await formatSource(whitespace, "html");
    // A newline right after the opening tag is dropped by the HTML parser, so
    // the leading one Prettier adds does not reach the page.
    expect(bodyOf(out, "pre").replace(/^\n/, "")).toBe(bodyOf(whitespace, "pre"));
    expect(bodyOf(out, "textarea").replace(/^\n/, "")).toBe(bodyOf(whitespace, "textarea"));
    expect(bodyOf(out, "pre")).toContain("\ttab\ntrailing   ");
  });

  it("adds no space between inline runs the author wrote touching", async () => {
    const out = await formatSource(whitespace, "html");
    expect(out).toContain("<b>bold</b><i>italic</i><code>code</code>and text touching");
    expect(out).toContain('<span class="chip">one</span><span class="chip">two</span>');
    expect(out).toContain("1<sup>st</sup>, H<sub>2</sub>O.");
  });

  it.each([
    ["<p>hi</div>", 'Unexpected closing tag "div"'],
    ["<div", 'Opening tag "div" not terminated'],
  ])("rejects %j rather than returning part of it", async (input, message) => {
    await expect(formatSource(input, "html")).rejects.toThrow(message);
  });
});

describe("formatSource: the other families", () => {
  it("reindents JSON without reordering it", async () => {
    const out = await formatSource('{"b":1,"a":[1,2,{"c":null}]}', "json");
    expect(out).toBe('{ "b": 1, "a": [1, 2, { "c": null }] }\n');
    const long = await formatSource(`{"rows":[${'{"region":"West","revenue":1712000},'.repeat(3)}{"x":1}]}`, "json");
    expect(long.split("\n")[1]).toBe('  "rows": [');
  });

  it("rejects JSON that does not parse", async () => {
    await expect(formatSource('{"a":', "json")).rejects.toThrow();
  });

  it("formats JSX", async () => {
    const out = await formatSource("export default function A(){return <div className='x'><b>hi</b></div>}", "babel");
    expect(out).toContain("export default function A() {\n  return (\n    <div className=\"x\">");
  });

  it("formats YAML and Markdown", async () => {
    expect(await formatSource("a:   1\nb:\n    - x", "yaml")).toBe("a: 1\nb:\n  - x\n");
    expect(await formatSource("#  Title\n*  one\n*  two", "markdown")).toBe("# Title\n\n- one\n- two\n");
  });
});

describe("formatForEditor", () => {
  it("returns HTML output when it renders the same text", async () => {
    const same = vi.fn().mockResolvedValue(true);
    const out = await formatForEditor("<div><p>a</p></div>", "html", same);
    expect(out).toBe("<div><p>a</p></div>\n");
    expect(same).toHaveBeenCalledWith("<div><p>a</p></div>", out);
  });

  it("refuses HTML output that would render differently", async () => {
    const same = vi.fn().mockResolvedValue(false);
    await expect(formatForEditor(whitespace, "html", same)).rejects.toThrow(RENDER_CHANGED_MESSAGE);
  });

  it("does not render-check the families whose output has no layout", async () => {
    const same = vi.fn();
    await formatForEditor('{"a":1}', "json", same);
    expect(same).not.toHaveBeenCalled();
  });

  it("does not render-check output identical to its input", async () => {
    const same = vi.fn();
    await formatForEditor("<p>a</p>\n", "html", same);
    expect(same).not.toHaveBeenCalled();
  });
});

describe("formatErrorMessage", () => {
  it("keeps the first sentence and the position of a syntax error", () => {
    const prettier = new Error(
      'Unexpected closing tag "div". It may happen when the tag has already been closed by another tag. For more info see https://www.w3.org/TR/html5/syntax.html#closing-elements-that-have-implied-end-tags (1:12)\n> 1 | <p>hi</div>\n    |            ^',
    );
    expect(formatErrorMessage(prettier)).toBe('Unexpected closing tag "div". (1:12)');
    expect(formatErrorMessage(new Error('Opening tag "div" not terminated. (1:1)'))).toBe(
      'Opening tag "div" not terminated. (1:1)',
    );
  });

  it("keeps a message with no position as it is", () => {
    expect(formatErrorMessage(new Error("Unexpected token"))).toBe("Unexpected token");
    expect(formatErrorMessage("boom")).toBe("boom");
  });

  it("says something for an empty error", () => {
    expect(formatErrorMessage(new Error(""))).toBe("The source could not be formatted.");
  });

  it("shortens the real error Prettier raises", async () => {
    const err = await formatSource("<p>hi</div>", "html").catch((e: unknown) => e);
    expect(formatErrorMessage(err)).toBe('Unexpected closing tag "div". (1:6)');
  });
});

describe("longestLineLength and overflowsEditor", () => {
  it("measures the longest line", () => {
    expect(longestLineLength("")).toBe(0);
    expect(longestLineLength("abc")).toBe(3);
    expect(longestLineLength("a\nabcd\nab")).toBe(4);
    expect(longestLineLength("ab\n")).toBe(2);
  });

  it("overflows when the longest line is wider than the text area", () => {
    expect(overflowsEditor("x".repeat(101), 8, 800)).toBe(true);
    expect(overflowsEditor("x".repeat(100), 8, 800)).toBe(false);
  });

  it("never overflows an editor with no width yet", () => {
    expect(overflowsEditor("x".repeat(1000), 8, 0)).toBe(false);
    expect(overflowsEditor("x".repeat(1000), 0, 800)).toBe(false);
  });
});
