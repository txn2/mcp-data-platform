import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DrawerShell } from "./DrawerShell";

describe("DrawerShell", () => {
  it("closes on Escape and on a backdrop click", () => {
    const onClose = vi.fn();
    render(
      <DrawerShell title="Event Detail" onClose={onClose}>
        <p>detail</p>
      </DrawerShell>,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);

    // The backdrop is the dimmed sibling of the panel, not the panel itself.
    const dialog = screen.getByRole("dialog", { name: "Event Detail" });
    fireEvent.click(dialog.previousElementSibling!);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("leaves Escape to a confirmation opened inside it", async () => {
    // ChangesetDrawer opens a Radix ConfirmDialog over the drawer. One Escape
    // must dismiss the confirmation only -- closing the drawer as well would
    // discard the changeset the reader was working through.
    const onClose = vi.fn();
    const onCancelled = vi.fn();
    render(
      <DrawerShell title="Changeset Detail" onClose={onClose}>
        <ConfirmDialog
          open
          onOpenChange={(next) => {
            if (!next) onCancelled();
          }}
          title="Roll this changeset back?"
          onConfirm={vi.fn()}
        />
      </DrawerShell>,
    );

    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: /Roll this changeset back/ })).toBeInTheDocument(),
    );

    fireEvent.keyDown(document, { key: "Escape" });

    await waitFor(() => expect(onCancelled).toHaveBeenCalledTimes(1));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("refuses both dismissals while its action is in flight", () => {
    const onClose = vi.fn();
    render(
      <DrawerShell title="Insight Detail" onClose={onClose} busy>
        <p>saving</p>
      </DrawerShell>,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    const dialog = screen.getByRole("dialog", { name: "Insight Detail" });
    fireEvent.click(dialog.previousElementSibling!);
    expect(onClose).not.toHaveBeenCalled();
  });
});
