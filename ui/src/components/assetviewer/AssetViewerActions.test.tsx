import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { AssetViewerActions } from "./AssetViewerActions";

// A Radix menu opens on pointerdown with the primary button, not on click.
function openMenu(trigger: HTMLElement) {
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: "mouse" });
}

function setWidth(w: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: w });
}

afterEach(() => setWidth(1024));

function handlers() {
  return {
    onDelete: vi.fn(),
    onDownload: vi.fn(),
    onCopyToMyAssets: vi.fn(),
    onShare: vi.fn(),
    onAddToCollection: vi.fn(),
  };
}

describe("AssetViewerActions on a phone (#2390)", () => {
  it("keeps Share a button and puts the rest in More, Delete last", () => {
    setWidth(400);
    const h = handlers();
    render(<AssetViewerActions isOwner canShare {...h} />);
    expect(screen.queryByRole("button", { name: /Download/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(h.onShare).toHaveBeenCalled();

    openMenu(screen.getByRole("button", { name: "More" }));
    const items = within(screen.getByRole("menu")).getAllByRole("menuitem").map((i) => i.textContent);
    expect(items).toEqual(["Download", "Add to collection", "Delete"]);
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(h.onDelete).toHaveBeenCalled();
  });

  it("offers a reader who does not own it Save to My Assets in More, with the Shared badge", () => {
    setWidth(400);
    const h = handlers();
    render(
      <AssetViewerActions
        isOwner={false}
        sharePermission="viewer"
        copyMutation={{ isPending: false, mutate: vi.fn() } as never}
        {...h}
      />,
    );
    expect(screen.getByText("Shared (Viewer)")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Share" })).toBeNull();
    openMenu(screen.getByRole("button", { name: "More" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Save to My Assets" }));
    expect(h.onCopyToMyAssets).toHaveBeenCalled();
    expect(screen.queryByRole("menuitem", { name: "Delete" })).toBeNull();
  });

  it("shows no More when there is nothing to put in it", () => {
    setWidth(400);
    render(<AssetViewerActions isOwner={false} onDelete={vi.fn()} onCopyToMyAssets={vi.fn()} onShare={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "More" })).toBeNull();
  });

  it("lays every action out as a button on a wider screen", () => {
    setWidth(1024);
    render(<AssetViewerActions isOwner canShare {...handlers()} />);
    for (const name of [/Delete/, /Download/, /Add to collection/, /Share/]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    expect(screen.queryByRole("button", { name: "More" })).toBeNull();
  });
});
