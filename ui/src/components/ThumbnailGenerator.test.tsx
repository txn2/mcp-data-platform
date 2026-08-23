import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";

// html2canvas rasterizes through a real canvas, which jsdom does not have, so
// it is stood in for. What was drawn is asserted against the capture containers
// still in the document, which is where #1432 was visible.
const { fetchRaw } = vi.hoisted(() => ({ fetchRaw: vi.fn() }));
vi.mock("html2canvas", () => ({
  default: () => Promise.resolve({ toBlob: (cb: (b: Blob | null) => void) => cb(new Blob()) }),
}));
vi.mock("mermaid", () => ({ default: { initialize: vi.fn(), render: vi.fn() } }));
vi.mock("@/api/portal/client", () => ({ apiFetch: vi.fn(), apiFetchRaw: fetchRaw }));

import { ThumbnailGenerator } from "./ThumbnailGenerator";

beforeEach(() => {
  fetchRaw.mockReset();
  fetchRaw.mockResolvedValue({ ok: true });
});

/** The variant of every upload the capture made, in order. */
function uploadedVariants(): string[] {
  return fetchRaw.mock.calls.map((call) => (String(call[0]).includes("variant=dark") ? "dark" : "light"));
}

const DOC = '{"name":"acme","rows":[1,2]}';

describe("a JSON asset", () => {
  it("is drawn as formatted JSON rather than as a line of markdown prose", async () => {
    const { container } = render(
      <ThumbnailGenerator assetId="ast-1" content={DOC} contentType="application/json" version={3} />,
    );

    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));

    // One capture container per scheme, each holding the re-indented document.
    const blocks = container.querySelectorAll("pre");
    expect(blocks).toHaveLength(2);
    expect(blocks[0]?.textContent).toContain('  "name": "acme"');
    // The markdown path renders prose; nothing on the JSON path should.
    expect(container.querySelector("p")).toBeNull();
    expect(container.querySelector(".jt-key")?.textContent).toBe('"name"');
  });

  it("records the version the capture was rendered from", async () => {
    render(<ThumbnailGenerator assetId="ast-1" content={DOC} contentType="application/json" version={3} />);
    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));
    expect(String(fetchRaw.mock.calls[0]?.[0])).toContain("version=3");
  });

  it("is recognized through a vendor dialect", async () => {
    const { container } = render(
      <ThumbnailGenerator assetId="ast-1" content={DOC} contentType="application/vnd.acme.report+json" />,
    );
    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));
    expect(container.querySelectorAll("pre")).toHaveLength(2);
  });
});

describe("a newline-delimited JSON asset", () => {
  it("is drawn as its records rather than as one JSON document", async () => {
    const { container } = render(
      <ThumbnailGenerator
        assetId="ast-2"
        content={'{"a":1}\n{"a":2}\n'}
        contentType="application/x-ndjson"
      />,
    );

    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));

    // Two schemes, two records each.
    expect(container.querySelectorAll("tbody tr")).toHaveLength(4);
    expect(container.querySelector("pre")).toBeNull();
  });

  it("is separated from JSON by the normalized type, not by a substring", async () => {
    const { container } = render(
      <ThumbnailGenerator assetId="ast-2" content={'{"a":1}\n'} contentType="application/jsonl" />,
    );
    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));
    expect(container.querySelectorAll("tbody tr")).toHaveLength(2);
    expect(container.querySelector("pre")).toBeNull();
  });
});

describe("the families the capturer already drew", () => {
  it("still renders markdown as prose", async () => {
    const { container } = render(
      <ThumbnailGenerator assetId="ast-3" content={"# Title\n\nBody"} contentType="text/markdown" />,
    );
    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));
    expect(container.querySelectorAll("h1")).toHaveLength(2);
    expect(container.querySelector("p")?.textContent).toBe("Body");
  });

  it("still renders CSV as a table of its first rows", async () => {
    const { container } = render(
      <ThumbnailGenerator
        assetId="ast-5"
        content={"region,revenue\nWest,1540000\nEast,1260000\n"}
        contentType="text/csv"
      />,
    );
    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));
    // One table per scheme, each with the header row and both data rows.
    const tables = container.querySelectorAll("table");
    expect(tables).toHaveLength(2);
    expect(Array.from(tables[0]!.querySelectorAll("thead th")).map((c) => c.textContent)).toEqual([
      "region",
      "revenue",
    ]);
    expect(tables[0]!.querySelectorAll("tbody tr")).toHaveLength(2);
  });

  // SVG carries its own colors, so one capture serves both modes.
  it("still renders SVG once, from its own markup", async () => {
    const { container } = render(
      <ThumbnailGenerator
        assetId="ast-6"
        content={'<svg xmlns="http://www.w3.org/2000/svg"><circle r="5" /></svg>'}
        contentType="image/svg+xml"
      />,
    );
    await waitFor(() => expect(uploadedVariants()).toEqual(["light"]));
    expect(container.querySelectorAll("svg")).toHaveLength(1);
  });

  it("leaves a type it cannot draw to its placeholder icon", () => {
    const { container } = render(
      <ThumbnailGenerator assetId="ast-4" content="%PDF-1.7" contentType="application/pdf" />,
    );
    expect(container).toBeEmptyDOMElement();
    expect(fetchRaw).not.toHaveBeenCalled();
  });
});
