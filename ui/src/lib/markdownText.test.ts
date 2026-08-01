import { describe, it, expect } from "vitest";
import { markdownToPlainText } from "./markdownText";

describe("markdownToPlainText", () => {
  it("returns an empty string for empty input", () => {
    expect(markdownToPlainText("")).toBe("");
    expect(markdownToPlainText(null)).toBe("");
    expect(markdownToPlainText(undefined)).toBe("");
  });

  it("unwraps bold, italic and strikethrough", () => {
    expect(markdownToPlainText("**Order header** is _central_ and ~~legacy~~")).toBe(
      "Order header is central and legacy",
    );
    expect(markdownToPlainText("__strong__ and *slanted*")).toBe("strong and slanted");
  });

  it("leaves spaced asterisks and underscores alone", () => {
    expect(markdownToPlainText("2 * 3 * 4 = 24")).toBe("2 * 3 * 4 = 24");
    expect(markdownToPlainText("a _ b _ c")).toBe("a _ b _ c");
  });

  it("keeps snake_case identifiers intact", () => {
    expect(markdownToPlainText("`is_outgoing = TRUE` sets legacy_order_id")).toBe(
      "is_outgoing = TRUE sets legacy_order_id",
    );
  });

  it("flattens headings, bullets and numbered lists onto one line", () => {
    expect(markdownToPlainText("# Title\n\n- first\n- second\n\n1. third")).toBe(
      "Title first second third",
    );
  });

  it("keeps link and image text, drops the target", () => {
    expect(markdownToPlainText("see [the docs](https://example.com/a_b) now")).toBe(
      "see the docs now",
    );
    expect(markdownToPlainText("![a chart](https://example.com/c.png)")).toBe("a chart");
    expect(markdownToPlainText("<https://example.com/x>")).toBe("https://example.com/x");
  });

  it("keeps fenced code contents without the fences", () => {
    expect(markdownToPlainText("run it:\n\n```sql\nSELECT 1\n```\n")).toBe("run it: SELECT 1");
  });

  it("flattens tables and drops the alignment row", () => {
    expect(markdownToPlainText("| col | type |\n| --- | ---- |\n| id | bigint |")).toBe(
      "col type id bigint",
    );
  });

  it("drops blockquote markers, horizontal rules and inline HTML", () => {
    expect(markdownToPlainText("> quoted\n\n---\n\n<b>tagged</b> text")).toBe(
      "quoted tagged text",
    );
  });

  it("leaves entity reference tokens readable", () => {
    expect(markdownToPlainText("joins urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)")).toBe(
      "joins urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)",
    );
  });

  it("collapses runs of whitespace and trims", () => {
    expect(markdownToPlainText("  spaced   out\n\n\ttext  ")).toBe("spaced out text");
  });
});
