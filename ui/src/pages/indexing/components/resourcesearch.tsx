import { Loader2 } from "lucide-react";
import { type ResourceIndexState, useReclassify, useResourceIndex } from "@/api/admin/indexjobs";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/patterns/EmptyState";
import { formatBytes } from "@/lib/format";
import { relTime } from "./helpers";
import { Section } from "./panels";

// How managed resources are indexed for search (#2111): how many files search
// reads in full, on their details alone, or not at all, and the action that
// applies the deployment's resources.index rules again to every file no writer
// chose for.

function Count({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="space-y-0.5" title={hint}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="text-lg font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function lastRunText(state: ResourceIndexState): string | null {
  const run = state.last_run;
  if (!run) return null;
  if (!run.finished_at) return `Reclassifying, started ${relTime(run.started_at)}`;
  if (run.error) return `The last reclassification failed: ${run.error}`;
  const r = run.result;
  return r
    ? `Last reclassified ${relTime(run.finished_at)}: ${r.examined.toLocaleString()} files read, ` +
        `${r.changed.toLocaleString()} changed, ${r.cleared.toLocaleString()} had their kept text removed`
    : null;
}

export function ResourceSearchPanel({
  data,
  isError,
  onReclassify,
  reclassifying,
}: {
  data?: ResourceIndexState;
  isError: boolean;
  onReclassify: () => void;
  reclassifying: boolean;
}) {
  if (isError) return <EmptyState className="py-6">Could not read how resources are indexed.</EmptyState>;
  if (!data) return <EmptyState className="py-6">Counting resources…</EmptyState>;
  const c = data.counts;
  const running = !!data.last_run && !data.last_run.finished_at;
  const note = lastRunText(data);
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
        <Count label="Contents" value={c.full.toLocaleString()} hint="Search reads their details and contents" />
        <Count label="Details only" value={c.metadata.toLocaleString()} hint="Search reads their name, description, folder and tags" />
        <Count label="Not indexed" value={c.none.toLocaleString()} hint="Listed in their folders; search finds the folder" />
        <Count label="Chosen by writers" value={c.explicit.toLocaleString()} hint="The rules never change these" />
        <Count label="Text kept" value={formatBytes(c.content_bytes)} hint="Extracted from files for search" />
        <Count label="Folders" value={c.folders.toLocaleString()} hint="Folders search finds as one entry" />
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button size="sm" variant="outline" onClick={onReclassify} disabled={running || reclassifying}>
          {(running || reclassifying) && <Loader2 className="animate-spin" />}
          Reclassify
        </Button>
        <span className="text-xs text-muted-foreground">
          {note ?? "Applies resources.index again to every file no writer chose for, in batches."}
        </span>
      </div>
    </div>
  );
}

// ResourceSearchSection reads its own counts, so the page asks for them only
// on a deployment that indexes resources.
export function ResourceSearchSection() {
  const q = useResourceIndex();
  const reclassify = useReclassify();
  return (
    <Section title="Resource search" hint="how managed resources are indexed">
      <ResourceSearchPanel
        data={q.data}
        isError={q.isError}
        onReclassify={() => reclassify.mutate()}
        reclassifying={reclassify.isPending}
      />
    </Section>
  );
}
