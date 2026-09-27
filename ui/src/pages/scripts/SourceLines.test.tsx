import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SourceLines } from "./SourceLines";

afterEach(cleanup);

const source = "a = 1\n\nplatform.query(\"SELECT 1\")\nprint(a)";

describe("SourceLines", () => {
  it("numbers the lines and marks the ones asked for", () => {
    render(<SourceLines source={source} markedLines={[3]} />);
    const rows = screen.getByTestId("script-source-lines").querySelectorAll("[data-line]");
    expect(rows).toHaveLength(4);
    expect(rows[2]).toHaveAttribute("data-marked", "true");
    expect(rows[0]).not.toHaveAttribute("data-marked");
    expect(rows[2]).toHaveTextContent("3platform.query");
  });

  it("says a version with no source has none", () => {
    render(<SourceLines source="" />);
    expect(screen.getByText("(this version has no source)")).toBeInTheDocument();
  });

  it("reports the lines a reader selects, and nothing for a bare click", () => {
    const onSelect = vi.fn();
    render(<SourceLines source={source} onSelectLines={onSelect} />);
    const host = screen.getByTestId("script-source-lines");
    const rows = host.querySelectorAll("[data-line] span:last-child");
    const sel = window.getSelection()!;
    sel.removeAllRanges();
    sel.setBaseAndExtent(rows[3]!.firstChild!, 1, rows[1]!, 0);
    fireEvent.mouseUp(host);
    expect(onSelect).toHaveBeenLastCalledWith({ from: 2, to: 4 });

    sel.removeAllRanges();
    fireEvent.keyUp(host);
    expect(onSelect).toHaveBeenLastCalledWith(null);
  });
});
