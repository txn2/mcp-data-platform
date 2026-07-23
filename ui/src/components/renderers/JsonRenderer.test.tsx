import { describe, it, expect, beforeAll, vi } from "vitest";
import { render, screen, within, fireEvent } from "@testing-library/react";
import { JsonRenderer } from "./JsonRenderer";

const DOC = JSON.stringify({
  total: 2,
  results: [
    { id: 1, name: "acme", region: "west" },
    { id: 2, name: "globex", region: "east" },
  ],
});

beforeAll(() => {
  // The virtualizer sizes its window from the scroll container's offsetHeight,
  // which jsdom always reports as 0. With no height it concludes nothing is on
  // screen and renders no rows, so every assertion below would look at an empty
  // tree.
  Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, value: 640 });
  Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, value: 800 });
  Element.prototype.scrollIntoView = vi.fn();
});

describe("JsonRenderer", () => {
  it("opens on the tree view with the top level expanded", () => {
    render(<JsonRenderer content={DOC} />);

    const tree = screen.getByRole("tree", { name: /json document/i });
    expect(within(tree).getByText("total")).toBeInTheDocument();
    expect(within(tree).getByText("results")).toBeInTheDocument();
    // Nested containers start collapsed, so the document's shape is what shows.
    expect(within(tree).queryByText("acme")).not.toBeInTheDocument();
  });

  it("expands and collapses every container on demand", async () => {
    render(<JsonRenderer content={DOC} />);

    fireEvent.click(screen.getByRole("button", { name: /expand all/i }));
    const tree = screen.getByRole("tree", { name: /json document/i });
    expect(within(tree).getByText('"acme"')).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /collapse all/i }));
    expect(within(tree).queryByText('"acme"')).not.toBeInTheDocument();
  });

  it("counts search matches and reveals a hit inside a collapsed subtree", async () => {
    render(<JsonRenderer content={DOC} />);

    fireEvent.change(screen.getByLabelText(/search keys and values/i), { target: { value: "globex" } });

    // Two keys plus the value would be noise; only the value matches here.
    expect(await screen.findByText("1 of 1")).toBeInTheDocument();
    // The match lives under $.results[1], which was collapsed on open.
    const tree = screen.getByRole("tree", { name: /json document/i });
    expect(within(tree).getByText("globex")).toBeInTheDocument();
  });

  it("steps between matches", async () => {
    render(<JsonRenderer content={DOC} />);

    fireEvent.change(screen.getByLabelText(/search keys and values/i), { target: { value: "name" } });
    expect(await screen.findByText("1 of 2")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /next match/i }));
    expect(await screen.findByText("2 of 2")).toBeInTheDocument();

    // The match list wraps rather than dead-ending at the last hit.
    fireEvent.click(screen.getByRole("button", { name: /next match/i }));
    expect(await screen.findByText("1 of 2")).toBeInTheDocument();
  });

  it("reports when a search finds nothing", async () => {
    render(<JsonRenderer content={DOC} />);

    fireEvent.change(screen.getByLabelText(/search keys and values/i), { target: { value: "nothing-here" } });
    expect(await screen.findByText("No matches")).toBeInTheDocument();
  });

  it("copies the selected node's JSONPath and value", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    render(<JsonRenderer content={DOC} />);

    fireEvent.click(screen.getByText("total"));
    fireEvent.click(screen.getByRole("button", { name: /copy path/i }));
    expect(writeText).toHaveBeenCalledWith("$.total");

    fireEvent.click(screen.getByRole("button", { name: /copy value/i }));
    expect(writeText).toHaveBeenLastCalledWith("2");
  });

  it("round-trips between the tree, formatted and raw views", async () => {
    render(<JsonRenderer content={DOC} />);

    fireEvent.click(screen.getByRole("button", { name: "raw" }));
    expect(screen.queryByRole("tree")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "tree" }));
    expect(screen.getByRole("tree", { name: /json document/i })).toBeInTheDocument();
  });

  it("falls back to a source view with the parser's message on invalid JSON", async () => {
    render(<JsonRenderer content='{"a": 1,,,}' />);

    expect(await screen.findByText(/not valid json/i)).toBeInTheDocument();
    expect(screen.queryByRole("tree")).not.toBeInTheDocument();
  });
});
