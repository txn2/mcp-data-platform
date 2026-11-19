import { useMemo, useState } from "react";
import { ApiError } from "@/api/portal/client";
import type { AssetQueryFilters } from "@/api/portal/hooks/assets";
import { MAX_BULK_ASSETS, useBulkAssets, type BulkResult } from "@/api/portal/hooks/bulk";
import type { DisplayAsset } from "../types";
import { BulkBar, SelectAllBanner, type BulkDialogKind } from "./BulkBar";
import { BulkDialog, type BulkArgs } from "./BulkDialog";
import { BulkResultPanel } from "./BulkResultPanel";
import { allSelected, matchingIds, type AssetSelection } from "./selection";

/** The selection props the grid and the table take. */
export interface RowSelection {
  has: (id: string) => boolean;
  onToggle: (id: string, shift: boolean) => void;
  pageAllSelected: boolean;
  onTogglePage: () => void;
}

/** rowSelection adapts a selection to the rows on screen, in their order. */
export function rowSelection(selection: AssetSelection, items: DisplayAsset[]): RowSelection {
  const ordered = items.map((i) => i.asset.id);
  return {
    has: selection.has,
    onToggle: (id, shift) => selection.toggle(id, shift, ordered),
    pageAllSelected: allSelected(selection.selected, ordered),
    onTogglePage: () => selection.togglePage(ordered),
  };
}

/**
 * AssetBulk is everything a selection on the Assets tab brings up (#2507):
 * the toolbar, the offer to select every matching asset, the action dialogs
 * and the result of the last action.
 */
export function AssetBulk({
  selection,
  items,
  total,
  filters,
  query,
}: {
  selection: AssetSelection;
  items: DisplayAsset[];
  total?: number;
  filters: AssetQueryFilters;
  query: string;
}) {
  const [dialog, setDialog] = useState<BulkDialogKind | null>(null);
  const [result, setResult] = useState<BulkResult | null>(null);
  const [error, setError] = useState("");
  const [selectingAll, setSelectingAll] = useState(false);
  const bulk = useBulkAssets();
  const names = useMemo(() => new Map(items.map((i) => [i.asset.id, i.asset.name])), [items]);
  const ordered = items.map((i) => i.asset.id);
  const ids = [...selection.selected];

  function apply(args: BulkArgs) {
    setError("");
    bulk.mutate(
      { ...args, asset_ids: ids.slice(0, MAX_BULK_ASSETS) },
      {
        onSuccess: (res) => {
          setResult(res);
          setDialog(null);
          selection.clear();
        },
        onError: (err) => setError(err instanceof ApiError ? err.message : "The action failed. Try again."),
      },
    );
  }

  async function selectAll() {
    setSelectingAll(true);
    try {
      selection.replace(await matchingIds(filters, query));
    } catch {
      setError("The matching assets could not be read. Try again.");
    } finally {
      setSelectingAll(false);
    }
  }

  return (
    <div className="space-y-2">
      <BulkBar count={selection.count} onOpen={setDialog} onClear={selection.clear} />
      <SelectAllBanner
        pageAllSelected={allSelected(selection.selected, ordered)}
        pageCount={ordered.length}
        total={total}
        selectedCount={selection.count}
        loading={selectingAll}
        onSelectAll={() => void selectAll()}
      />
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      {result && (
        <BulkResultPanel
          result={result}
          names={names}
          onSelectSkipped={(skipped) => {
            selection.replace(skipped);
            setResult(null);
          }}
          onDone={() => setResult(null)}
        />
      )}
      <BulkDialog
        kind={dialog}
        count={selection.count}
        firstId={ids[0] ?? ""}
        busy={bulk.isPending}
        onApply={apply}
        onClose={() => setDialog(null)}
      />
    </div>
  );
}
