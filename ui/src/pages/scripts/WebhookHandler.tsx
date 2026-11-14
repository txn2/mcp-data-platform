import { useState } from "react";
import { useScriptEventSummary } from "@/api/portal/hooks/scripts";
import type { ScriptEventSummary } from "@/api/portal/hooks/scripts";
import { useSaveHandler, useSetHandlerEnabled, type HandlerDetail } from "@/api/portal/hooks/scriptHandlers";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { formatWhen } from "./runFormat";

// WebhookHandler is one of a script's webhook handlers (#2332, #2434): each
// event the source stores enqueues one run, with trigger event, reading the
// event as run.event. It also says how the script's event runs stand: how
// many wait, and how many events were skipped because as many runs as the
// deployment allows were already waiting.
export function WebhookHandler({ scriptId, handler }: { scriptId: string; handler: HandlerDetail }) {
  const setEnabled = useSetHandlerEnabled(handler.handler);
  const summary = useScriptEventSummary(scriptId, true);
  const source = String(handler.event_source ?? "");
  return (
    <div className="space-y-3 text-sm">
      <p>
        Each event <span className="font-mono">{source}</span> stores runs this script once, with the event as
        run.event.
      </p>
      <Button
        size="sm"
        variant="outline"
        disabled={setEnabled.isPending}
        onClick={() => setEnabled.mutate(!handler.enabled)}
      >
        {handler.enabled ? "Turn off" : "Turn on"}
      </Button>
      {setEnabled.error && (
        <Alert variant="destructive">
          <AlertDescription>{setEnabled.error.message}</AlertDescription>
        </Alert>
      )}
      {summary.data && <EventSummaryFacts summary={summary.data} />}
    </div>
  );
}

// AddWebhook adds a webhook handler: the source is chosen from the ones the
// caller may run the script for. An administrator may name any source; the
// owner the source of a form they own, which the server enforces.
export function AddWebhook({
  scriptId,
  sources,
  held,
  onDone,
}: {
  scriptId: string;
  sources: string[];
  held: string[];
  onDone: () => void;
}) {
  const choices = sources.filter((s) => !held.includes(s));
  const [source, setSource] = useState(choices[0] ?? "");
  const save = useSaveHandler(scriptId);
  return (
    <div className="space-y-3 text-sm">
      <p className="text-muted-foreground">
        Each event the source stores runs this script once, with the event as run.event. A form&apos;s source is
        named form-&lt;endpoint&gt;.
      </p>
      {choices.length === 0 ? (
        <p className="text-muted-foreground">
          {sources.length === 0
            ? "There is no webhook source you may run this script for. An administrator may name any source; you may name the source of a form you own."
            : "This script already runs for every source you may name."}
        </p>
      ) : (
        <div className="space-y-1">
          <Label htmlFor={`webhook-source-${scriptId}`}>Webhook source</Label>
          <select
            id={`webhook-source-${scriptId}`}
            className="h-9 w-full max-w-sm rounded-md border bg-background px-2 text-sm"
            value={source}
            onChange={(e) => setSource(e.target.value)}
          >
            {choices.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
      )}
      {save.error && (
        <Alert variant="destructive">
          <AlertDescription>{save.error.message}</AlertDescription>
        </Alert>
      )}
      <div className="flex gap-2">
        <Button
          size="sm"
          disabled={!source || save.isPending}
          onClick={() => save.mutate({ kind: "webhook", event_source: source }, { onSuccess: onDone })}
        >
          Add webhook
        </Button>
        <Button size="sm" variant="ghost" disabled={save.isPending} onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
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
        {summary.pending} event runs, at most {summary.max_queued} for each source
      </dd>
      <dt className="text-muted-foreground">Skipped</dt>
      <dd>{skipped}</dd>
      <dt className="text-muted-foreground">Last event run</dt>
      <dd>{summary.last_event_run ? formatWhen(summary.last_event_run) : "None yet"}</dd>
    </dl>
  );
}
