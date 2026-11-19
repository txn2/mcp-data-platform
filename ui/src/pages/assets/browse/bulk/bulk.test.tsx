import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act, waitFor } from "@testing-library/react";
import { renderHook } from "@testing-library/react";
import type { Asset } from "@/api/portal/types";

const mutate = vi.fn();
vi.mock("@/api/portal/hooks/bulk", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/bulk")>()),
  useBulkAssets: () => ({ mutate, isPending: false }),
  usePortalTags: () => ({ data: { tags: [], total: 0 } }),
}));
vi.mock("@/api/portal/hooks/collections", () => ({
  useCollectionTargets: () => ({
    data: {
      data: [
        {
          id: "c1",
          name: "Board pack",
          updated_at: "",
          shared: false,
          sections: [
            { id: "s1", title: "Revenue", item_count: 1, has_asset: false },
            { id: "s2", title: "", item_count: 0, has_asset: false },
          ],
        },
      ],
    },
    isLoading: false,
  }),
}));
vi.mock("@/api/portal/hooks/assets", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/assets")>()),
  useSharePersonas: () => ({ data: { personas: [{ name: "finance", display_name: "Finance" }] } }),
}));
const fetched: string[] = [];
vi.mock("@/api/portal/client", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/client")>()),
  apiFetch: vi.fn(async (path: string) => {
    fetched.push(path);
    const offset = Number(new URLSearchParams(path.split("?")[1]).get("offset"));
    const rows = Array.from({ length: offset === 0 ? 100 : 20 }, (_, i) => ({ id: `a${offset + i}` }));
    return path.startsWith("/assets/search")
      ? { data: rows.map((asset) => ({ asset, score: 1 })), total: 120 }
      : { data: rows, total: 120 };
  }),
}));

import { BulkBar, SelectAllBanner, actionVerb } from "./BulkBar";
import { BulkDialog } from "./BulkDialog";
import { BulkResultPanel, skippedLabel } from "./BulkResultPanel";
import { AssetBulk, rowSelection } from "./AssetBulk";
import { allSelected, applyToggle, matchingIds, useAssetSelection } from "./selection";

afterEach(cleanup);
beforeEach(() => {
  mutate.mockReset();
  fetched.length = 0;
});

const ordered = ["a", "b", "c", "d"];

describe("selection (#2507)", () => {
  it("toggles one box and shift-selects a range from the anchor", () => {
    let t = applyToggle(new Set(), "b", false, null, ordered);
    expect([...t.selected]).toEqual(["b"]);
    expect(t.anchor).toBe("b");
    t = applyToggle(t.selected, "d", true, t.anchor, ordered);
    expect([...t.selected].sort()).toEqual(["b", "c", "d"]);
    t = applyToggle(t.selected, "a", true, "c", ordered);
    expect([...t.selected].sort()).toEqual(["a", "b", "c", "d"]);
    t = applyToggle(t.selected, "c", false, t.anchor, ordered);
    expect(t.selected.has("c")).toBe(false);
    expect(allSelected(new Set(ordered), ordered)).toBe(true);
    expect(allSelected(new Set(), [])).toBe(false);
  });

  it("selects and clears a page, replaces and clears", () => {
    const { result } = renderHook(() => useAssetSelection());
    act(() => result.current.togglePage(ordered));
    expect(result.current.count).toBe(4);
    act(() => result.current.togglePage(ordered));
    expect(result.current.count).toBe(0);
    act(() => result.current.toggle("a", false, ordered));
    act(() => result.current.toggle("c", true, ordered));
    expect(result.current.count).toBe(3);
    expect(result.current.has("b")).toBe(true);
    act(() => result.current.replace(["z"]));
    expect([...result.current.selected]).toEqual(["z"]);
    act(() => result.current.clear());
    expect(result.current.count).toBe(0);
  });

  it("reads every matching id a page at a time, listing or searching", async () => {
    expect(await matchingIds({ scope: "mine", tag: "finance" }, "")).toHaveLength(120);
    expect(fetched[0]).toContain("tag=finance");
    expect(fetched).toHaveLength(2);
    fetched.length = 0;
    expect(await matchingIds({ scope: "all" }, "revenue")).toHaveLength(120);
    expect(fetched[0]).toContain("/assets/search?");
    expect(fetched[0]).toContain("q=revenue");
  });
});

describe("BulkBar and the select-all banner (#2507)", () => {
  it("shows the count and the six actions, and nothing with no selection", () => {
    const onOpen = vi.fn();
    const onClear = vi.fn();
    const { container, rerender } = render(<BulkBar count={0} onOpen={onOpen} onClear={onClear} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<BulkBar count={60} onOpen={onOpen} onClear={onClear} />);
    expect(screen.getAllByText("60 selected")).toHaveLength(2);
    for (const label of ["Add tags", "Remove tags", "Add to collection", "Set status", "Share", "Delete"]) {
      fireEvent.click(screen.getByRole("button", { name: label }));
    }
    expect(onOpen.mock.calls.map((c) => c[0])).toEqual([
      "add_tags",
      "remove_tags",
      "add_to_collection",
      "status",
      "share",
      "delete",
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Status" }));
    expect(onOpen).toHaveBeenLastCalledWith("status");
    fireEvent.click(screen.getAllByRole("button", { name: "Clear" })[0]!);
    expect(onClear).toHaveBeenCalled();
  });

  it("offers every matching asset only when the page is selected and more match, refusing past 500", () => {
    const onSelectAll = vi.fn();
    const { rerender, container } = render(
      <SelectAllBanner pageAllSelected={false} pageCount={50} total={312} selectedCount={10} loading={false} onSelectAll={onSelectAll} />,
    );
    expect(container).toBeEmptyDOMElement();
    rerender(<SelectAllBanner pageAllSelected pageCount={50} total={312} selectedCount={50} loading={false} onSelectAll={onSelectAll} />);
    fireEvent.click(screen.getByRole("button", { name: "Select all 312 matching" }));
    expect(onSelectAll).toHaveBeenCalled();
    rerender(<SelectAllBanner pageAllSelected pageCount={50} total={900} selectedCount={50} loading={false} onSelectAll={onSelectAll} />);
    expect(screen.getByRole("button", { name: "Select all 900 matching" })).toBeDisabled();
    expect(screen.getByText(/Over 500 at once is refused/)).toBeInTheDocument();
  });

  it("names each action's result", () => {
    expect(actionVerb("share")).toBe("Shared");
    expect(actionVerb("delete")).toBe("Deleted");
    expect(actionVerb("add_tags")).toBe("Tagged");
    expect(actionVerb("remove_tags")).toBe("Untagged");
    expect(actionVerb("add_to_collection")).toBe("Added to collection");
    expect(actionVerb("set_status")).toBe("Status set");
    expect(actionVerb("clear_status")).toBe("Status cleared");
  });
});

describe("BulkDialog (#2507)", () => {
  function open(kind: Parameters<typeof BulkDialog>[0]["kind"]) {
    const onApply = vi.fn();
    render(<BulkDialog kind={kind} count={3} firstId="a1" busy={false} onApply={onApply} onClose={vi.fn()} />);
    return onApply;
  }

  it("adds tags", () => {
    const onApply = open("add_tags");
    expect(screen.getByText("Add tags to 3 assets")).toBeInTheDocument();
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "finance, board" } });
    fireEvent.click(screen.getByRole("button", { name: "Add tags" }));
    expect(onApply).toHaveBeenCalledWith({ action: "add_tags", tags: ["finance", "board"] });
  });

  it("files into a collection section", () => {
    const onApply = open("add_to_collection");
    fireEvent.click(screen.getByRole("button", { name: "Board pack" }));
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "s1" } });
    fireEvent.click(screen.getByRole("button", { name: "Add to collection" }));
    expect(onApply).toHaveBeenCalledWith({ action: "add_to_collection", collection_id: "c1", section_id: "s1" });
  });

  it("sets a deprecated status with its note, and clears", () => {
    const onApply = open("status");
    fireEvent.click(screen.getByLabelText("Deprecated"));
    fireEvent.change(screen.getByLabelText(/Note readers see/), { target: { value: "Replaced" } });
    fireEvent.click(screen.getByRole("button", { name: "Set status" }));
    expect(onApply).toHaveBeenCalledWith({ action: "set_status", status: "deprecated", note: "Replaced" });
    fireEvent.click(screen.getByRole("button", { name: "Clear status" }));
    expect(onApply).toHaveBeenLastCalledWith({ action: "clear_status" });
  });

  it("shares with a person by address or with a persona", () => {
    const onApply = open("share");
    const who = screen.getByLabelText("With a person or persona");
    fireEvent.change(who, { target: { value: "carol@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(onApply).toHaveBeenCalledWith({
      action: "share",
      share: { shared_with_email: "carol@example.com", permission: "viewer" },
    });
    fireEvent.change(who, { target: { value: "finance" } });
    fireEvent.change(screen.getByLabelText("Permission"), { target: { value: "editor" } });
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(onApply).toHaveBeenLastCalledWith({
      action: "share",
      share: { shared_with_persona: "finance", permission: "editor" },
    });
  });

  it("confirms a delete", () => {
    const onApply = open("delete");
    expect(screen.getByText(/go to Trash/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onApply).toHaveBeenCalledWith({ action: "delete" });
  });
});

describe("BulkResultPanel (#2507)", () => {
  it("lists skipped assets, naming only those the reader can open", () => {
    const names = new Map([
      ["a1", "Monthly churn"],
      ["a2", "Q3 revenue"],
    ]);
    expect(skippedLabel("a1", "only the owner can share this asset", names)).toBe("Monthly churn");
    expect(skippedLabel("7f3c99", "not found or no access", names)).toBe("An asset (id 7f3c…)");
    const onSelect = vi.fn();
    const onDone = vi.fn();
    render(
      <BulkResultPanel
        result={{
          action: "share",
          requested: 3,
          applied: ["a2"],
          skipped: [
            { asset_id: "a1", reason: "only the owner can share this asset" },
            { asset_id: "7f3c99", reason: "not found or no access" },
          ],
        }}
        names={names}
        onSelectSkipped={onSelect}
        onDone={onDone}
      />,
    );
    expect(screen.getByText("Shared · Applied to 1 · Skipped 2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Select the skipped 2" }));
    expect(onSelect).toHaveBeenCalledWith(["a1", "7f3c99"]);
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onDone).toHaveBeenCalled();
  });
});

describe("AssetBulk (#2507)", () => {
  const items = [
    { asset: { id: "a1", name: "One" } as Asset },
    { asset: { id: "a2", name: "Two" } as Asset },
  ];

  it("applies a dialog's action to the selection and shows the result", async () => {
    const { result } = renderHook(() => useAssetSelection());
    act(() => result.current.replace(["a1", "a2"]));
    mutate.mockImplementation((_req, opts) =>
      opts.onSuccess({ action: "delete", requested: 2, applied: ["a1"], skipped: [{ asset_id: "a2", reason: "only the owner can delete this asset" }] }),
    );
    const { rerender } = render(
      <AssetBulk selection={result.current} items={items} total={2} filters={{}} query="" />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialogDelete = await screen.findAllByRole("button", { name: "Delete" });
    fireEvent.click(dialogDelete[dialogDelete.length - 1]!);
    expect(mutate.mock.calls[0]![0]).toEqual({ action: "delete", asset_ids: ["a1", "a2"] });
    rerender(<AssetBulk selection={result.current} items={items} total={2} filters={{}} query="" />);
    await waitFor(() => expect(screen.getByText("Deleted · Applied to 1 · Skipped 1")).toBeInTheDocument());
    expect(screen.getByText("Two")).toBeInTheDocument();
  });

  it("adapts a selection to the rows shown", () => {
    const { result } = renderHook(() => useAssetSelection());
    const rows = rowSelection(result.current, items);
    expect(rows.pageAllSelected).toBe(false);
    act(() => rows.onTogglePage());
    expect(result.current.count).toBe(2);
    act(() => rowSelection(result.current, items).onToggle("a1", false));
    expect(result.current.has("a1")).toBe(false);
  });
});
