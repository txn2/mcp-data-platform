import { describe, it, expect } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { ContentRenderer } from "./ContentRenderer";

const CONTENT_URL = "/api/v1/portal/assets/a1/content";

describe("ContentRenderer routing", () => {
  it("renders a JSON asset as a searchable tree, not raw text", () => {
    // The motivating case of issue #1007: a stored application/json asset used
    // to land in a plain <pre> block.
    render(
      <ContentRenderer
        contentType="application/json"
        content='{"total":1,"results":[{"id":1,"name":"acme"}]}'
        fileName="export.json"
      />,
    );

    return waitFor(() => {
      expect(screen.getByRole("tree", { name: /json document/i })).toBeInTheDocument();
      expect(screen.getByLabelText(/search keys and values/i)).toBeInTheDocument();
    });
  });

  it("reclassifies a mislabeled asset from its content", () => {
    // An asset stored before the server settled types still carries
    // application/octet-stream; the client rules give it the JSON viewer anyway.
    render(
      <ContentRenderer
        contentType="application/octet-stream"
        content='{"results":[{"id":1}]}'
        contentUrl={CONTENT_URL}
      />,
    );

    return waitFor(() => {
      expect(screen.getByRole("tree", { name: /json document/i })).toBeInTheDocument();
    });
  });

  it("renders an image from the content URL", async () => {
    render(
      <ContentRenderer
        contentType="image/png"
        contentUrl={CONTENT_URL}
        fileName="chart.png"
        sizeBytes={4096}
      />,
    );

    const img = await screen.findByAltText("chart.png");
    expect(img).toHaveAttribute("src", CONTENT_URL);
    expect(await screen.findByLabelText("Zoom in")).toBeInTheDocument();
  });

  it("renders audio and video players pointed at the content URL", async () => {
    const { unmount } = render(
      <ContentRenderer contentType="audio/mpeg" contentUrl={CONTENT_URL} fileName="clip.mp3" />,
    );
    await waitFor(() => {
      expect(document.querySelector("audio")).toHaveAttribute("src", CONTENT_URL);
    });
    unmount();

    render(<ContentRenderer contentType="video/mp4" contentUrl={CONTENT_URL} fileName="clip.mp4" />);
    await waitFor(() => {
      expect(document.querySelector("video")).toHaveAttribute("src", CONTENT_URL);
    });
  });

  it("renders a PDF through our own viewer, not the browser's plugin", async () => {
    // The plugin honoured the document's /OpenAction, so a file exported with
    // "print on open" raised the print dialog at a reader who had asked only
    // to look at it (#1783). Sandboxing was no answer -- Chrome refuses to
    // instantiate the plugin inside any sandboxed frame. The viewer is PDF.js
    // now, which executes no document-level action at all.
    render(<ContentRenderer contentType="application/pdf" contentUrl={CONTENT_URL} fileName="report.pdf" />);

    const frame = await screen.findByLabelText("report.pdf");
    expect(frame.tagName).not.toBe("OBJECT");
    expect(document.querySelector('object[type="application/pdf"]')).toBeNull();
    // Download stays reachable whatever the viewer does with the document.
    expect(screen.getAllByRole("link", { name: /Download/ })[0]).toHaveAttribute(
      "href",
      CONTENT_URL,
    );
  });

  it("shows a metadata card for an unrecognized binary type, never raw bytes", () => {
    render(
      <ContentRenderer
        contentType="application/zip"
        contentUrl={CONTENT_URL}
        fileName="bundle.zip"
        sizeBytes={2048}
      />,
    );

    expect(screen.getByText(/no preview for this file type/i)).toBeInTheDocument();
    expect(screen.getByText("bundle.zip")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /download/i })).toHaveAttribute("href", CONTENT_URL);
  });

  it("falls back to the metadata card when a URL family has no URL", () => {
    render(<ContentRenderer contentType="image/png" fileName="chart.png" sizeBytes={10} />);
    expect(screen.getByText(/no preview for this file type/i)).toBeInTheDocument();
  });

  // Every family's viewer is loaded on demand (#1355), so the table arrives a
  // tick after the render rather than during it.
  it("renders a CSV asset as a table", async () => {
    render(<ContentRenderer contentType="text/csv" content={"id,name\n1,acme\n"} fileName="rows.csv" />);
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(screen.getByText("acme")).toBeInTheDocument();
  });

  it("renders a TSV asset with the same table viewer", async () => {
    render(
      <ContentRenderer
        contentType="text/tab-separated-values"
        content={"id\tname\n1\tacme\n"}
        fileName="rows.tsv"
      />,
    );
    expect(await screen.findByRole("table")).toBeInTheDocument();
    expect(screen.getByText("acme")).toBeInTheDocument();
  });

  it("renders content declared text/plain that sniffs as HTML as text, not markup", () => {
    // The client half of the active-type rule. Rendering this as HTML would
    // execute author-controlled markup on the platform's own origin.
    const html = "<!DOCTYPE html>\n<b id=\"payload\">not markup</b>";
    const { container } = render(<ContentRenderer contentType="text/plain" content={html} />);

    expect(container.querySelector("#payload")).toBeNull();
    expect(container.querySelector("pre")?.textContent).toContain("<b id=");
  });
});
