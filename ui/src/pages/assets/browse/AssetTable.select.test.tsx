import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { Asset } from "@/api/portal/types";
import { DEFAULT_ASSET_SORT } from "@/components/listSort";
import { AssetTable } from "./AssetTable";

afterEach(cleanup);

function asset(id: string): Asset {
  return {
    id,
    owner_id: "u1",
    owner_email: "ann@example.com",
    name: `Asset ${id}`,
    description: "",
    content_type: "text/html",
    size_bytes: 10,
    tags: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  } as unknown as Asset;
}

describe("AssetTable bulk selection (#2507)", () => {
  it("ticks a row without opening it, and selects the page from the header", () => {
    const onNavigate = vi.fn();
    const onToggle = vi.fn();
    const onTogglePage = vi.fn();
    render(
      <AssetTable
        items={[{ asset: asset("a1") }, { asset: asset("a2") }]}
        sort={DEFAULT_ASSET_SORT}
        onNavigate={onNavigate}
        onPreview={vi.fn()}
        selection={{ has: (id) => id === "a1", onToggle, pageAllSelected: false, onTogglePage }}
      />,
    );
    expect(screen.getByLabelText("Select Asset a1")).toBeChecked();
    fireEvent.click(screen.getByLabelText("Select Asset a2"), { shiftKey: true });
    expect(onToggle).toHaveBeenCalledWith("a2", true);
    expect(onNavigate).not.toHaveBeenCalled();
    const header = screen.getByLabelText("Select all on this page") as HTMLInputElement;
    expect(header.indeterminate).toBe(true);
    fireEvent.click(header);
    expect(onTogglePage).toHaveBeenCalled();
  });

  it("carries no boxes without a selection", () => {
    render(<AssetTable items={[{ asset: asset("a1") }]} sort={DEFAULT_ASSET_SORT} onNavigate={vi.fn()} onPreview={vi.fn()} />);
    expect(screen.queryByLabelText("Select Asset a1")).not.toBeInTheDocument();
  });
});
