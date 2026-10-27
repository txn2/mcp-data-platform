import { useState } from "react";
import { useScriptEventSummary, useSetScriptEventSource } from "@/api/portal/hooks/scripts";
import type { ScriptContract, ScriptEventSummary } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatWhen } from "./runFormat";

// ScriptEventsCard is where the owner says which webhook source a script runs
// for (#2332), beside its schedule: each event the source stores enqueues one
// run, with trigger event, reading the event as run.event. The card also says
// how the script's event runs stand: how many wait, and how many events were
// skipped because as many runs as the deployment allows were already waiting.
// An administrator may name any source; the owner the source of a form they
// own, which the server enforces and this card reports.
export function ScriptEventsCard({ scriptId, contract }: { scriptId: string; contract: ScriptContract }) {
  const current = contract.event_source ?? "";
  const [source, setSource] = useState(current);
  const save = useSetScriptEventSource(scriptId);
  const summary = useScriptEventSummary(scriptId, current !== "");
  const changed = source.trim() !== current;

  return (
    <SectionCard
      title="Events"
      collapsible
      defaultOpen={current !== ""}
      summary={current ? `Runs for ${current}` : "Runs for no webhook source"}
    >
      <div className="space-y-3 text-sm">
        <p className="text-muted-foreground">
          Each event the named webhook source stores runs this script once, with the event as run.event. A
          form&apos;s source is named form-&lt;endpoint&gt;.
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <div className="space-y-1">
            <Label htmlFor={`event-source-${scriptId}`}>Webhook source</Label>
            <Input
              id={`event-source-${scriptId}`}
              className="w-64"
              value={source}
              placeholder="form-contact"
              onChange={(e) => setSource(e.target.value)}
            />
          </div>
          <Button size="sm" disabled={!changed || save.isPending} onClick={() => save.mutate(source.trim())}>
            Save
          </Button>
          {current && (
            <Button size="sm" variant="outline" disabled={save.isPending} onClick={() => { setSource(""); save.mutate(""); }}>
              Stop running for events
            </Button>
          )}
        </div>
        {save.error && (
          <Alert variant="destructive">
            <AlertDescription>{save.error.message}</AlertDescription>
          </Alert>
        )}
        {save.data && <p role="status" className="text-xs text-muted-foreground">{save.data.message}</p>}
        {current && summary.data && <EventSummaryFacts summary={summary.data} />}
      </div>
    </SectionCard>
  );
}

// EventSummaryFacts is how a script's event runs stand: how many wait, how
// many events were skipped, and the last event run.
function EventSummaryFacts({ summary }: { summary: ScriptEventSummary }) {
  const skipped =
    summary.skipped_since === 0
      ? `None in the last ${summary.skipped_window_days} days`
      : `${summary.skipped_since} in the last ${summary.skipped_window_days} days, the last ${formatWhen(summary.last_skipped_at)}; each is a run marked Skipped (backlog)`;
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-1 sm:grid-cols-[12rem_1fr]">
      <dt className="text-muted-foreground">Waiting</dt>
      <dd>
        {summary.pending} of at most {summary.max_queued} event runs
      </dd>
      <dt className="text-muted-foreground">Skipped</dt>
      <dd>{skipped}</dd>
      <dt className="text-muted-foreground">Last event run</dt>
      <dd>{summary.last_event_run ? formatWhen(summary.last_event_run) : "None yet"}</dd>
    </dl>
  );
}
