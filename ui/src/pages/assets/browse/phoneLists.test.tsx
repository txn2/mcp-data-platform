import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import type { Asset, Collection } from "@/api/portal/types";
import { DEFAULT_ASSET_SORT, DEFAULT_COLLECTION_SORT } from "@/components/listSort";
import { AssetTable } from "./AssetTable";
import { AssetFilterBar } from "./AssetFilterBar";
import { CollectionTable } from "@/pages/collections/browse/CollectionTable";
import { SectionIntro } from "@/components/patterns/SectionIntro";

// #2723: below 768px the lists are rows a person can read, the filters fold
// behind a Filters button, and the section intro starts collapsed.

const wide = window.innerWidth;
beforeEach(() => {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 });
});
afterEach(() => {
  cleanup();
  Object.defineProperty(window, "innerWidth", { configurable: true, value: wide });
});

function asset(id: string, extra: Partial<Asset> = {}): Asset {
  return {
    id,
    owner_id: "u1",
    owner_email: "ann@example.com",
    name: `Weekly sales summary for the regional leadership review ${id}`,
    description: "",
    content_type: "text/html",
    size_bytes: 2048,
    tags: ["sales", "weekly", "leadership"],
    collections: [{ id: "c1", name: "QBR" }],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    ...extra,
  } as unknown as Asset;
}

describe("Assets on a phone (#2723)", () => {
  it("is rows with the name, type, status and one line of who, when and size; no table and no tags", () => {
    const onNavigate = vi.fn();
    render(
      <AssetTable
        items={[{ asset: asset("a1", { status: { value: "draft" } } as Partial<Asset>) }, { asset: asset("a2") }]}
        sort={DEFAULT_ASSET_SORT}
        onNavigate={onNavigate}
        onPreview={vi.fn()}
      />,
    );
    expect(screen.queryByRole("table")).toBeNull();
    const rows = screen.getAllByTestId("asset-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByTestId("asset-row-meta").textContent).toMatch(/ann@example\.com · Updated .* · 2(\.0)? KB/);
    expect(within(rows[0]!).queryByText("sales")).toBeNull();
    expect(within(rows[0]!).queryByText("QBR")).toBeNull();
    fireEvent.click(within(rows[1]!).getByTestId("asset-row-name"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/a2");
  });

  it("selects a tapped row instead of opening it while anything is selected", () => {
    const onNavigate = vi.fn();
    const onToggle = vi.fn();
    render(
      <AssetTable
        items={[{ asset: asset("a1") }, { asset: asset("a2") }]}
        sort={DEFAULT_ASSET_SORT}
        onNavigate={onNavigate}
        onPreview={vi.fn()}
        selection={{ has: (id) => id === "a1", onToggle, pageAllSelected: false, onTogglePage: vi.fn() }}
      />,
    );
    fireEvent.click(within(screen.getAllByTestId("asset-row")[1]!).getByTestId("asset-row-name"));
    expect(onToggle).toHaveBeenCalledWith("a2", false);
    expect(onNavigate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByLabelText(/Select Weekly sales summary .* a1/));
    expect(onToggle).toHaveBeenCalledWith("a1", false);
  });

  it("folds the filters behind a counted Filters button whose chips clear each one", () => {
    const onContentTypeChange = vi.fn();
    const onStatusChange = vi.fn();
    render(
      <AssetFilterBar
        scope="mine"
        onScopeChange={vi.fn()}
        search=""
        onSearchChange={vi.fn()}
        contentType="text/html"
        onContentTypeChange={onContentTypeChange}
        tag=""
        onTagChange={vi.fn()}
        tagFacets={[]}
        owners={[]}
        onOwnersChange={vi.fn()}
        authorFacets={[]}
        status="draft"
        onStatusChange={onStatusChange}
        sort={DEFAULT_ASSET_SORT}
        onSortChange={vi.fn()}
        sortDisabled={false}
        viewMode="table"
        onViewModeChange={vi.fn()}
        resultCount={6}
      />,
    );
    expect(screen.getByTestId("phone-filter-bar")).toBeTruthy();
    expect(screen.getByPlaceholderText("Search assets")).toBeTruthy();
    expect(screen.queryByLabelText("Filter by content type")).toBeNull();
    const button = screen.getByRole("button", { name: /Filters/ });
    expect(button.textContent).toContain("2");
    fireEvent.click(screen.getByRole("button", { name: /Type: HTML/ }));
    expect(onContentTypeChange).toHaveBeenCalledWith("");
    fireEvent.click(button);
    expect(screen.getByRole("dialog", { name: "Filters" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Show 6 assets" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear all" }));
    expect(onStatusChange).toHaveBeenCalledWith("");
  });
});

describe("Collections on a phone (#2723)", () => {
  it("is rows of the name and one line of who and when", () => {
    const coll = {
      id: "c1", owner_id: "u", owner_email: "ann@example.com", name: "Quarterly business review, all regions",
      description: "", config: {}, sections: [], asset_tags: ["a", "b"], created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-02T00:00:00Z",
    } as unknown as Collection;
    const onNavigate = vi.fn();
    render(<CollectionTable items={[{ collection: coll }]} sort={DEFAULT_COLLECTION_SORT} onNavigate={onNavigate} />);
    expect(screen.queryByRole("table")).toBeNull();
    const row = screen.getByTestId("collection-row");
    expect(row.textContent).toMatch(/ann@example\.com · Updated/);
    expect(within(row).queryByText("a")).toBeNull();
    fireEvent.click(within(row).getByRole("button"));
    expect(onNavigate).toHaveBeenCalledWith("/collections/c1");
  });
});

describe("The section intro on a phone (#2723)", () => {
  it("starts collapsed, opens on a tap and remembers it", () => {
    const store: Record<string, string> = {};
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => store[k] ?? null,
      setItem: (k: string, v: string) => { store[k] = v; },
      removeItem: (k: string) => { delete store[k]; },
    });
    const { unmount } = render(<SectionIntro route="/assets" />);
    fireEvent.click(screen.getByRole("button", { name: /About this page/ }));
    expect(screen.getByRole("region", { name: /is for/ })).toBeTruthy();
    unmount();
    render(<SectionIntro route="/assets" />);
    expect(screen.queryByRole("button", { name: /About this page/ })).toBeNull();
    vi.unstubAllGlobals();
  });
});
