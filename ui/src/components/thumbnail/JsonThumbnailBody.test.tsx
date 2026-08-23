import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import {
  buildJsonLines,
  buildNdjsonRecords,
  tokenizeJsonLine,
  JsonThumbnailBody,
  NdjsonThumbnailBody,
} from "./JsonThumbnailBody";
import { LIGHT_SCHEME } from "./schemes";

const tokens = LIGHT_SCHEME.tokens;

/** The text of a line's runs, which must concatenate back to the source line. */
function textOf(runs: { text: string }[]): string {
  return runs.map((r) => r.text).join("");
}

describe("tokenizeJsonLine", () => {
  it("keeps the line intact across its runs", () => {
    const line = '  "count": -12.5e3,';
    expect(textOf(tokenizeJsonLine(line))).toBe(line);
  });

  it("separates a key from a string value by the colon that follows it", () => {
    const runs = tokenizeJsonLine('"name": "value"');
    expect(runs.filter((r) => r.tone === "key").map((r) => r.text)).toEqual(['"name"']);
    expect(runs.filter((r) => r.tone === "string").map((r) => r.text)).toEqual(['"value"']);
  });

  it("does not read a literal or a number out of the inside of a string", () => {
    const runs = tokenizeJsonLine('"note": "true 42 null"');
    expect(runs.some((r) => r.tone === "literal")).toBe(false);
    expect(runs.some((r) => r.tone === "number")).toBe(false);
  });

  it("tones each scalar family", () => {
    const runs = tokenizeJsonLine("[1, true, null]");
    const tones = runs.filter((r) => r.tone !== "punct").map((r) => `${r.tone}:${r.text}`);
    expect(tones).toEqual(["number:1", "literal:true", "literal:null"]);
  });
});

describe("buildJsonLines", () => {
  it("re-indents a document that arrived on one line", () => {
    const lines = buildJsonLines('{"a":1,"b":[2]}');
    expect(lines.map(textOf)).toEqual(['{', '  "a": 1,', '  "b": [', "    2", "  ]", "}"]);
  });

  // The viewer falls back to the raw source with the parser's message; the
  // capture has no room for a message, so it draws the source alone. Either
  // way the reader sees the document rather than a placeholder icon.
  it("draws the source when the document does not parse", () => {
    const lines = buildJsonLines('{"a": oops}');
    expect(lines.map(textOf)).toEqual(['{"a": oops}']);
  });

  it("stops after the lines a 400x300 capture could show", () => {
    const big = JSON.stringify(Object.fromEntries(Array.from({ length: 200 }, (_, i) => [`k${i}`, i])));
    expect(buildJsonLines(big).length).toBe(44);
  });
});

describe("buildNdjsonRecords", () => {
  it("numbers records by the line they are on, skipping blanks", () => {
    const records = buildNdjsonRecords('{"a":1}\n\n{"a":2}\n');
    expect(records.map((r) => r.line)).toEqual([1, 3]);
    expect(records.map((r) => textOf(r.tokens))).toEqual(['{"a":1}', '{"a":2}']);
  });

  it("stops after the records a capture could show", () => {
    const many = Array.from({ length: 50 }, (_, i) => `{"i":${i}}`).join("\n");
    expect(buildNdjsonRecords(many).length).toBe(18);
  });

  it("clips a record too long to draw", () => {
    const long = `{"v":"${"x".repeat(500)}"}`;
    const records = buildNdjsonRecords(long);
    expect(textOf(records[0]!.tokens).length).toBe(240);
  });

  // A record that is not JSON still lists: the viewer shows it as a line with a
  // parse complaint, and the capture shows the line.
  it("lists a record that does not parse", () => {
    expect(buildNdjsonRecords("not json\n").map((r) => textOf(r.tokens))).toEqual(["not json"]);
  });
});

describe("JsonThumbnailBody", () => {
  it("draws one element per line with the keys toned apart from the values", () => {
    const { container } = render(
      <JsonThumbnailBody lines={buildJsonLines('{"name":"acme"}')} tokens={tokens} scope="tg-1" />,
    );
    expect(container.querySelectorAll("pre > div")).toHaveLength(3);
    expect(container.querySelector(".jt-key")?.textContent).toBe('"name"');
    expect(container.querySelector(".jt-string")?.textContent).toBe('"acme"');
    expect(container.querySelector("style")?.textContent).toContain(tokens.jsonKey);
  });
});

describe("NdjsonThumbnailBody", () => {
  it("draws a numbered row per record", () => {
    const { container } = render(
      <NdjsonThumbnailBody records={buildNdjsonRecords('{"a":1}\n{"a":2}')} tokens={tokens} scope="tg-2" />,
    );
    const rows = container.querySelectorAll("tbody tr");
    expect(rows).toHaveLength(2);
    expect(Array.from(container.querySelectorAll("td.jt-line")).map((c) => c.textContent)).toEqual(["1", "2"]);
  });
});
