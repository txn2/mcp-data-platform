import { useState } from "react";
import { AssetsTabs } from "@/components/AssetsTabs";
import { Button } from "@/components/ui/button";
import { useDeprecateAsset, useTrashAsset, useUnused, type UnusedItem } from "@/api/portal/hooks/library";
import { useAuthStore } from "@/stores/auth";
import { ActionError } from "./ActionError";
import { EveryoneSwitch } from "./EveryoneSwitch";
import { StatusPill } from "./StatusPill";

interface Props {
  onNavigate: (path: string) => void;
}

const PERIODS = [30, 90, 180] as const;
const EMPTY_UNUSED = { items: [] as UnusedItem[], total: 0, days: 90 };

function shortDate(iso?: string): string {
  return iso ? new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" }) : "Never";
}

/**
 * UnusedPage lists the reader's assets nobody has opened in a period (#2509),
 * with counts only, never who opened. Deprecate and Delete act on a selection;
 * Delete sends to the trash.
 */
export function UnusedPage({ onNavigate }: Props) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [days, setDays] = useState<number>(90);
  const [everyone, setEveryone] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const unused = useUnused(days, everyone);
  const deprecate = useDeprecateAsset();
  const trash = useTrashAsset();
  const { items } = unused.data ?? EMPTY_UNUSED;
  const busy = deprecate.isPending || trash.isPending;

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const allSelected = items.length > 0 && items.every((it) => selected.has(it.id));

  async function apply(action: "deprecate" | "delete") {
    for (const id of selected) {
      if (action === "deprecate") await deprecate.mutateAsync({ id, note: "Not opened in the last " + days + " days" });
      else await trash.mutateAsync(id);
    }
    setSelected(new Set());
  }

  return (
    <div className="space-y-4">
      <AssetsTabs active="unused" onNavigate={onNavigate} />
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="space-y-1">
          <h1 className="text-xl font-semibold">Unused</h1>
          <p className="text-sm text-muted-foreground">
            Your assets nobody has opened in the period. Low use can also mean an asset is hard to find.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-4">
          <label className="flex items-center gap-2 text-sm">
            Not opened in
            <select
              value={days}
              onChange={(e) => setDays(Number(e.target.value))}
              className="h-9 rounded-md border bg-background px-2 text-sm"
            >
              {PERIODS.map((p) => (
                <option key={p} value={p}>{p} days</option>
              ))}
            </select>
          </label>
          {isAdmin && <EveryoneSwitch label="Everyone's" checked={everyone} onChange={setEveryone} />}
        </div>
      </div>

      {selected.size > 0 && <SelectionBar count={selected.size} busy={busy} onApply={apply} />}
      <ActionError error={deprecate.error ?? trash.error} />

      {!unused.isLoading && items.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          Empty: every asset you own was opened in the last {days} days.
        </p>
      ) : (
        <UnusedTable
          items={items}
          everyone={everyone}
          selected={selected}
          onToggle={toggle}
          onToggleAll={() => setSelected(allSelected ? new Set() : new Set(items.map((it) => it.id)))}
          allSelected={allSelected}
          onNavigate={onNavigate}
        />
      )}
      <p className="text-xs text-muted-foreground">
        Counts only: never who opened. Opens are counted from the release that started recording them, so an older
        asset reads "Never" until it is opened. Opening an asset takes it off the list.
      </p>
    </div>
  );
}

function SelectionBar({
  count,
  busy,
  onApply,
}: {
  count: number;
  busy: boolean;
  onApply: (action: "deprecate" | "delete") => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-primary/30 bg-primary/5 px-3 py-2 text-sm">
      <b>{count} selected</b>
      <Button variant="outline" size="sm" disabled={busy} onClick={() => onApply("deprecate")}>Deprecate</Button>
      <Button variant="outline" size="sm" disabled={busy} className="text-destructive" onClick={() => onApply("delete")}>
        Delete
      </Button>
      <span className="text-muted-foreground">Delete sends them to Trash.</span>
    </div>
  );
}

function UnusedTable({
  items,
  everyone,
  selected,
  onToggle,
  onToggleAll,
  allSelected,
  onNavigate,
}: {
  items: UnusedItem[];
  everyone: boolean;
  selected: Set<string>;
  onToggle: (id: string) => void;
  onToggleAll: () => void;
  allSelected: boolean;
  onNavigate: (path: string) => void;
}) {
  return (
        <div className="overflow-x-auto rounded-xl border bg-card">
          <table className="w-full min-w-[640px] text-sm">
            <thead>
              <tr className="border-b text-left text-xs text-muted-foreground">
                <th className="w-9 px-3 py-2">
                  <input
                    type="checkbox"
                    aria-label="Select all"
                    checked={allSelected}
                    onChange={onToggleAll}
                  />
                </th>
                <th className="px-3 py-2 font-semibold">Name</th>
                {everyone && <th className="px-3 py-2 font-semibold">Owner</th>}
                <th className="px-3 py-2 font-semibold">Status</th>
                <th className="px-3 py-2 text-right font-semibold">Opens, all time</th>
                <th className="px-3 py-2 font-semibold">Last opened</th>
                <th className="px-3 py-2 font-semibold">Created</th>
              </tr>
            </thead>
            <tbody>
              {items.map((it) => (
                <tr key={it.id} className="border-b last:border-b-0">
                  <td className="px-3 py-2">
                    <input
                      type="checkbox"
                      aria-label={`Select ${it.name}`}
                      checked={selected.has(it.id)}
                      onChange={() => onToggle(it.id)}
                    />
                  </td>
                  <td className="px-3 py-2">
                    <button type="button" className="text-left text-primary hover:underline" onClick={() => onNavigate(`/assets/${it.id}`)}>
                      {it.name}
                    </button>
                  </td>
                  {everyone && <td className="px-3 py-2 text-muted-foreground">{it.owner_email}</td>}
                  <td className="px-3 py-2"><StatusPill status={it.status} /></td>
                  <td className="px-3 py-2 text-right">{it.opens}</td>
                  <td className="px-3 py-2 text-muted-foreground">{shortDate(it.last_opened)}</td>
                  <td className="px-3 py-2 text-muted-foreground">{shortDate(it.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
  );
}
