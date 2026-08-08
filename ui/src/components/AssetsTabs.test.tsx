import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { AssetsTabs } from "./AssetsTabs";

afterEach(cleanup);

// Choosing a face here leaves the page the reader is on, so the strip must not
// select on focus the way a panel switcher can afford to. Radix's default does
// exactly that, which would turn an arrow key into a navigation.
describe("AssetsTabs", () => {
  // Focus is what an arrow key moves; under Radix's automatic default the
  // trigger's own focus handler is what would then fire the navigation, so
  // that handler is the thing worth pinning.
  it("does not navigate when focus lands on a face", () => {
    const onNavigate = vi.fn();
    render(<AssetsTabs active="assets" onNavigate={onNavigate} />);

    fireEvent.focus(screen.getByRole("tab", { name: "Collections" }));

    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("navigates to the chosen route when a face is pressed", () => {
    const onNavigate = vi.fn();
    render(<AssetsTabs active="assets" onNavigate={onNavigate} />);

    fireEvent.mouseDown(screen.getByRole("tab", { name: "Collections" }));

    expect(onNavigate).toHaveBeenCalledWith("/collections");
  });

  // Radix stamps every trigger with aria-controls naming a TabsContent. These
  // faces lead to routes, so that id would resolve to nothing and both a
  // screen reader and an axe reference check would report a dead relationship.
  it("names no panel it does not render", () => {
    render(<AssetsTabs active="assets" onNavigate={vi.fn()} />);

    for (const tab of screen.getAllByRole("tab")) {
      expect(tab).not.toHaveAttribute("aria-controls");
    }
  });
});
