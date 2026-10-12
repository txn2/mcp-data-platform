import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/react";
import { confirmLeave, hasUnsavedChanges, useUnsavedChangesGuard, DISCARD_PROMPT } from "./navigationGuard";

function Form({ dirty }: { dirty: boolean }) {
  useUnsavedChangesGuard(dirty);
  return null;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("navigationGuard (#2093)", () => {
  it("lets navigation through when no form is dirty", () => {
    const confirm = vi.spyOn(window, "confirm");
    render(<Form dirty={false} />);
    expect(confirmLeave()).toBe(true);
    expect(confirm).not.toHaveBeenCalled();
  });

  it("asks while a form is dirty, and stays when the answer is no", () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<Form dirty />);
    expect(hasUnsavedChanges()).toBe(true);
    expect(confirmLeave()).toBe(false);
    expect(confirm).toHaveBeenCalledWith(DISCARD_PROMPT);
    expect(hasUnsavedChanges()).toBe(true);
  });

  it("leaves and forgets the edit when the answer is yes", () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<Form dirty />);
    expect(confirmLeave()).toBe(true);
    expect(hasUnsavedChanges()).toBe(false);
  });

  it("releases a form that saves or unmounts, and asks the browser on unload while dirty", () => {
    const { rerender, unmount } = render(<Form dirty />);
    const ev = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    rerender(<Form dirty={false} />);
    expect(hasUnsavedChanges()).toBe(false);
    rerender(<Form dirty />);
    unmount();
    expect(hasUnsavedChanges()).toBe(false);
    const after = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(after);
    expect(after.defaultPrevented).toBe(false);
  });
});
