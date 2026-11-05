import { useEffect, useState } from "react";

import {
  type APIChange,
  type APIChangeOp,
  type APISpecWatch,
  useAPISpecChanges,
  useAPISpecWatch,
  useDecideAPIChange,
  useSaveAPISpecWatch,
} from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { LabeledInput } from "./forms";

// The Updates section of a spec's dialog and its Changes list (#2483, #2484):
// how often the spec's upstream is checked, whether a changed spec waits for
// review, the upstream URL an uploaded or pasted spec is compared with, and
// what each check found.

/** INTERVALS are the schedules offered; the value is the Go duration sent. */
export const INTERVALS = [
  { label: "Off", value: "off" },
  { label: "Daily", value: "24h" },
  { label: "Weekly", value: "168h" },
  { label: "Monthly", value: "720h" },
] as const;

/** UpdatesDraft is what the section edits. interval is an INTERVALS value. */
export interface UpdatesDraft {
  interval: string;
  hold: boolean;
  upstreamURL: string;
}

export const EMPTY_UPDATES: UpdatesDraft = { interval: "off", hold: false, upstreamURL: "" };

/** draftOf reads a stored watch into the section. A stored interval that is
 * not one of the offered schedules is kept as its own duration, so saving the
 * dialog does not change it. */
export function draftOf(watch: APISpecWatch | undefined): UpdatesDraft {
  if (!watch) return EMPTY_UPDATES;
  return {
    interval: intervalValue(watch.refresh_interval),
    hold: watch.hold_changes,
    upstreamURL: watch.upstream_url ?? "",
  };
}

/** intervalValue turns a Go duration ("168h0m0s") into the schedule it is. */
export function intervalValue(duration: string): string {
  if (!duration) return "off";
  const hours = /^(\d+)h/.exec(duration);
  if (!hours) return duration;
  const offered = INTERVALS.find((i) => i.value === `${hours[1]}h`);
  return offered ? offered.value : duration;
}

/** watchInput is the draft as the watch route takes it. */
export function watchInput(draft: UpdatesDraft, isURL: boolean) {
  return {
    refresh_interval: draft.interval === "off" ? "" : draft.interval,
    hold_changes: isURL && draft.hold,
    upstream_url: isURL ? "" : draft.upstreamURL.trim(),
  };
}

/** sameDraft reports whether two drafts would store the same watch. */
function sameDraft(a: UpdatesDraft, b: UpdatesDraft, isURL: boolean): boolean {
  return JSON.stringify(watchInput(a, isURL)) === JSON.stringify(watchInput(b, isURL));
}

/** useSpecUpdates holds the Updates section's draft for a spec dialog, and
 * saves it after the spec itself: only when it changed, so a dialog saved for
 * its content alone leaves the watch as it was. */
export function useSpecUpdates(catalogID: string, specName: string, isEditing: boolean) {
  const { data: watch } = useAPISpecWatch(catalogID, specName, isEditing);
  const saveWatch = useSaveAPISpecWatch();
  const [draft, setDraft] = useState<UpdatesDraft>(EMPTY_UPDATES);
  useEffect(() => {
    if (watch) setDraft(draftOf(watch));
  }, [watch]);
  const save = async (name: string, isURL: boolean) => {
    if (sameDraft(draft, draftOf(watch), isURL)) return;
    await saveWatch.mutateAsync({ catalogID, specName: name, input: watchInput(draft, isURL) });
  };
  return { watch, draft, setDraft, save, saving: saveWatch.isPending };
}

function when(stamp?: string): string {
  return stamp ? new Date(stamp).toLocaleString() : "";
}

/** CheckStatus is when the spec was last and is next checked, and what the
 * last check failed on. */
function CheckStatus({ watch }: { watch: APISpecWatch }) {
  return (
    <>
      {(watch.last_checked_at || watch.next_check_at) && (
        <p className="text-xs text-muted-foreground">
          {watch.last_checked_at && <>Last checked {when(watch.last_checked_at)}. </>}
          {watch.next_check_at && <>Next check {when(watch.next_check_at)}.</>}
        </p>
      )}
      {watch.last_error && (
        <Alert variant="destructive">
          <AlertDescription>The last check failed: {watch.last_error}</AlertDescription>
        </Alert>
      )}
    </>
  );
}

export function SpecUpdatesFields({
  draft,
  onChange,
  isURL,
  watch,
}: {
  draft: UpdatesDraft;
  onChange: (next: UpdatesDraft) => void;
  /** isURL is a spec fetched from its URL; the others are uploaded or pasted. */
  isURL: boolean;
  watch?: APISpecWatch;
}) {
  const custom = !INTERVALS.some((i) => i.value === draft.interval);
  return (
    <section className="space-y-3 border-t pt-4" aria-label="Updates">
      <h4 className="text-sm font-medium">Updates</h4>
      <div className="space-y-1.5">
        <Label className="text-xs">Check for updates</Label>
        <Select value={draft.interval} onValueChange={(v) => onChange({ ...draft, interval: v })}>
          <SelectTrigger className="w-48" aria-label="Check for updates">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {INTERVALS.map((i) => (
              <SelectItem key={i.value} value={i.value}>
                {i.label}
              </SelectItem>
            ))}
            {custom && <SelectItem value={draft.interval}>{draft.interval}</SelectItem>}
          </SelectContent>
        </Select>
        <p className="text-xs text-muted-foreground">
          {isURL
            ? "How often the spec URL is read. A change is compared with the spec in use, and the report lists what was added, removed, changed and deprecated."
            : "How often the upstream URL below is read. The difference is reported; the spec you uploaded or pasted is never replaced."}
        </p>
      </div>

      {isURL ? (
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={draft.hold}
            onChange={(e) => onChange({ ...draft, hold: e.target.checked })}
          />
          <span>
            Hold changes for review
            <span className="block text-xs text-muted-foreground">
              A changed spec waits in Changes for Apply; until then connections keep the spec in use.
            </span>
          </span>
        </label>
      ) : (
        <LabeledInput
          label="Upstream URL"
          help="Where this spec is published. It is read and compared with the last copy read from it; the spec you uploaded or pasted stays as it is."
          value={draft.upstreamURL}
          onChange={(v) => onChange({ ...draft, upstreamURL: v })}
          placeholder="https://vendor.example.com/openapi.yaml"
          mono
        />
      )}

      {watch && <CheckStatus watch={watch} />}
    </section>
  );
}

/** STATUS_LABEL is each change status as the list names it. */
const STATUS_LABEL: Record<string, string> = {
  applied: "Applied",
  pending: "Waiting for review",
  discarded: "Discarded",
  reported: "Reported",
};

function OpLines({ title, ops }: { title: string; ops: APIChangeOp[] }) {
  if (ops.length === 0) return null;
  return (
    <div>
      <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{title}</p>
      <ul className="mt-1 space-y-0.5">
        {ops.map((op) => (
          <li key={op.id} className="text-xs">
            <code className="font-mono">{op.id}</code>
            {op.summary && <span className="text-muted-foreground"> ({op.summary})</span>}
            {op.used_by && op.used_by.length > 0 && (
              <span className="text-muted-foreground">
                {" "}
                used by {[...new Set(op.used_by.map((c) => `${c.kind} ${c.name}`))].join(", ")}
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function ChangeRow({
  change,
  isReadOnly,
  onDecide,
  deciding,
}: {
  change: APIChange;
  isReadOnly: boolean;
  onDecide: (decision: "apply" | "discard") => void;
  deciding: boolean;
}) {
  const [open, setOpen] = useState(change.status === "pending");
  const r = change.report;
  const changed = r.changed.map((c) => ({ ...c, summary: c.parts.join(", ") }));
  return (
    <li className="space-y-2 px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-muted-foreground">{when(change.created_at)}</span>
        <Badge variant={change.status === "pending" ? "default" : "secondary"}>
          {STATUS_LABEL[change.status] ?? change.status}
        </Badge>
        <button type="button" className="text-left text-sm underline-offset-2 hover:underline" onClick={() => setOpen(!open)}>
          {change.summary}
        </button>
        {change.status === "pending" && !isReadOnly && (
          <span className="ml-auto flex gap-1">
            <Button type="button" size="xs" onClick={() => onDecide("apply")} disabled={deciding}>
              Apply
            </Button>
            <Button type="button" size="xs" variant="outline" onClick={() => onDecide("discard")} disabled={deciding}>
              Discard
            </Button>
          </span>
        )}
      </div>
      {open && (
        <div className="space-y-2 rounded-md bg-muted/40 p-2">
          <OpLines title="Removed" ops={r.removed} />
          <OpLines title="Changed" ops={changed} />
          <OpLines title="Deprecated" ops={r.deprecated} />
          <OpLines title="Added" ops={r.added} />
          {r.truncated && <p className="text-xs text-muted-foreground">The lists were cut at 500 operations each.</p>}
        </div>
      )}
    </li>
  );
}

export function SpecChanges({
  catalogID,
  specName,
  isReadOnly,
}: {
  catalogID: string;
  specName: string;
  isReadOnly: boolean;
}) {
  const { data, isLoading } = useAPISpecChanges(catalogID, specName);
  const decide = useDecideAPIChange();
  const [error, setError] = useState<string | null>(null);
  // A held change replaced by a newer one says nothing the newer one does not.
  const changes = (data?.changes ?? []).filter((c) => c.status !== "superseded");
  return (
    <section className="space-y-2" aria-label="Changes">
      <h4 className="text-sm font-medium">Changes</h4>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading changes…</p>
      ) : changes.length === 0 ? (
        <p className="text-sm text-muted-foreground">No changes found yet.</p>
      ) : (
        <ul className="divide-y rounded-md border">
          {changes.map((c) => (
            <ChangeRow
              key={c.id}
              change={c}
              isReadOnly={isReadOnly}
              deciding={decide.isPending}
              onDecide={(decision) => {
                setError(null);
                decide.mutate(
                  { catalogID, specName, changeID: c.id, decision },
                  { onError: (e) => setError(e instanceof Error ? e.message : `${decision} failed`) },
                );
              }}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
