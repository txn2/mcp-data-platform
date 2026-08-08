import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ModalScroll, ModalShell } from "./ModalShell";

// The geometry these shapes produce is measured end-to-end in
// e2e/interactive/resource-detail-modal.spec.ts, where a real layout engine can
// report a height. What is worth pinning here is the dismissal contract, which
// is pure event handling and identical for both shapes, and which regions of
// the capped shape sit outside the scrolling body.

describe("ModalShell", () => {
  it("closes on Escape, so a pointer is not the only way out", () => {
    const onClose = vi.fn();
    render(
      <ModalShell onClose={onClose} label="Details">
        <p>body</p>
      </ModalShell>,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps the header and footer out of the scrolling body", () => {
    render(
      <ModalShell
        onClose={vi.fn()}
        label="Details"
        header={<h2>Quarterly chart</h2>}
        footer={<button type="button">Download</button>}
      >
        <p>body</p>
      </ModalShell>,
    );

    // Only the body scrolls, so the two fixed regions must be siblings of it
    // rather than children -- a header inside the body scrolls away with the
    // content it heads.
    const body = screen.getByText("body").parentElement!;
    const title = screen.getByRole("heading", { name: "Quarterly chart" });
    const action = screen.getByRole("button", { name: "Download" });

    expect(screen.getByTestId("modal-panel").contains(title)).toBe(true);
    expect(body.contains(title)).toBe(false);
    expect(body.contains(action)).toBe(false);
  });

  it("leaves Escape to a layer opened over it", async () => {
    // A Radix layer above handles Escape on the document in the capture phase
    // and marks the event handled, so closing the select must not also close
    // the modal it was opened inside.
    const onClose = vi.fn();
    render(
      <ModalShell onClose={onClose} label="Edit Resource">
        <Select defaultValue="samples">
          <SelectTrigger aria-label="Category">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="samples">samples</SelectItem>
            <SelectItem value="guides">guides</SelectItem>
          </SelectContent>
        </Select>
      </ModalShell>,
    );

    fireEvent.click(screen.getByRole("combobox", { name: "Category" }));
    await waitFor(() => expect(screen.getByRole("listbox")).toBeInTheDocument());

    fireEvent.keyDown(document, { key: "Escape" });

    await waitFor(() => expect(screen.queryByRole("listbox")).not.toBeInTheDocument());
    expect(onClose).not.toHaveBeenCalled();

    // With the select closed, the next Escape reaches the modal.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes on a backdrop click but not on a click inside the panel", () => {
    const onClose = vi.fn();
    render(
      <ModalShell onClose={onClose} label="Details">
        <button type="button">Save</button>
      </ModalShell>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("modal-overlay"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("refuses both dismissals while busy, so a mutation's outcome survives", () => {
    // Closing mid-mutation unmounts the only component that renders the
    // result, so a partial failure would read as success.
    const onClose = vi.fn();
    render(
      <ModalShell onClose={onClose} label="Upload Resource" busy>
        <p>uploading</p>
      </ModalShell>,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.click(screen.getByTestId("modal-overlay"));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("ignores the Escape that cancels an IME composition", () => {
    const onClose = vi.fn();
    render(
      <ModalShell onClose={onClose} label="Edit Resource">
        <input aria-label="Description" />
      </ModalShell>,
    );

    fireEvent.keyDown(screen.getByLabelText("Description"), {
      key: "Escape",
      isComposing: true,
    });
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.keyDown(screen.getByLabelText("Description"), { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("ModalScroll", () => {
  it("shares the whole dismissal contract with the capped shape", () => {
    const onClose = vi.fn();
    const { unmount } = render(
      <ModalScroll onClose={onClose} label="Delete Resource">
        <div>Are you sure?</div>
      </ModalScroll>,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId("modal-overlay"));
    expect(onClose).toHaveBeenCalledTimes(2);
    unmount();

    const busyClose = vi.fn();
    render(
      <ModalScroll onClose={busyClose} label="Delete Resource" busy>
        <div>Deleting...</div>
      </ModalScroll>,
    );
    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.click(screen.getByTestId("modal-overlay"));
    expect(busyClose).not.toHaveBeenCalled();
  });

  it("keeps its natural height, leaving the panel chrome to the child", () => {
    render(
      <ModalScroll onClose={vi.fn()} label="Delete Resource">
        <div>Are you sure?</div>
      </ModalScroll>,
    );

    // The child is a direct child of the panel: this shape adds no scrolling
    // body, which is what lets a caller that brings its own card use it as-is.
    const panel = screen.getByTestId("modal-panel");
    expect(screen.getByText("Are you sure?").parentElement).toBe(panel);
  });
});
