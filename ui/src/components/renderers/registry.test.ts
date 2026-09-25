import { describe, it, expect } from "vitest";
import {
  resolveRenderer,
  isEditableContent,
  rendersFromURL,
  exceedsInlineLimit,
  contentLoad,
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
    ["application/vnd.apache.parquet", "parquet"],
    ["application/x-parquet", "parquet"],
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
    ["application/vnd.apache.parquet", "Parquet"],
    ["application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "Excel workbook"],
    ["image/png", "Image (PNG)"],
    ["audio/mpeg", "Audio (MPEG)"],
    ["video/mp4", "Video (MP4)"],
    ["application/octet-stream", "Binary"],
    ["", "Unknown"],
  ])("labels %s", (ct, want) => {
    expect(familyLabel(ct)).toBe(want);
  });
});

describe("an Excel workbook", () => {
  it("opens on the download card, read from the content URL and never edited (#1849)", () => {
    for (const input of [
      { contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" },
      { contentType: "application/octet-stream", fileName: "sales.xlsx" },
    ]) {
      const entry = resolveRenderer(input);
      expect(entry.kind).toBe("binary");
      expect(entry.contentType).toBe("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet");
      expect(entry.source).toBe("url");
      expect(entry.editable).toBe(false);
    }
  });
});

describe("a Parquet file", () => {
  it("is read from the content URL, not embedded, and never edited", () => {
    const entry = resolveRenderer({ contentType: "application/octet-stream", fileName: "orders.parquet" });
    expect(entry.kind).toBe("parquet");
    expect(entry.source).toBe("url");
    expect(entry.editable).toBe(false);
    expect(entry.inlineLimit).toBeNull();
  });
});

describe("contentLoad", () => {
  const fiveMB = 5 * 1024 * 1024;

  it("fetches a table past the old flat 2 MB, within its family's limit (#1874)", () => {
    expect(contentLoad("text/csv", fiveMB, "data.csv")).toBe("fetch");
    expect(contentLoad("text/tab-separated-values", fiveMB)).toBe("fetch");
    expect(contentLoad("application/x-ndjson", fiveMB)).toBe("fetch");
    expect(contentLoad("application/json", fiveMB)).toBe("fetch");
  });

  it("refuses an inline family past its own limit without fetching it", () => {
    expect(contentLoad("text/csv", VIRTUALIZED_INLINE_LIMIT + 1)).toBe("too-large");
    expect(contentLoad("text/markdown", TEXT_INLINE_LIMIT + 1)).toBe("too-large");
    expect(contentLoad("text/markdown", TEXT_INLINE_LIMIT)).toBe("fetch");
  });

  it("hands a family whose renderer loads the endpoint the endpoint, at any size", () => {
    expect(contentLoad("image/png", 1024)).toBe("url");
    expect(contentLoad("application/pdf", 500 * 1024 * 1024)).toBe("url");
    expect(contentLoad("application/vnd.apache.parquet", 1024)).toBe("url");
  });

  it("reads a small file under a generic type, so detection can place it", () => {
    expect(contentLoad("application/octet-stream", 1024)).toBe("fetch");
    expect(contentLoad("application/octet-stream", TEXT_INLINE_LIMIT + 1)).toBe("url");
  });
});
