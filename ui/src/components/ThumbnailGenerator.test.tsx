import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor } from "@testing-library/react";

// html2canvas rasterizes through a real canvas, which jsdom does not have, so
// it is stood in for. What was drawn is asserted against the capture containers
// still in the document, which is where #1432 was visible.
// The uploader builds the whole URL and calls fetch itself, because an asset
// and a resource live under different API roots (#1554). Watching a client mock
// here would watch a client it no longer uses.
const { fetchRaw } = vi.hoisted(() => ({ fetchRaw: vi.fn() }));
const fetchMock = vi.fn();
vi.mock("html2canvas", () => ({
  default: () => Promise.resolve({ toBlob: (cb: (b: Blob | null) => void) => cb(new Blob()) }),
}));
vi.mock("mermaid", () => ({ default: { initialize: vi.fn(), render: vi.fn() } }));
vi.mock("@/api/portal/client", () => ({ apiFetch: vi.fn(), apiFetchRaw: fetchRaw }));

import { ThumbnailGenerator } from "./ThumbnailGenerator";

beforeEach(() => {
  fetchRaw.mockReset();
  fetchRaw.mockResolvedValue({ ok: true });
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({ ok: true, status: 200 });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

/** The variant of every upload the capture made, in order. */
function uploadedVariants(): string[] {
  return uploads().map((url) => (url.includes("variant=dark") ? "dark" : "light"));
}

/** The URLs the capture PUT to, which is what says where an upload went. */
function uploads(): string[] {
  return fetchMock.mock.calls
    .filter((call) => (call[1] as RequestInit | undefined)?.method === "PUT")
    .map((call) => String(call[0]));
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
    // The full URL, so an upload sent to the wrong API root fails here rather
    // than passing on a fragment a client mock would have prefixed (#1554).
    expect(uploads()[0]).toBe("/api/v1/portal/assets/ast-1/thumbnail?version=3");
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

  // Plain text is one of the commonest things anyone uploads and had no
  // thumbnail at all, for either kind, until #1568 -- and a .md stored as
  // text/plain, which is what every generic declaration used to produce, lands
  // here too.
  it("draws a plain-text file as text, in both schemes", async () => {
    const { container } = render(
      <ThumbnailGenerator
        assetId="ast-7"
        content={"# Release notes\n\nNot markdown: the type says so.\n"}
        contentType="text/plain; charset=utf-8"
      />,
    );
    await waitFor(() => expect(uploadedVariants()).toEqual(["light", "dark"]));
    const blocks = container.querySelectorAll("pre");
    expect(blocks).toHaveLength(2);
    // The markup is left alone rather than interpreted: the type says the
    // markdown is not meant to be read as markdown.
    expect(blocks[0]?.textContent).toContain("# Release notes");
    expect(container.querySelector("h1")).toBeNull();
  });

  it("leaves a type it cannot draw to its placeholder icon", () => {
    const { container } = render(
      <ThumbnailGenerator assetId="ast-4" content="%PDF-1.7" contentType="application/pdf" />,
    );
    expect(container).toBeEmptyDOMElement();
    expect(uploads()).toEqual([]);
  });
});

// A capture that rendered an error instead of the artifact produces a perfectly
// valid PNG, so nothing about the image says it is wrong: an asset naming a
// managed resource or another asset was stored showing "Failed to fetch" on
// every tile (#1497). The frame reports how many reference loads failed, and
// that report is the only thing that can stop the upload.
describe("an artifact that lost its references", () => {
  /**
   * The capture reads the frame's document, which a jsdom iframe does not have
   * until something is written into it: nothing loads the blob: URL here.
   */
  function fillFrame(container: HTMLElement) {
    container.querySelector("iframe")?.contentDocument?.write("<html><body>artifact</body></html>");
  }

  /** The frame telling the parent it is ready, as the capture script sends it. */
  function ready(refFailures?: number) {
    window.dispatchEvent(
      new MessageEvent("message", {
        data: refFailures === undefined
          ? { type: "thumbnail-ready" }
          : { type: "thumbnail-ready", refFailures },
        origin: window.location.origin,
      }),
    );
  }

  const CODE = `export default function App() { return <div>Hi</div>; }`;

  it("is not stored, so the asset stays pending", async () => {
    const onFailed = vi.fn();
    const { container } = render(
      <ThumbnailGenerator
        assetId="ast-11"
        content={CODE}
        contentType="text/jsx"
        version={3}
        onFailed={onFailed}
      />,
    );
    fillFrame(container);

    ready(2);

    await waitFor(() => expect(onFailed).toHaveBeenCalled());
    expect(uploads()).toEqual([]);
  });

  it("is stored when every reference loaded", async () => {
    const onCaptured = vi.fn();
    const { container } = render(
      <ThumbnailGenerator
        assetId="ast-11"
        content={CODE}
        contentType="text/jsx"
        version={3}
        onCaptured={onCaptured}
      />,
    );
    fillFrame(container);

    ready(0);

    await waitFor(() => expect(onCaptured).toHaveBeenCalled());
    expect(uploads()[0]).toBe("/api/v1/portal/assets/ast-11/thumbnail?version=3");
  });

  // A frame from before this message carried no count. Reading its absence as a
  // failure would stop every HTML asset being captured at all.
  it("is stored when the frame reported no count", async () => {
    const onCaptured = vi.fn();
    const { container } = render(
      <ThumbnailGenerator
        assetId="ast-12"
        content="<html><body>hi</body></html>"
        contentType="text/html"
        onCaptured={onCaptured}
      />,
    );
    fillFrame(container);

    ready();

    await waitFor(() => expect(onCaptured).toHaveBeenCalled());
    expect(uploads()).not.toEqual([]);
  });
});

// A type none of the three capture paths handles.
//
// The queue holds one item at a time and moves on when the capture reports
// back, so a generator that quietly rendered nothing left it holding that item
// forever: one undispatchable entry and no thumbnail after it was ever captured
// in that tab, for either kind (#1554). Reporting the failure is what keeps the
// queue moving.
describe("a content type nothing can render", () => {
  it("reports a failure rather than rendering nothing", async () => {
    const onCaptured = vi.fn();
    const onFailed = vi.fn();
    vi.spyOn(console, "warn").mockImplementation(() => {});

    render(
      <ThumbnailGenerator
        assetId="ast-x"
        content="%PDF-1.7"
        contentType="application/pdf"
        onCaptured={onCaptured}
        onFailed={onFailed}
      />,
    );

    await waitFor(() => expect(onFailed).toHaveBeenCalledTimes(1));
    expect(onCaptured).not.toHaveBeenCalled();
    // And nothing was uploaded for it.
    expect(uploads()).toEqual([]);
  });
});
