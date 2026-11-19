import { useState } from "react";
import { AssetsTabs } from "@/components/AssetsTabs";
import { Button } from "@/components/ui/button";
import { useDeprecateAsset, useTrashAsset, useUnused, type UnusedItem } from "@/api/portal/hooks/library";
import { useAuthStore } from "@/stores/auth";
import { ActionError } from "./ActionError";
import { EveryoneSwitch } from "./EveryoneSwitch";
import { StatusPill } from "./StatusPill";
import { useIsMobile } from "@/hooks/useIsMobile";

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
  const isMobile = useIsMobile();
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
        <div className="flex w-full flex-wrap items-center gap-4 md:w-auto">
          {isMobile ? (
            <div role="group" aria-label="Not opened in" className="grid w-full grid-cols-3 rounded-lg bg-muted p-0.5">
              {PERIODS.map((p) => (
                <button
                  key={p}
                  type="button"
                  aria-pressed={days === p}
                  onClick={() => setDays(p)}
                  className={`min-h-10 rounded-md text-sm ${days === p ? "bg-background font-semibold shadow-sm" : "text-muted-foreground"}`}
                >
                  {p} days
                </button>
              ))}
            </div>
          ) : (
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
          )}
          {isAdmin && <EveryoneSwitch label="Everyone's" checked={everyone} onChange={setEveryone} />}
        </div>
      </div>

      {selected.size > 0 && <SelectionBar count={selected.size} busy={busy} onApply={apply} docked={isMobile} />}
      <ActionError error={deprecate.error ?? trash.error} />

      {!unused.isLoading && items.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          Empty: every asset you own was opened in the last {days} days.
        </p>
      ) : (
        <UnusedTable
          phone={isMobile}
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
  docked = false,
}: {
  count: number;
  busy: boolean;
  onApply: (action: "deprecate" | "delete") => void;
  /** On a phone the bar sits at the bottom of the screen (#2723). */
  docked?: boolean;
}) {
  if (docked) {
    return (
      <div className="fixed inset-x-3 bottom-5 z-40 flex items-center gap-2 rounded-2xl bg-foreground py-2 pr-2 pl-4 text-sm text-background shadow-lg">
        <b className="flex-1">{count} selected</b>
        <Button variant="outline" disabled={busy} className="h-11 bg-transparent text-background" onClick={() => onApply("deprecate")}>Deprecate</Button>
        <Button variant="destructive" disabled={busy} className="h-11" onClick={() => onApply("delete")}>Delete</Button>
      </div>
    );
  }
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
  phone = false,
  items,
  everyone,
  selected,
  onToggle,
  onToggleAll,
  allSelected,
  onNavigate,
}: {
  /** On a phone each asset is a row (#2723). */
  phone?: boolean;
  items: UnusedItem[];
  everyone: boolean;
  selected: Set<string>;
  onToggle: (id: string) => void;
  onToggleAll: () => void;
  allSelected: boolean;
  onNavigate: (path: string) => void;
}) {
  if (phone) {
    return (
      <ul className="flex flex-col pb-20" data-testid="unused-rows">
        {items.map((it) => (
          <li key={it.id} className="flex items-start gap-1 border-b py-2">
            <label className="flex size-11 shrink-0 items-center justify-center">
              <input type="checkbox" className="size-5" aria-label={`Select ${it.name}`} checked={selected.has(it.id)} onChange={() => onToggle(it.id)} />
            </label>
            <button type="button" className="flex min-w-0 flex-1 flex-col gap-1.5 pt-1 text-left" onClick={() => onNavigate(`/assets/${it.id}`)}>
              <span className="line-clamp-2 text-[15px] leading-snug font-medium">{it.name}</span>
              {it.status && (
                <span className="self-start">
                  <StatusPill status={it.status} />
                </span>
              )}
              <span className="truncate text-xs text-muted-foreground">
                {[everyone ? it.owner_email : "", it.last_opened ? `${it.opens} ${it.opens === 1 ? "open" : "opens"} · Last opened ${shortDate(it.last_opened)}` : `Never opened · Created ${shortDate(it.created_at)}`].filter(Boolean).join(" · ")}
              </span>
            </button>
          </li>
        ))}
      </ul>
    );
  }
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
