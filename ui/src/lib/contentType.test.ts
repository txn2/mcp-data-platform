import { describe, it, expect } from "vitest";
import {
  CT,
  normalizeContentType,
  isActiveType,
  isGenericType,
  isTextualType,
  typeFromFileName,
  detectTextType,
  resolveContentType,
} from "./contentType";

describe("normalizeContentType", () => {
  it.each([
    ["", ""],
    ["application/json", CT.json],
    ["text/json", CT.json],
    ["TEXT/JSON; charset=utf-8", CT.json],
    ["text/xml", CT.xml],
    ["application/x-yaml", CT.yaml],
    ["application/jsonl", CT.ndjson],
    ["image/jpg", "image/jpeg"],
    ["binary/octet-stream", CT.octet],
    ["application/vnd.acme+json", "application/vnd.acme+json"],
  ])("normalizes %s", (input, want) => {
    expect(normalizeContentType(input)).toBe(want);
  });

  it("rejects anything that is not a well-formed media type", () => {
    // The result reaches a Content-Type header and a renderer lookup; letting
    // arbitrary text through either would be a defect.
    for (const bad of ["not a media type", "text/plain\r\nX-Evil: 1", "application", "application/", "; charset=utf-8"]) {
      expect(normalizeContentType(bad)).toBe("");
    }
  });
});

describe("type predicates", () => {
  it("classifies active types", () => {
    for (const ct of ["text/html", "text/jsx", "image/svg+xml", "application/javascript"]) {
      expect(isActiveType(ct)).toBe(true);
    }
    for (const ct of ["application/json", "text/plain", "image/png"]) {
      expect(isActiveType(ct)).toBe(false);
    }
  });

  it("classifies generic declarations", () => {
    for (const ct of ["", "application/octet-stream", "text/plain", "text/plain; charset=utf-8"]) {
      expect(isGenericType(ct)).toBe(true);
    }
    expect(isGenericType("application/json")).toBe(false);
  });

  it("classifies textual types", () => {
    for (const ct of ["text/csv", "application/json", "application/xml", "application/yaml"]) {
      expect(isTextualType(ct)).toBe(true);
    }
    for (const ct of ["image/png", "audio/mpeg", "video/mp4", "application/pdf"]) {
      expect(isTextualType(ct)).toBe(false);
    }
  });
});

describe("typeFromFileName", () => {
  it.each([
    ["results.json", CT.json],
    ["data.CSV", CT.csv],
    ["notes.md", CT.markdown],
    ["chart.png", "image/png"],
    ["clip.mp4", "video/mp4"],
    ["archive.zip", "application/zip"],
    ["noextension", ""],
    ["trailing.", ""],
    ["thing.unknownext", ""],
  ])("maps %s", (name, want) => {
    expect(typeFromFileName(name)).toBe(want);
  });
});

describe("detectTextType", () => {
  it.each([
    ['{"a":1,"b":[1,2]}', CT.json],
    ["[{\"id\":1},{\"id\":2}]", CT.json],
    ["[]", CT.json],
    ['\n\n  {"a": 1}', CT.json],
    ['{"a":1}\n{"a":2}\n{"a":3}\n', CT.ndjson],
    ['<?xml version="1.0"?><catalog/>', CT.xml],
    ["<catalog>\n  <item/>\n</catalog>\n", CT.xml],
    ["---\nname: acme\n", CT.yaml],
    ["%YAML 1.2\n---\na: 1\n", CT.yaml],
    ["id,name,total\n1,acme,10\n2,globex,20\n3,initech,30\n", CT.csv],
    ["id\tname\n1\tacme\n2\tglobex\n3\tinitech\n", CT.tsv],
  ])("detects %s", (content, want) => {
    expect(detectTextType(content)).toBe(want);
  });

  it.each([
    ["prose", "The report is attached.\nRegards,\nOps\n"],
    ["lone brace", "{"],
    ["broken json", '{"a": 1,,,}'],
    ["two-line csv", "a,b\n1,2\n"],
    ["ragged commas", "one, two\nthree\nfour, five, six\n"],
    ["bullet list", "- not yaml\n- just prose\n"],
  ])("leaves %s unclassified", (_name, content) => {
    expect(detectTextType(content)).toBe("");
  });

  it("never classifies content as an active type", () => {
    // The client-side rule matches the server's: sniffing may only land on a
    // passive family, so a mislabelled document cannot become script-bearing.
    const html = "<!DOCTYPE html>\n<html><body><script>alert(1)</script></body></html>";
    const svg = '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>';
    const fragment = '<div class="x">hello</div>';

    for (const content of [html, svg, fragment]) {
      const got = detectTextType(content);
      expect(isActiveType(got)).toBe(false);
    }
  });

  it("classifies a document longer than the sniff window", () => {
    const big = '{"rows":[' + '{"id":1,"name":"acme"},'.repeat(2000) + '{"id":2}]}';
    expect(big.length).toBeGreaterThan(8192);
    expect(detectTextType(big)).toBe(CT.json);
  });
});

describe("resolveContentType", () => {
  it("honors a specific declaration over the content", () => {
    expect(resolveContentType("text/csv", undefined, '{"a":1}')).toBe(CT.csv);
    expect(resolveContentType("text/markdown", "x.json", "# hi")).toBe(CT.markdown);
  });

  it("falls back to the filename when the declaration is generic", () => {
    expect(resolveContentType("application/octet-stream", "chart.png")).toBe("image/png");
    expect(resolveContentType("", "clip.mp3")).toBe("audio/mpeg");
  });

  it("falls back to the content when neither declaration nor filename helps", () => {
    expect(resolveContentType("text/plain", "export", '{"results":[]}')).toBe(CT.json);
    expect(resolveContentType("application/octet-stream", undefined, "a,b\n1,2\n3,4\n")).toBe(CT.csv);
  });

  it("does not let a filename promote a generic declaration to an active type", () => {
    // A ".html" name on a text/plain asset would otherwise render as markup on
    // the strength of a string the author chose.
    expect(resolveContentType("text/plain", "page.html", "plain words")).toBe(CT.plain);
    expect(resolveContentType("application/octet-stream", "chart.svg", "plain words")).not.toBe(CT.svg);
  });

  it("returns a usable type when nothing is known", () => {
    expect(resolveContentType("", undefined, undefined)).toBe(CT.octet);
    expect(resolveContentType("text/plain", undefined, "just words\n")).toBe(CT.plain);
  });
});
