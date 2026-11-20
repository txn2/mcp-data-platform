import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { BuildWatch, type BuildStore } from "@/lib/buildWatch";
import { BuildNotice } from "./BuildNotice";

function tab(): BuildStore {
  const m = new Map<string, string>();
  return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => void m.set(k, v) };
}

const NOTICE = "The portal has been updated. Save your changes, then reload to use the new version.";

describe("BuildNotice (#2727)", () => {
  it("shows nothing while the page runs the newest build it has heard of", () => {
    render(<BuildNotice watch={new BuildWatch("v1+a", tab())} isBusy={() => true} />);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("shows nothing for a new build while the reader is not busy: the next navigation moves the page", () => {
    const watch = new BuildWatch("v1+a", tab());
    render(<BuildNotice watch={watch} isBusy={() => false} />);
    act(() => watch.observe("v2+b"));
    expect(screen.queryByText(NOTICE)).toBeNull();
  });

  it("with unsaved changes, a new build shows the notice and Reload reloads", () => {
    const watch = new BuildWatch("v1+a", tab());
    const reload = vi.fn();
    render(<BuildNotice watch={watch} isBusy={() => true} reload={reload} />);
    act(() => watch.observe("v2+b"));
    expect(screen.getByRole("status").textContent).toContain(NOTICE);
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(reload).toHaveBeenCalledOnce();
  });

  it("shows the notice when a reload for the new build came back on the old one", () => {
    const store = tab();
    const first = new BuildWatch("v1+a", store);
    first.observe("v2+b");
    first.reloadOnNavigate();
    const again = new BuildWatch("v1+a", store);
    render(<BuildNotice watch={again} isBusy={() => false} />);
    act(() => again.observe("v2+b"));
    expect(screen.getByText(NOTICE)).toBeTruthy();
  });

  it("keeps the notice once shown, after the reader stops being busy", () => {
    vi.useFakeTimers();
    try {
      const watch = new BuildWatch("v1+a", tab());
      let busy = true;
      render(<BuildNotice watch={watch} isBusy={() => busy} />);
      act(() => watch.observe("v2+b"));
      expect(screen.getByText(NOTICE)).toBeTruthy();
      busy = false;
      act(() => vi.advanceTimersByTime(3000));
      expect(screen.getByText(NOTICE)).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });
});
