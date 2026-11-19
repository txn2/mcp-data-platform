import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { useContext } from "react";
import { createPortal } from "react-dom";
import { ItemPanelSlot, PageArea, usePanelSheet } from "./PageArea";
import { SheetHandle } from "@/components/feedback/SheetControls";

// A panel in the slot that shows the sheet's controls when it is one.
function Panel() {
  const slot = useContext(ItemPanelSlot);
  const sheet = usePanelSheet();
  const body = <div>{sheet ? <SheetHandle sheet={sheet} /> : <p>beside the page</p>}</div>;
  return slot ? createPortal(body, slot) : null;
}

const slot = () => document.querySelector("[data-item-panel]") as HTMLElement;

function setWidth(width: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
}

beforeEach(() => {
  setWidth(390);
  vi.restoreAllMocks();
});

// #2705: on a narrow screen the panel is a sheet of 60% or the whole screen
// below the top bar, the size kept between threads and visits.
describe("PageArea sheet", () => {
  it("opens at 60% and is made full size with the handle, remembering the choice", () => {
    const { unmount } = render(<PageArea><Panel /></PageArea>);
    expect(slot().className).toContain("h-[60dvh]");
    fireEvent.click(screen.getByRole("button", { name: "Make the feedback sheet bigger" }));
    expect(slot().className).toContain("absolute inset-0");
    expect(slot().dataset.sheetSize).toBe("full");
    expect(localStorage.getItem("feedback-sheet-size")).toBe("full");
    unmount();
    render(<PageArea><Panel /></PageArea>);
    expect(slot().dataset.sheetSize).toBe("full");
    fireEvent.click(screen.getByRole("button", { name: "Make the feedback sheet smaller" }));
    expect(slot().className).toContain("h-[60dvh]");
  });

  it("follows a drag on the handle: up is full size, down is smaller", () => {
    render(<PageArea><Panel /></PageArea>);
    const handle = screen.getByTestId("sheet-handle");
    fireEvent.pointerDown(handle, { clientY: 400 });
    fireEvent.pointerUp(handle, { clientY: 200 });
    fireEvent.click(handle);
    expect(slot().dataset.sheetSize).toBe("full");
    fireEvent.pointerDown(handle, { clientY: 200 });
    fireEvent.pointerUp(handle, { clientY: 500 });
    fireEvent.click(handle);
    expect(slot().dataset.sheetSize).toBe("default");
  });

  it("is no sheet beside the page, where the panel keeps its column", () => {
    setWidth(1280);
    render(<PageArea><Panel /></PageArea>);
    expect(screen.getByText("beside the page")).toBeInTheDocument();
    expect(slot().className).toContain("lg:w-md");
    expect(slot().className).toContain("lg:h-auto");
  });

  it("keeps working when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    render(<PageArea><Panel /></PageArea>);
    fireEvent.click(screen.getByRole("button", { name: "Make the feedback sheet bigger" }));
    expect(slot().dataset.sheetSize).toBe("full");
  });
});
