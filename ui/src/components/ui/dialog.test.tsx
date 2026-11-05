import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Dialog, DialogContent, DialogTitle } from "./dialog";

// #2468: a click on the backdrop closed a dialog and discarded the form in
// it. The Radix route keeps a dialog open on a pointer outside it; Escape and
// the close button still close it.
function Form({ onOpenChange, onPointerDownOutside }: { onOpenChange: (open: boolean) => void; onPointerDownOutside?: () => void }) {
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent onPointerDownOutside={onPointerDownOutside}>
        <DialogTitle>Add spec</DialogTitle>
        <input aria-label="Name" defaultValue="typed" />
      </DialogContent>
    </Dialog>
  );
}

describe("DialogContent", () => {
  it("stays open on a pointer outside it and keeps what was typed", async () => {
    const onOpenChange = vi.fn();
    const outside = vi.fn();
    render(<Form onOpenChange={onOpenChange} onPointerDownOutside={outside} />);

    // Radix arms its outside-pointer listener a tick after it opens.
    await new Promise((r) => setTimeout(r, 0));
    const overlay = document.querySelector('[data-slot="dialog-overlay"]')!;
    fireEvent.pointerDown(overlay);
    fireEvent.click(overlay);

    expect(onOpenChange).not.toHaveBeenCalled();
    expect(outside).toHaveBeenCalled();
    expect(screen.getByLabelText("Name")).toHaveValue("typed");
  });

  it("still closes on Escape and on its close button", () => {
    const onOpenChange = vi.fn();
    render(<Form onOpenChange={onOpenChange} />);

    fireEvent.keyDown(screen.getByLabelText("Name"), { key: "Escape" });
    expect(onOpenChange).toHaveBeenLastCalledWith(false);

    onOpenChange.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
  });
});
