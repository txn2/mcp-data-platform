import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { SlideOver } from "./SlideOver";

describe("SlideOver", () => {
  it("stays open on a backdrop click and closes on Escape (#2468)", () => {
    const onClose = vi.fn();
    const { container } = render(
      <SlideOver onClose={onClose}>
        <textarea aria-label="Feedback" defaultValue="typed" />
      </SlideOver>,
    );

    fireEvent.click(container.querySelector('[aria-hidden="true"]')!);
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
