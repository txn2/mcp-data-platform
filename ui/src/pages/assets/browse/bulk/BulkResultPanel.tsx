import { REASON_HIDDEN, type BulkResult } from "@/api/portal/hooks/bulk";
import { Button } from "@/components/ui/button";
import { actionVerb } from "./BulkBar";

/**
 * The name a skipped asset is listed by: its own when the reader can open it,
 * and only its id, shortened, when the reason is that they cannot (or it does
 * not exist), so the panel says nothing about it the reader did not send.
 */
export function skippedLabel(id: string, reason: string, names: Map<string, string>): string {
  const name = names.get(id);
  if (reason !== REASON_HIDDEN && name) return name;
  return `An asset (id ${id.slice(0, 4)}…)`;
}

/**
 * BulkResultPanel reports what a bulk action did (#2507): how many assets it
 * was applied to, and each skipped asset with why.
 */
export function BulkResultPanel({
  result,
  names,
  onSelectSkipped,
  onDone,
}: {
  result: BulkResult;
  names: Map<string, string>;
  onSelectSkipped: (ids: string[]) => void;
  onDone: () => void;
}) {
  const skipped = result.skipped;
  return (
    <section
      role="status"
      aria-label="Bulk result"
      className="flex flex-col gap-2 rounded-xl border bg-card px-4 py-3 text-sm"
    >
      <b className="text-[15px] font-semibold">
        {actionVerb(result.action)} · Applied to {result.applied.length} · Skipped {skipped.length}
      </b>
      {skipped.length > 0 && (
        <ul className="flex flex-col gap-1.5">
          {skipped.map((s) => (
            <li key={s.asset_id} className="flex justify-between gap-2 rounded-md bg-muted/50 px-2.5 py-2">
              <span className={names.has(s.asset_id) && s.reason !== REASON_HIDDEN ? "" : "text-muted-foreground"}>
                {skippedLabel(s.asset_id, s.reason, names)}
              </span>
              <span className="text-right text-orange-800 dark:text-orange-300">{s.reason}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="text-xs text-muted-foreground">
        Nothing changed on a skipped asset. Delete sends assets to Trash, where they can be restored until purged.
      </p>
      <div className="flex flex-wrap gap-2">
        {skipped.length > 0 && (
          <Button variant="outline" size="sm" onClick={() => onSelectSkipped(skipped.map((s) => s.asset_id))}>
            Select the skipped {skipped.length}
          </Button>
        )}
        <Button variant="outline" size="sm" onClick={onDone}>
          Done
        </Button>
      </div>
    </section>
  );
}
