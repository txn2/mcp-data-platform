import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { CollapsibleMarkdown } from "./CollapsibleMarkdown";

// MarkdownRenderer pulls in mermaid, which is irrelevant to the collapse
// behavior under test and noisy in jsdom. Stub it to a plain passthrough.
vi.mock("./MarkdownRenderer", () => ({
  MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div>,
}));

// jsdom reports scrollHeight as 0, so overflow can never be detected
// without a stub. Force a value to drive the two branches.
function setScrollHeight(px: number): () => void {
  const desc = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "scrollHeight",
  );
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
    configurable: true,
    get: () => px,
  });
  return () => {
    if (desc) {
      Object.defineProperty(HTMLElement.prototype, "scrollHeight", desc);
    }
  };
}

afterEach(cleanup);

describe("CollapsibleMarkdown", () => {
  it("renders short content with no toggle", () => {
    const restore = setScrollHeight(20);
    render(<CollapsibleMarkdown content="short note" />);

    expect(screen.getByText("short note")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    restore();
  });

  it("clamps long content and toggles reveal", () => {
    const restore = setScrollHeight(900);
    render(<CollapsibleMarkdown content="a very long memory record" />);

    const toggle = screen.getByRole("button", { name: "Show more" });
    expect(toggle).toBeInTheDocument();

    fireEvent.click(toggle);
    expect(
      screen.getByRole("button", { name: "Show less" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Show less" }));
    expect(
      screen.getByRole("button", { name: "Show more" }),
    ).toBeInTheDocument();
    restore();
  });

  it("uses a taller clamp when maxHeightPx is raised", () => {
    // 150px overflows the default ~84px clamp but fits within a 200px clamp,
    // so the toggle must not appear when the caller raises maxHeightPx.
    const restore = setScrollHeight(150);
    render(<CollapsibleMarkdown content="medium description" maxHeightPx={200} />);

    expect(screen.getByText("medium description")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    restore();
  });

  it("still clamps content taller than the raised maxHeightPx", () => {
    const restore = setScrollHeight(400);
    render(<CollapsibleMarkdown content="long description" maxHeightPx={200} />);

    expect(
      screen.getByRole("button", { name: "Show more" }),
    ).toBeInTheDocument();
    restore();
  });
});
