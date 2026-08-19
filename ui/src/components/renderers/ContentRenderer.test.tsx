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

  it("renders a PDF through an object element pointed at the content URL", async () => {
    // Deliberately not a sandboxed iframe: Chrome refuses to instantiate its
    // PDF plugin inside any sandboxed frame, which renders a broken-plugin
    // icon instead of the document. Containment is the serving side's job.
    render(<ContentRenderer contentType="application/pdf" contentUrl={CONTENT_URL} fileName="report.pdf" />);

    const embed = await screen.findByLabelText("report.pdf");
    expect(embed.tagName).toBe("OBJECT");
    expect(embed).toHaveAttribute("data", CONTENT_URL);
    expect(embed).toHaveAttribute("type", "application/pdf");
    expect(embed).not.toHaveAttribute("sandbox");
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
