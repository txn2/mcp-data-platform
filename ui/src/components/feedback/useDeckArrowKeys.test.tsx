import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render } from "@testing-library/react";
import { DECK_FRAME_ATTR } from "@/lib/deck";
import { useDeckArrowKeys } from "./useDeckArrowKeys";

function Harness({ active }: { active: boolean }) {
  useDeckArrowKeys(active);
  return (
    <div>
      <textarea aria-label="Message" />
      <input aria-label="Name" />
      <div role="tab" tabIndex={0} aria-label="Tab" />
      <div contentEditable aria-label="Editor" suppressContentEditableWarning />
      <div role="dialog" aria-label="Dialog">
        <button type="button">In a dialog</button>
      </div>
      <button type="button">Outside</button>
    </div>
  );
}

let posted: ReturnType<typeof vi.fn>;
let frame: HTMLIFrameElement;

beforeEach(() => {
  frame = document.createElement("iframe");
  frame.setAttribute(DECK_FRAME_ATTR, "");
  document.body.appendChild(frame);
  posted = vi.fn();
  vi.spyOn(frame.contentWindow as Window, "postMessage").mockImplementation(posted as never);
});

afterEach(() => {
  frame.remove();
  vi.restoreAllMocks();
});

const moves = () => posted.mock.calls.map((c) => JSON.parse(c[0] as string).method as string);

describe("useDeckArrowKeys", () => {
  it("moves the deck for each arrow key with focus outside a text box", () => {
    const { getByText } = render(<Harness active />);
    const outside = getByText("Outside");
    for (const key of ["ArrowRight", "ArrowLeft", "ArrowUp", "ArrowDown"]) fireEvent.keyDown(outside, { key });
    expect(moves()).toEqual(["right", "left", "up", "down"]);
  });

  it("stops the arrow from scrolling the page when it moves the deck", () => {
    const { getByText } = render(<Harness active />);
    expect(fireEvent.keyDown(getByText("Outside"), { key: "ArrowRight" })).toBe(false);
  });

  it.each(["Message", "Name", "Tab", "Editor"])("leaves the arrows to %s", (label) => {
    const { getByLabelText } = render(<Harness active />);
    expect(fireEvent.keyDown(getByLabelText(label), { key: "ArrowRight" })).toBe(true);
    expect(posted).not.toHaveBeenCalled();
  });

  it("leaves the arrows alone inside a dialog open over the page", () => {
    const { getByText } = render(<Harness active />);
    expect(fireEvent.keyDown(getByText("In a dialog"), { key: "ArrowRight" })).toBe(true);
    expect(posted).not.toHaveBeenCalled();
  });

  it("ignores other keys, modified arrows and a handled key", () => {
    const { getByText } = render(<Harness active />);
    const outside = getByText("Outside");
    fireEvent.keyDown(outside, { key: "a" });
    fireEvent.keyDown(outside, { key: "ArrowRight", altKey: true });
    fireEvent.keyDown(outside, { key: "ArrowRight", metaKey: true });
    fireEvent.keyDown(outside, { key: "ArrowRight", shiftKey: true });
    fireEvent.keyDown(outside, { key: "ArrowRight", ctrlKey: true });
    outside.addEventListener("keydown", (e) => e.preventDefault());
    fireEvent.keyDown(outside, { key: "ArrowRight" });
    expect(posted).not.toHaveBeenCalled();
  });

  it("does nothing while the panel is closed, or with no deck on the page", () => {
    const { getByText, rerender } = render(<Harness active={false} />);
    fireEvent.keyDown(getByText("Outside"), { key: "ArrowRight" });
    expect(posted).not.toHaveBeenCalled();
    rerender(<Harness active />);
    frame.removeAttribute(DECK_FRAME_ATTR);
    expect(fireEvent.keyDown(getByText("Outside"), { key: "ArrowRight" })).toBe(true);
    expect(posted).not.toHaveBeenCalled();
  });
});
