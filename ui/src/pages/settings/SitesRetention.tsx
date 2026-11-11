import { useSiteRetention, type SiteSweep } from "@/api/admin/hooks";
import { formatBytes } from "@/pages/scripts/runFormat";
import { ConfigField } from "./connections/fields";

// Releases kept (#2602), in Admin > Settings > Sites: how many releases each
// environment keeps unless its owner sets its own number, what the daily
// cleanup does with the rest, and what the last cleanup removed or, in Report
// only, would remove.

const dateTime = new Intl.DateTimeFormat("en-US", { year: "numeric", month: "2-digit", day: "2-digit", hour: "numeric", minute: "2-digit" });

/** keepValid is a releases-kept value the server takes: a whole number, 0 for every release. */
export function keepValid(keep: string): boolean {
  return /^\d+$/.test(keep.trim());
}

export function ReleasesFields({ keep, mode, onKeep, onMode }: { keep: string; mode: "delete" | "report"; onKeep: (v: string) => void; onMode: (m: "delete" | "report") => void }) {
  const n = Number(keep);
  const keepHelp = !keepValid(keep)
    ? "Enter a whole number. 0 keeps every release."
    : n === 0
      ? "Every environment keeps every release it served, unless its owner sets its own number."
      : `Each environment keeps the last ${keep.trim()} releases it served, unless its owner sets its own number. 0 keeps every release.`;
  return (
    <div className="space-y-3 border-t pt-4" data-testid="sites-releases">
      <p className="text-sm font-medium">Releases</p>
      <ConfigField label="Releases kept per environment" type="number" value={keep} onChange={onKeep} help={keepHelp} />
      <fieldset className="space-y-2">
        <legend className="mb-1 text-sm font-medium">Cleanup</legend>
        <Mode value="delete" mode={mode} onMode={onMode} title="Delete" help="Once a day, remove releases no environment keeps, with their files, and files no release uses." />
        <Mode value="report" mode={mode} onMode={onMode} title="Report only" help="Work out what would be removed and show it here. Nothing is deleted." />
      </fieldset>
      <LastCleanup />
    </div>
  );
}

function Mode({ value, mode, onMode, title, help }: { value: "delete" | "report"; mode: string; onMode: (m: "delete" | "report") => void; title: string; help: string }) {
  return (
    <label className={`flex cursor-pointer items-start gap-2 rounded border p-3 ${mode === value ? "border-primary bg-primary/5" : ""}`}>
      <input type="radio" name="cleanup" aria-label={title} className="mt-1" checked={mode === value} onChange={() => onMode(value)} />
      <span>
        <span className="block font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{help}</span>
      </span>
    </label>
  );
}

function plural(n: number, one: string, many: string): string {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`;
}

/** The newest cleanup of every site, not one an administrator ran for one site. */
function lastFull(sweeps: SiteSweep[]): SiteSweep | undefined {
  return sweeps.find((w) => !w.scope) ?? sweeps[0];
}

function LastCleanup() {
  const { data } = useSiteRetention();
  const w = data ? lastFull(data.sweeps) : undefined;
  if (!data) return null;
  if (!w) return <p className="text-xs text-muted-foreground">No cleanup has run yet. The first runs within a day.</p>;
  const at = dateTime.format(new Date(w.finished_at)).replace(",", " at");
  const what = `${plural(w.releases, "release", "releases")} from ${plural(w.sites_swept, "site", "sites")} and ${plural(w.files + w.orphans, "file", "files")} no release used`;
  const freed = formatBytes(w.bytes + w.orphan_bytes);
  return (
    <div className="rounded border p-3 text-sm" data-testid="last-cleanup">
      {w.mode === "report" ? (
        <>
          <p className="font-medium">Last cleanup, report only: {at}</p>
          <p className="text-xs text-muted-foreground">
            Would remove {what}, and free {freed}. Nothing was deleted.
          </p>
        </>
      ) : (
        <>
          <p className="font-medium">Last cleanup: {at}</p>
          <p className="text-xs text-muted-foreground">
            Removed {what}. Freed {freed}.
          </p>
        </>
      )}
    </div>
  );
}
