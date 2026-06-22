import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";

const h = vi.hoisted(() => ({
  baseline: { data: undefined as { baseline: string } | undefined, isLoading: false },
}));

// Override only the baseline hook the panel consumes; keep everything else real.
vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useAgentInstructionsBaseline: () => h.baseline,
  };
});

import { PlatformBaselinePanel } from "./ConfigEditorPage";

afterEach(() => {
  cleanup();
  h.baseline = { data: undefined, isLoading: false };
});

describe("PlatformBaselinePanel", () => {
  it("renders the baseline text when present", () => {
    h.baseline = {
      data: { baseline: "How to operate this platform:\n- Call `search` first." },
      isLoading: false,
    };
    render(<PlatformBaselinePanel />);
    expect(screen.getByTestId("platform-baseline-panel")).toBeInTheDocument();
    expect(screen.getByText(/How to operate this platform/)).toBeInTheDocument();
    expect(screen.getByText(/Call `search` first/)).toBeInTheDocument();
  });

  it("renders nothing when the baseline is empty", () => {
    h.baseline = { data: { baseline: "" }, isLoading: false };
    const { container } = render(<PlatformBaselinePanel />);
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByTestId("platform-baseline-panel")).not.toBeInTheDocument();
  });

  it("renders nothing while loading", () => {
    h.baseline = { data: undefined, isLoading: true };
    const { container } = render(<PlatformBaselinePanel />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the baseline is only whitespace", () => {
    h.baseline = { data: { baseline: "   \n  " }, isLoading: false };
    const { container } = render(<PlatformBaselinePanel />);
    expect(container).toBeEmptyDOMElement();
  });
});
