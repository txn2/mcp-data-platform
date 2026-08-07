import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { MarkdownEditor } from "./MarkdownEditor";

afterEach(cleanup);

// The editing surface is CodeMirror's own element, not a React node, so the
// accessible name reaches it through the contentAttributes facet rather than a
// JSX attribute. These tests pin that wiring: the vocabulary description
// editors (#1200) name their editor this way, and every call site predating the
// prop leaves the editor unnamed.
describe("MarkdownEditor", () => {
  it("names the editing surface with the given label", () => {
    render(<MarkdownEditor value="## Included" onChange={() => {}} label="Term definition" />);

    const surface = screen.getByLabelText("Term definition");
    expect(surface).toHaveClass("cm-content");
  });

  it("leaves the editing surface unnamed when no label is given", () => {
    const { container } = render(<MarkdownEditor value="## Included" onChange={() => {}} />);

    const surface = container.querySelector(".cm-content");
    expect(surface).not.toBeNull();
    expect(surface).not.toHaveAttribute("aria-label");
  });
});
