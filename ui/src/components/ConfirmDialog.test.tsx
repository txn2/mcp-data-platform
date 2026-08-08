import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ConfirmDialog } from "./ConfirmDialog";

function renderDialog(props: Partial<React.ComponentProps<typeof ConfirmDialog>> = {}) {
  const onConfirm = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <ConfirmDialog
      open
      onOpenChange={onOpenChange}
      title="Delete the connection"
      onConfirm={onConfirm}
      {...props}
    />,
  );
  return { onConfirm, onOpenChange };
}

describe("ConfirmDialog", () => {
  it("offers no dismissal that bypasses the in-flight guard", () => {
    // Escape and an outside click are refused while a confirm is running, and
    // Cancel is disabled. A corner Close would be a fourth way out that none of
    // those guards cover, so the dialog asks for none.
    renderDialog({ loading: true });

    expect(screen.queryByRole("button", { name: /^close$/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /cancel/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /working/i })).toBeDisabled();
  });

  it("runs the action and re-enables the dialog once it settles", async () => {
    const { onConfirm } = renderDialog();

    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));

    expect(onConfirm).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /confirm/i })).toBeEnabled(),
    );
  });

  it("keeps the dialog open and surfaces a rejection where the operator can read it", async () => {
    // A parent banner would be behind the overlay, so the message has to land
    // inside the dialog.
    const onConfirm = vi.fn().mockRejectedValue(new Error("nope"));
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    const { rerender } = render(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title="Delete the connection"
        onConfirm={onConfirm}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /confirm/i })).toBeEnabled(),
    );

    rerender(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title="Delete the connection"
        onConfirm={onConfirm}
        error="The connection is still in use."
      />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(/still in use/i);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    consoleError.mockRestore();
  });

  it("renders inside the scrolling overlay so a tall dialog keeps its top edge", () => {
    // The stock shadcn content centers itself with a translate, which puts the
    // title of an overlong dialog above the top of the viewport with no way to
    // scroll back to it. Content nested in the scrolling overlay is what
    // prevents that, and it is invisible to every other assertion here.
    renderDialog({
      description: "This cannot be undone.",
    });

    const overlay = document.querySelector("[data-slot='dialog-overlay']");
    expect(overlay).not.toBeNull();
    expect(overlay).toHaveClass("overflow-y-auto");
    expect(overlay!.contains(screen.getByRole("dialog"))).toBe(true);
  });
});
