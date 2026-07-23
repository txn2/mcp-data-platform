import { describe, it, expect } from "vitest";
import {
  resolveRenderer,
  isEditableContent,
  rendersFromURL,
  exceedsInlineLimit,
  languageForContentType,
  familyLabel,
  TEXT_INLINE_LIMIT,
  VIRTUALIZED_INLINE_LIMIT,
} from "./registry";

describe("resolveRenderer", () => {
  it.each([
    ["application/json", "json"],
    ["text/json", "json"],
    ["application/x-ndjson", "ndjson"],
    ["text/csv", "table"],
    ["text/tab-separated-values", "table"],
    ["text/markdown", "markdown"],
    ["text/html", "html"],
    ["text/jsx", "jsx"],
    ["image/svg+xml", "svg"],
    ["application/xml", "code"],
    ["application/yaml", "code"],
    ["application/sql", "code"],
    ["text/x-python", "code"],
    ["text/plain", "text"],
    ["application/pdf", "pdf"],
    ["image/png", "image"],
    ["image/avif", "image"],
    ["audio/mpeg", "audio"],
    ["video/webm", "video"],
    ["application/zip", "binary"],
    ["application/octet-stream", "binary"],
  ])("routes %s to the %s renderer", (contentType, kind) => {
    expect(resolveRenderer({ contentType }).kind).toBe(kind);
  });

  it("routes structured suffixes by their syntax", () => {
    expect(resolveRenderer({ contentType: "application/vnd.acme.report+json" }).kind).toBe("json");
    expect(resolveRenderer({ contentType: "application/atom+xml" }).kind).toBe("code");
  });

  it("falls back to content detection for a generically typed asset", () => {
    // The case issue #1007 exists for: an api_export asset saved before the
    // server settled types, still carrying application/octet-stream.
    const entry = resolveRenderer({
      contentType: "application/octet-stream",
      content: '{"results":[{"id":1}],"total":1}',
    });
    expect(entry.kind).toBe("json");
    expect(entry.contentType).toBe("application/json");
  });

  it("falls back to the filename when there is no content in hand", () => {
    expect(resolveRenderer({ contentType: "", fileName: "chart.png" }).kind).toBe("image");
  });

  it("never resolves detected content to an active renderer", () => {
    const html = "<!DOCTYPE html>\n<html><body><script>alert(1)</script></body></html>";
    const entry = resolveRenderer({ contentType: "text/plain", content: html });
    expect(["html", "jsx", "svg"]).not.toContain(entry.kind);
  });
});

describe("renderer capabilities", () => {
  it("marks text families editable and media families not", () => {
    for (const ct of ["application/json", "text/csv", "text/markdown", "text/html", "application/yaml"]) {
      expect(isEditableContent(ct)).toBe(true);
    }
    for (const ct of ["image/png", "audio/mpeg", "video/mp4", "application/pdf", "application/octet-stream"]) {
      expect(isEditableContent(ct)).toBe(false);
    }
  });

  it("marks binary families as URL-sourced", () => {
    for (const ct of ["image/png", "audio/mpeg", "video/mp4", "application/pdf", "application/zip"]) {
      expect(rendersFromURL(ct)).toBe(true);
    }
    for (const ct of ["application/json", "text/csv", "text/plain", "text/html"]) {
      expect(rendersFromURL(ct)).toBe(false);
    }
  });

  it("supplies a CodeMirror language per family", () => {
    expect(languageForContentType("application/json")).toBe("json");
    expect(languageForContentType("application/yaml")).toBe("yaml");
    expect(languageForContentType("application/sql")).toBe("sql");
    expect(languageForContentType("text/x-python")).toBe("python");
    expect(languageForContentType("application/xml")).toBe("xml");
    expect(languageForContentType("text/plain")).toBeUndefined();
  });
});

describe("exceedsInlineLimit", () => {
  it("keeps a large JSON document inline because its viewer virtualizes", () => {
    // The acceptance criterion of issue #1007: a multi-megabyte JSON asset
    // opens in the tree rather than being refused by the old flat 2 MB guard.
    const fiveMB = 5 * 1024 * 1024;
    expect(fiveMB).toBeGreaterThan(TEXT_INLINE_LIMIT);
    expect(exceedsInlineLimit("application/json", fiveMB)).toBe(false);
    expect(exceedsInlineLimit("text/csv", fiveMB)).toBe(false);
  });

  it("still refuses a large block of unstructured text", () => {
    expect(exceedsInlineLimit("text/plain", TEXT_INLINE_LIMIT + 1)).toBe(true);
    expect(exceedsInlineLimit("text/markdown", TEXT_INLINE_LIMIT + 1)).toBe(true);
    expect(exceedsInlineLimit("text/plain", TEXT_INLINE_LIMIT - 1)).toBe(false);
  });

  it("refuses a virtualized document past its own much larger cap", () => {
    expect(exceedsInlineLimit("application/json", VIRTUALIZED_INLINE_LIMIT + 1)).toBe(true);
  });

  it("never refuses a media family, which streams from a URL", () => {
    const huge = 4 * 1024 * 1024 * 1024;
    for (const ct of ["video/mp4", "audio/mpeg", "image/png", "application/pdf"]) {
      expect(exceedsInlineLimit(ct, huge)).toBe(false);
    }
  });
});

describe("familyLabel", () => {
  it.each([
    ["application/json", "JSON"],
    ["text/csv", "CSV"],
    ["application/pdf", "PDF"],
    ["image/png", "Image (PNG)"],
    ["audio/mpeg", "Audio (MPEG)"],
    ["video/mp4", "Video (MP4)"],
    ["application/octet-stream", "Binary"],
    ["", "Unknown"],
  ])("labels %s", (ct, want) => {
    expect(familyLabel(ct)).toBe(want);
  });
});
