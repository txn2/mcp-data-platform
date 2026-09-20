import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { Tile, type TileData } from "./Tile";
import { drawFirstPage } from "@/lib/pdfPage";

// The page the platform's renderer draws a tile from (#1787). What is asserted
// here is the page's half of the contract: each family is laid out, and the
// page says it is drawn -- or why it cannot be -- exactly when the renderer
// should take its picture. The picture itself is the renderer's, and is held
// by the Go integration and acceptance suites.

// pdf.js rasterizes onto a real canvas with a real worker, neither of which
// jsdom has. The module boundary is what this file asserts over: the page
// hands it the canvas and the URL, and turns what it rejects with into the
// reason the file has no tile.
vi.mock("@/lib/pdfPage", () => ({
  drawFirstPage: vi.fn(async () => {}),
  pdfFailureReason: vi.fn(() => "the document is password-protected"),
}));

vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg data-testid="diagram"></svg>' })),
  },
}));

const data = (over: Partial<TileData>): TileData => ({
  contentType: "text/markdown",
  name: "notes.md",
  content: "",
  contentURL: "/content",
  serveFromURL: false,
  ...over,
});

function drawn() {
  const reasons: string[] = [];
  return { reasons, onDrawn: (r: string) => reasons.push(r) };
}

afterEach(cleanup);

describe("Tile", () => {
  it("lays out a markdown document and says it is drawn", async () => {
    const d = drawn();
    const { container } = render(
      <Tile data={data({ content: "# Quarterly review\n\nRevenue rose." })} dark={false} onDrawn={d.onDrawn} />,
    );
    await waitFor(() => expect(d.reasons).toEqual([""]));
    expect(container.querySelector("h1")?.textContent).toBe("Quarterly review");
  });

  it("draws a mermaid block as its diagram before saying it is drawn", async () => {
    const d = drawn();
    const { findByTestId } = render(
      <Tile
        data={data({ content: "```mermaid\ngraph TD; A-->B\n```" })}
        dark
        onDrawn={d.onDrawn}
      />,
    );
    await findByTestId("diagram");
    await waitFor(() => expect(d.reasons).toEqual([""]));
  });

  // A themeable family is drawn once per scheme, on the scheme's own
  // background, so a dark card never shows a white tile (#1568).
  it("paints the scheme the page was opened in", async () => {
    const light = render(<Tile data={data({ content: "x" })} dark={false} onDrawn={() => {}} />);
    const lightBg = (light.container.firstChild as HTMLElement).style.background;
    light.unmount();
    const dark = render(<Tile data={data({ content: "x" })} dark onDrawn={() => {}} />);
    const darkBg = (dark.container.firstChild as HTMLElement).style.background;
    expect(lightBg).not.toBe(darkBg);
  });

  it("lays out a CSV as its table, split by the delimiter the viewer uses", async () => {
    const d = drawn();
    const { container } = render(
      <Tile
        data={data({ contentType: "text/tab-separated-values", content: "region\tsales\nwest\t10\n" })}
        dark={false}
        onDrawn={d.onDrawn}
      />,
    );
    await waitFor(() => expect(d.reasons).toEqual([""]));
    expect([...container.querySelectorAll("th")].map((th) => th.textContent)).toEqual(["region", "sales"]);
  });

  it.each([
    ["application/json", '{"a": 1}'],
    ["application/x-ndjson", '{"a": 1}\n{"a": 2}\n'],
    ["text/plain", "plain words"],
    ["image/svg+xml", '<svg xmlns="http://www.w3.org/2000/svg"><rect width="4" height="4"/></svg>'],
  ])("lays out %s and says it is drawn", async (contentType, content) => {
    const d = drawn();
    render(<Tile data={data({ contentType, content })} dark={false} onDrawn={d.onDrawn} />);
    await waitFor(() => expect(d.reasons).toEqual([""]));
  });

  it("draws a raster image from the URL it is served at", async () => {
    const d = drawn();
    const { container } = render(
      <Tile data={data({ contentType: "image/png", serveFromURL: true, content: undefined })} dark={false} onDrawn={d.onDrawn} />,
    );
    const img = container.querySelector("img")!;
    expect(img.getAttribute("src")).toBe("/content");
    fireEvent.load(img);
    await waitFor(() => expect(d.reasons).toEqual([""]));
  });

  it("says why an image that will not decode has no tile", () => {
    const d = drawn();
    const { container } = render(
      <Tile data={data({ contentType: "image/png", serveFromURL: true })} dark={false} onDrawn={d.onDrawn} />,
    );
    fireEvent.error(container.querySelector("img")!);
    expect(d.reasons).toEqual(["the image could not be decoded"]);
  });

  // HTML and JSX carry their own page. The frame reports when it has settled,
  // and a document whose linked files did not load would be a picture of its
  // error branch (#1497), so that is a reason rather than a tile.
  it("draws an HTML document once its frame has settled", async () => {
    const d = drawn();
    const { container } = render(
      <Tile data={data({ contentType: "text/html", content: "<h1>Report</h1>" })} dark={false} onDrawn={d.onDrawn} />,
    );
    const frame = container.querySelector("iframe")!;
    expect(frame.getAttribute("srcdoc")).toContain("<h1>Report</h1>");
    expect(frame.getAttribute("srcdoc")).toContain("thumbnail-ready");

    window.dispatchEvent(
      new MessageEvent("message", { origin: window.location.origin, data: { type: "thumbnail-ready", refFailures: 0 } }),
    );
    await waitFor(() => expect(d.reasons).toEqual([""]));
  });

  it("says a document whose linked files did not load has no tile", () => {
    const d = drawn();
    render(<Tile data={data({ contentType: "text/jsx", content: "export default () => null" })} dark={false} onDrawn={d.onDrawn} />);
    window.dispatchEvent(
      new MessageEvent("message", { origin: window.location.origin, data: { type: "thumbnail-ready", refFailures: 2 } }),
    );
    expect(d.reasons).toEqual(["2 file(s) this document links to could not be loaded"]);
  });

  it("ignores a message from anywhere but its own frame", () => {
    const d = drawn();
    render(<Tile data={data({ contentType: "text/html", content: "<p>x</p>" })} dark={false} onDrawn={d.onDrawn} />);
    window.dispatchEvent(
      new MessageEvent("message", { origin: "https://elsewhere.example.com", data: { type: "thumbnail-ready" } }),
    );
    expect(d.reasons).toEqual([]);
  });

  it("says nothing draws a type outside every family", () => {
    const d = drawn();
    render(<Tile data={data({ contentType: "application/zip" })} dark={false} onDrawn={d.onDrawn} />);
    expect(d.reasons).toEqual(["nothing draws application/zip"]);
  });

  // A PDF is drawn onto a canvas by pdf.js, which needs a real 2d context and
  // a real worker: what the page owes is the canvas, the URL handed to the
  // drawing code, and a reason when it cannot draw. The picture itself is held
  // by the Go integration suite, in the renderer that takes it.
  it("gives a PDF a canvas and draws it from the content URL", async () => {
    const d = drawn();
    const { container } = render(
      <Tile data={data({ contentType: "application/pdf", contentURL: "/content", serveFromURL: true })} dark={false} onDrawn={d.onDrawn} />,
    );
    const canvas = container.querySelector("canvas");
    expect(canvas).not.toBeNull();
    await waitFor(() => expect(drawFirstPage).toHaveBeenCalledWith(canvas, "/content"));
    await waitFor(() => expect(d.reasons).toEqual([""]));
  });

  it("records why a PDF could not be drawn instead of taking its picture", async () => {
    vi.mocked(drawFirstPage).mockRejectedValueOnce(new Error("boom"));
    const d = drawn();
    render(<Tile data={data({ contentType: "application/pdf", serveFromURL: true })} dark={false} onDrawn={d.onDrawn} />);
    await waitFor(() => expect(d.reasons).toEqual(["the document is password-protected"]));
  });

  // A PDF is drawn once. The tile page is opened in both schemes for a
  // themeable family only, but nothing here should read `dark` and produce a
  // different picture for it.
  it("draws the same PDF whatever scheme the page was opened in", async () => {
    const d = drawn();
    render(<Tile data={data({ contentType: "application/pdf", serveFromURL: true })} dark onDrawn={d.onDrawn} />);
    await waitFor(() => expect(d.reasons).toEqual([""]));
  });
});
