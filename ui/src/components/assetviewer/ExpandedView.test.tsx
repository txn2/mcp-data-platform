import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ExpandButton, ExpandedView } from "./ExpandedView";

describe("ExpandedView (#2390)", () => {
  it("leaves the layout alone when not expanded", () => {
    render(
      <ExpandedView expanded={false} title="Q3 report" onClose={vi.fn()}>
        <iframe title="doc" />
      </ExpandedView>,
    );
    expect(screen.getByTestId("expanded-view")).toHaveClass("contents");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("fills the viewport inside its safe area, names the content and closes", () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <ExpandedView expanded={false} title="Q3 report" onClose={onClose}>
        <iframe title="doc" />
      </ExpandedView>,
    );
    const frame = screen.getByTitle("doc");
    rerender(
      <ExpandedView expanded title="Q3 report" onClose={onClose}>
        <iframe title="doc" />
      </ExpandedView>,
    );
    expect(screen.getByTitle("doc")).toBe(frame);
    const dialog = screen.getByRole("dialog", { name: "Q3 report" });
    expect(dialog.className).toContain("fixed inset-0");
    expect(dialog.className).toContain("env(safe-area-inset-bottom)");
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("offers Expand", () => {
    const onClick = vi.fn();
    render(<ExpandButton onClick={onClick} />);
    fireEvent.click(screen.getByRole("button", { name: /Expand/ }));
    expect(onClick).toHaveBeenCalled();
  });
});
