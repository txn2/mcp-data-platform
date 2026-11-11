import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { openRowProps } from "./openRow";

function Row({ open }: { open: () => void }) {
  return (
    <table>
      <tbody>
        <tr data-testid="row" {...openRowProps(open)}>
          <td>name</td>
          <td>
            <a href="https://site.example.test" target="_blank" rel="noreferrer">
              site.example.test
            </a>
          </td>
          <td>
            <button type="button">act</button>
          </td>
        </tr>
      </tbody>
    </table>
  );
}

describe("openRowProps", () => {
  it("opens from a click anywhere on the row", () => {
    const open = vi.fn();
    render(<Row open={open} />);
    fireEvent.click(screen.getByText("name"));
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("leaves a link or a button inside the row to do its own thing", () => {
    const open = vi.fn();
    render(<Row open={open} />);
    fireEvent.click(screen.getByText("site.example.test"));
    fireEvent.click(screen.getByText("act"));
    expect(open).not.toHaveBeenCalled();
  });

  it("opens from Enter on the focused row, and only on the row itself", () => {
    const open = vi.fn();
    render(<Row open={open} />);
    const row = screen.getByTestId("row");
    expect(row).toHaveAttribute("tabindex", "0");
    fireEvent.keyDown(screen.getByText("site.example.test"), { key: "Enter" });
    expect(open).not.toHaveBeenCalled();
    fireEvent.keyDown(row, { key: " " });
    expect(open).not.toHaveBeenCalled();
    fireEvent.keyDown(row, { key: "Enter" });
    expect(open).toHaveBeenCalledTimes(1);
  });
});
