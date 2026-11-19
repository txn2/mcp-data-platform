import { useCallback, useRef, useState } from "react";
import { apiFetch } from "@/api/portal/client";
import { assetFilterParams, type AssetQueryFilters } from "@/api/portal/hooks/assets";
import { MAX_BULK_ASSETS } from "@/api/portal/hooks/bulk";
import type { Asset, PaginatedResponse, ScoredAsset } from "@/api/portal/types";

/** The outcome of one click on a row's box. */
export interface Toggled {
  selected: Set<string>;
  anchor: string | null;
}

/**
 * applyToggle is one click on a box. A plain click turns that row on or off
 * and makes it the anchor; a shift-click turns on every row between the
 * anchor and this one, in the order they are shown, the way a file list does.
 */
export function applyToggle(
  selected: Set<string>,
  id: string,
  shift: boolean,
  anchor: string | null,
  ordered: string[],
): Toggled {
  const next = new Set(selected);
  const from = anchor ? ordered.indexOf(anchor) : -1;
  const to = ordered.indexOf(id);
  if (shift && from >= 0 && to >= 0) {
    const [lo, hi] = from < to ? [from, to] : [to, from];
    for (const rowId of ordered.slice(lo, hi + 1)) next.add(rowId);
    return { selected: next, anchor };
  }
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return { selected: next, anchor: id };
}

/** Whether every row shown is selected (and at least one is shown). */
export function allSelected(selected: Set<string>, ordered: string[]): boolean {
  return ordered.length > 0 && ordered.every((id) => selected.has(id));
}

export interface AssetSelection {
  selected: Set<string>;
  count: number;
  has: (id: string) => boolean;
  toggle: (id: string, shift: boolean, ordered: string[]) => void;
  /** Selects the rows shown, or clears them when they are all selected. */
  togglePage: (ordered: string[]) => void;
  replace: (ids: string[]) => void;
  clear: () => void;
}

/** useAssetSelection is the set of assets ticked on the Assets tab (#2507). */
export function useAssetSelection(): AssetSelection {
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const anchor = useRef<string | null>(null);

  const toggle = useCallback((id: string, shift: boolean, ordered: string[]) => {
    setSelected((cur) => {
      const out = applyToggle(cur, id, shift, anchor.current, ordered);
      anchor.current = out.anchor;
      return out.selected;
    });
  }, []);

  const togglePage = useCallback((ordered: string[]) => {
    setSelected((cur) => {
      const next = new Set(cur);
      if (allSelected(cur, ordered)) ordered.forEach((id) => next.delete(id));
      else ordered.forEach((id) => next.add(id));
      return next;
    });
  }, []);

  const replace = useCallback((ids: string[]) => {
    anchor.current = null;
    setSelected(new Set(ids));
  }, []);

  const clear = useCallback(() => replace([]), [replace]);

  return {
    selected,
    count: selected.size,
    has: (id) => selected.has(id),
    toggle,
    togglePage,
    replace,
    clear,
  };
}

/** The page size the matching ids are read in; the server caps a page at 100. */
const MATCH_PAGE = 100;

/**
 * matchingIds reads the ids of every asset the filters (and, when searching,
 * the query) cover, a page at a time, stopping at MAX_BULK_ASSETS: what
 * "Select all matching" selects. The server applies the filters over what
 * the caller can open, so nothing here widens the selection.
 */
export async function matchingIds(filters: AssetQueryFilters, query: string): Promise<string[]> {
  const ids: string[] = [];
  for (let offset = 0; ids.length < MAX_BULK_ASSETS; offset += MATCH_PAGE) {
    const sp = new URLSearchParams();
    assetFilterParams(filters, sp);
    sp.set("limit", String(MATCH_PAGE));
    sp.set("offset", String(offset));
    let rows: Asset[];
    let total: number;
    if (query) {
      sp.set("q", query);
      const page = await apiFetch<PaginatedResponse<ScoredAsset>>(`/assets/search?${sp.toString()}`);
      rows = (page.data ?? []).map((s) => s.asset);
      total = page.total;
    } else {
      const page = await apiFetch<PaginatedResponse<Asset>>(`/assets?${sp.toString()}`);
      rows = page.data ?? [];
      total = page.total;
    }
    ids.push(...rows.map((a) => a.id));
    if (rows.length === 0 || offset + rows.length >= total) break;
  }
  return ids.slice(0, MAX_BULK_ASSETS);
}
