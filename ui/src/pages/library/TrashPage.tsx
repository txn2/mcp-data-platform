import { useState } from "react";
import { AssetsTabs } from "@/components/AssetsTabs";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { usePurge, useRestore, useTrash, type TrashItem } from "@/api/portal/hooks/library";
import { useAuthStore } from "@/stores/auth";
import { ActionError } from "./ActionError";
import { itemPath } from "./HomePage";
import { EveryoneSwitch } from "./EveryoneSwitch";

interface Props {
  onNavigate: (path: string) => void;
}

const DAY = 24 * 60 * 60 * 1000;
const EMPTY_TRASH = { items: [] as TrashItem[], total: 0, retention_days: 30 };

function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** purgeLabel is the purge date, flagged when it is within a week. */
function purgeLabel(iso: string): { text: string; soon: boolean } {
  const days = Math.ceil((new Date(iso).getTime() - Date.now()) / DAY);
  if (days <= 7) return { text: `${shortDate(iso)} (in ${Math.max(days, 0)} days)`, soon: true };
  return { text: shortDate(iso), soon: false };
}

function kindLabel(it: TrashItem): string {
  return it.kind === "collection" ? "Collection" : `Asset${it.content_type ? ` · ${it.content_type}` : ""}`;
}

/**
 * TrashPage lists the reader's deleted assets and collections with the day
 * each is purged (#2509). Restoring brings an item back with its versions,
 * shares and collection memberships; Delete permanently removes it now.
 * Administrators can switch to everyone's trash.
 */
export function TrashPage({ onNavigate }: Props) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [everyone, setEveryone] = useState(false);
  const trash = useTrash(everyone);
  const restore = useRestore();
  const purge = usePurge();
  const [confirming, setConfirming] = useState<TrashItem | null>(null);
  const [restored, setRestored] = useState<TrashItem | null>(null);
  const { items, retention_days: days } = trash.data ?? EMPTY_TRASH;

  const doRestore = (it: TrashItem) =>
    restore.mutate({ kind: it.kind, id: it.id }, { onSuccess: () => setRestored(it) });

  return (
    <div className="space-y-4">
      <AssetsTabs active="trash" onNavigate={onNavigate} />
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="space-y-1">
          <h1 className="text-xl font-semibold">Trash</h1>
          <p className="text-sm text-muted-foreground">
            Assets and collections you deleted. Each is purged for good on the date shown ({days} days after
            deletion on this deployment). Restoring brings back its shares and collection memberships.
          </p>
        </div>
        {isAdmin && <EveryoneSwitch label="Everyone's trash" checked={everyone} onChange={setEveryone} />}
      </div>

      <RestoredNotice item={restored} onNavigate={onNavigate} />
      <ActionError error={restore.error ?? purge.error} />

      {!trash.isLoading && items.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          Trash is empty. Deleted assets and collections stay here for {days} days before they are purged.
        </p>
      ) : (
        <TrashList items={items} everyone={everyone} onRestore={doRestore} onPurge={setConfirming} />
      )}
      {everyone && (
        <p className="text-xs text-muted-foreground">
          The assets in a deleted collection stay in Assets; only the collection is in Trash.
        </p>
      )}

      <PurgeDialog
        item={confirming}
        pending={purge.isPending}
        onCancel={() => setConfirming(null)}
        onConfirm={(it) => purge.mutate({ kind: it.kind, id: it.id }, { onSettled: () => setConfirming(null) })}
      />
    </div>
  );
}

function RowActions({
  item,
  onRestore,
  onPurge,
  wide = false,
}: {
  item: TrashItem;
  onRestore: (it: TrashItem) => void;
  onPurge: (it: TrashItem) => void;
  wide?: boolean;
}) {
  const cls = wide ? "h-11 flex-1" : "";
  return (
    <div className={wide ? "flex gap-2" : "inline-flex gap-2"}>
      <Button variant="outline" size="sm" className={cls} onClick={() => onRestore(item)}>
        Restore
      </Button>
      <Button variant="outline" size="sm" className={`${cls} text-destructive`} onClick={() => onPurge(item)}>
        Delete permanently
      </Button>
    </div>
  );
}

function RestoredNotice({ item, onNavigate }: { item: TrashItem | null; onNavigate: (path: string) => void }) {
  if (!item) return null;
  return (
    <div role="status" className="rounded-lg border border-emerald-300 bg-emerald-50 px-3 py-2 text-sm text-emerald-900 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-100">
      "{item.name}" is back in {item.kind === "collection" ? "Collections" : "Assets"}.{" "}
      <button type="button" className="font-semibold underline" onClick={() => onNavigate(itemPath(item))}>
        Open it
      </button>
    </div>
  );
}


/** The trash as a table on a wide screen and one card per item on a phone. */
function TrashList({
  items,
  everyone,
  onRestore,
  onPurge,
}: {
  items: TrashItem[];
  everyone: boolean;
  onRestore: (it: TrashItem) => void;
  onPurge: (it: TrashItem) => void;
}) {
  return (
    <>
      <div className="hidden overflow-x-auto rounded-xl border bg-card md:block">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-3 py-2 font-semibold">Name</th>
              <th className="px-3 py-2 font-semibold">Kind</th>
              {everyone && <th className="px-3 py-2 font-semibold">Owner</th>}
              <th className="px-3 py-2 font-semibold">Deleted</th>
              <th className="px-3 py-2 font-semibold">Purged on</th>
              <th className="px-3 py-2" />
            </tr>
          </thead>
          <tbody>
            {items.map((it) => (
              <TrashRow key={`${it.kind}:${it.id}`} item={it} everyone={everyone} onRestore={onRestore} onPurge={onPurge} />
            ))}
          </tbody>
        </table>
      </div>
      {/* Below 768px each item is a card (#2723): the purge date on a line of
          its own, flagged when it is within a week. */}
      <ul className="flex flex-col gap-2 md:hidden" data-testid="trash-cards">
        {items.map((it) => {
          const purge = purgeLabel(it.purge_at);
          return (
            <li key={`${it.kind}:${it.id}`} className="flex flex-col gap-1.5 rounded-xl border bg-card p-3">
              <b className="text-[15px] leading-snug">{it.name}</b>
              <span className="text-xs text-muted-foreground">
                {[kindLabel(it), everyone ? it.owner_email : "", `deleted ${shortDate(it.deleted_at)}`].filter(Boolean).join(" · ")}
              </span>
              <span className={purge.soon ? "text-xs font-semibold text-destructive" : "text-xs text-muted-foreground"}>
                Purged on {purge.text}
              </span>
              <RowActions item={it} onRestore={onRestore} onPurge={onPurge} wide />
            </li>
          );
        })}
      </ul>
    </>
  );
}

function TrashRow({
  item,
  everyone,
  onRestore,
  onPurge,
}: {
  item: TrashItem;
  everyone: boolean;
  onRestore: (it: TrashItem) => void;
  onPurge: (it: TrashItem) => void;
}) {
  const purgeOn = purgeLabel(item.purge_at);
  return (
    <tr className="border-b last:border-b-0">
      <td className="px-3 py-2">{item.name}</td>
      <td className="px-3 py-2 text-muted-foreground">{kindLabel(item)}</td>
      {everyone && <td className="px-3 py-2 text-muted-foreground">{item.owner_email}</td>}
      <td className="px-3 py-2 text-muted-foreground">{shortDate(item.deleted_at)}</td>
      <td className={purgeOn.soon ? "px-3 py-2 font-medium text-amber-700 dark:text-amber-400" : "px-3 py-2"}>
        {purgeOn.text}
      </td>
      <td className="whitespace-nowrap px-3 py-2 text-right">
        <RowActions item={item} onRestore={onRestore} onPurge={onPurge} />
      </td>
    </tr>
  );
}

function PurgeDialog({
  item,
  pending,
  onCancel,
  onConfirm,
}: {
  item: TrashItem | null;
  pending: boolean;
  onCancel: () => void;
  onConfirm: (it: TrashItem) => void;
}) {
  return (
    <Dialog open={!!item} onOpenChange={(open) => !open && onCancel()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete "{item?.name}" permanently?</DialogTitle>
          <DialogDescription>
            Its versions, comments and shares are removed and it cannot be restored. Assets that link to it will open
            "not found".
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel}>Cancel</Button>
          <Button variant="destructive" disabled={pending} onClick={() => item && onConfirm(item)}>
            Delete permanently
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
