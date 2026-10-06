import type { FlowTimedCall, ScriptFlow, ScriptRunFlow } from "@/api/portal/hooks/scriptFlow";
import { Button } from "@/components/ui/button";
import { formatDuration, type Selection } from "./flowModel";
import { Row, Rows } from "./panelParts";
import { lineOf } from "./timelineModel";

// CallDetail is one call a run made, selected on the Timeline or from its
// card (#1982): what it was sent, when, for how long, what it answered, and
// the line and card it came from.
export function CallDetail({
  graph,
  run,
  index,
  onSelect,
  onShowLines,
}: {
  graph: ScriptFlow;
  run: ScriptRunFlow;
  index: number;
  onSelect: (s: Selection) => void;
  onShowLines?: (lines: number[]) => void;
}) {
  const call = run.timeline[index];
  if (!call) return null;
  const card = call.node ? graph.nodes.find((n) => n.id === call.node) : undefined;
  const line = callLine(call);
  return (
    <div className="space-y-3" data-testid="flow-call-detail">
      <div>
        <div className="text-[11px] font-bold tracking-wider text-muted-foreground uppercase">
          Call {index + 1} of {run.timeline.length}
        </div>
        <h4 className="font-mono font-medium break-words">{call.tool}</h4>
      </div>
      <Rows>
        <Row label="Started">{formatDuration(call.start_ms)} into the run</Row>
        <Row label="Took">{formatDuration(call.duration_ms)}</Row>
        <Row label="Answered">{call.response_chars.toLocaleString()} characters</Row>
        <Row label="Outcome">
          {call.success ? "succeeded" : <span className="text-destructive">failed: {call.error || "no message"}</span>}
        </Row>
        {line > 0 && <Row label="Line">{line}</Row>}
        {card && (
          <Row label="Card">
            <button
              type="button"
              className="text-left text-primary underline-offset-2 hover:underline"
              onClick={() => onSelect({ kind: "node", id: card.id })}
            >
              {card.title}
            </button>
          </Row>
        )}
      </Rows>
      <Placement run={run} call={call} />
      <Arguments call={call} />
      {line > 0 && onShowLines && (
        <Button type="button" variant="outline" size="xs" onClick={() => onShowLines([line])}>
          Show in Source
        </Button>
      )}
    </div>
  );
}

// callLine is the line of the script that made a call: the innermost frame of
// its call site, or 0 when the call carries none.
export function callLine(call: FlowTimedCall): number {
  const site = call.call_site;
  return site && site.length > 0 ? lineOf(site[site.length - 1]!) : 0;
}

// Placement says why a call is tied to no line or card, where it is not.
function Placement({ run, call }: { run: ScriptRunFlow; call: FlowTimedCall }) {
  if (callLine(call) > 0) {
    return call.node ? null : (
      <p className="text-xs text-muted-foreground">
        No card made this call: the source computes which tool it calls.
      </p>
    );
  }
  return (
    <p className="text-xs text-muted-foreground" data-testid="flow-call-unplaced">
      {run.unplaced
        ? "This run was made before the platform recorded which line made each call, so this call can't be tied to a line or a card."
        : "This call was recorded without the line that made it, so it can't be tied to a line or a card."}
    </p>
  );
}

// Arguments is what the call was sent, laid out when it reads as JSON.
function Arguments({ call }: { call: FlowTimedCall }) {
  if (!call.arguments) {
    return <p className="text-xs text-muted-foreground">No arguments were recorded for this call.</p>;
  }
  return (
    <div className="space-y-1">
      <div className="text-xs text-muted-foreground">
        Arguments{call.arguments_truncated ? ", cut at 2 KB" : ""}
      </div>
      <pre className="max-h-64 overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-[11px] leading-relaxed break-words whitespace-pre-wrap">
        {pretty(call.arguments, !!call.arguments_truncated)}
      </pre>
    </div>
  );
}

// pretty indents arguments that parse as JSON; a cut copy does not parse and
// is shown as recorded.
function pretty(text: string, truncated: boolean): string {
  if (truncated) return `${text}…`;
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
