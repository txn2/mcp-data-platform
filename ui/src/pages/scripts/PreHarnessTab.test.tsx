import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { PreHarnessTab } from "./PreHarnessTab";

const listing = vi.fn();
vi.mock("@/api/admin/hooks/scripts", () => ({
  usePreHarnessScripts: () => listing(),
}));

afterEach(() => {
  cleanup();
  listing.mockReset();
});

const row = {
  id: "s1", name: "weekly-sales", display_name: "Weekly sales", owner_email: "jane@example.com",
  updated_at: "2026-09-01T10:00:00Z", lint_findings: 3, tests: 0,
};

describe("PreHarnessTab (#1943)", () => {
  it("lists each older automation with its findings and tests, and opens it on row click", () => {
    listing.mockReturnValue({
      data: { data: [row, { ...row, id: "s2", name: "daily", display_name: "", lint_findings: 0, tests: 0, owner_email: "" }], total: 2, examined: 2, pre_harness: 2 },
      isLoading: false, error: null,
    });
    const onNavigate = vi.fn();
    render(<PreHarnessTab basePath="/admin/automations" onNavigate={onNavigate} />);
    expect(screen.getByText("Weekly sales")).toBeInTheDocument();
    expect(screen.getByText("jane@example.com")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("nobody")).toBeInTheDocument();
    expect(screen.getAllByText("daily")).toHaveLength(2);
    fireEvent.click(screen.getByText("Weekly sales"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/automations/s1");
    expect(screen.queryByText(/were checked/)).not.toBeInTheDocument();
  });

  it("says when there were more than it checked", () => {
    listing.mockReturnValue({
      data: { data: [row], total: 1, examined: 200, pre_harness: 250 }, isLoading: false, error: null,
    });
    render(<PreHarnessTab basePath="/admin/automations" onNavigate={vi.fn()} />);
    expect(screen.getByText(/250 automations were saved before tests were required; the first 200/)).toBeInTheDocument();
  });

  it("says when every older automation is up to date", () => {
    listing.mockReturnValue({ data: { data: [], total: 0, examined: 4, pre_harness: 4 }, isLoading: false, error: null });
    render(<PreHarnessTab basePath="/admin/automations" onNavigate={vi.fn()} />);
    expect(screen.getByText(/has been brought up to date/)).toBeInTheDocument();
  });

  it("says when it is loading and when it could not load", () => {
    listing.mockReturnValue({ data: undefined, isLoading: true, error: null });
    render(<PreHarnessTab basePath="/admin/automations" onNavigate={vi.fn()} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
    cleanup();
    listing.mockReturnValue({ data: undefined, isLoading: false, error: new Error("x") });
    render(<PreHarnessTab basePath="/admin/automations" onNavigate={vi.fn()} />);
    expect(screen.getByText("These automations could not be loaded.")).toBeInTheDocument();
  });
});
