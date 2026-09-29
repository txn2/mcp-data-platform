import type { ScriptRunFlow } from "@/api/portal/hooks/scriptFlow";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { callGroupText, excerpt, formatDuration, groupCalls } from "./flowModel";
import type { PanelProps } from "./FlowSidePanel";
import { Excerpt, Row, Rows, Swatch } from "./panelParts";

// The side panel's reading of the Structure view (#1972): how to read it, a
// selected decision, exit or box, and the calls a drawn run made that no card
// made.

type Props = PanelProps;

// HowToReadStructure is the Structure view's explainer (#1972).
export function HowToReadStructure() {
  return (
    <div className="space-y-3">
      <h4 className="font-medium">How to read this</h4>
      <ul className="list-disc space-y-1.5 pl-4 text-xs text-muted-foreground">
        <li>
          <b className="text-foreground">Top to bottom</b> is the order the script runs: from{" "}
          <b className="text-foreground">Start</b> to <b className="text-foreground">End</b>. An arrow
          means "runs next".
        </li>
        <li>
          <b className="text-foreground">Each card is one call</b> the script makes on the platform. The
          colored bar says what it does: <Swatch role="reads" />
          reads <Swatch role="writes" />
          writes <Swatch role="output" />
          output.
        </li>
        <li>
          <b className="text-foreground">If</b> is a decision. The arrows out of it are its{" "}
          <b className="text-foreground">yes</b> and <b className="text-foreground">no</b> arms, which
          meet again where the script goes on.
        </li>
        <li>
          <b className="text-foreground">A dashed box</b> repeats: the loop's header is its heading, and
          the cards inside run once per pass.
        </li>
        <li>
          <b className="text-foreground">A grey box</b> is a function in the script, drawn where it is
          called. Its arrow folds it into one card and opens it again.
        </li>
        <li>
          <b className="text-foreground">Stops</b> is a <code className="font-mono">fail()</code>: the run
          ends there with that message. <b className="text-foreground">Returns early</b> ends it without
          running what comes after.
        </li>
        <li>
          The <b className="text-foreground">Calls</b> view draws the same cards by which result feeds
          which. Selecting a card here lights the cards it takes data from and feeds.
        </li>
      </ul>
      <p className="text-xs text-muted-foreground">
        Select a card for detail; double-click it to open its lines in Source.
      </p>
    </div>
  );
}

// KIND_HEADING is what a selected Structure node is called over its label.
const KIND_HEADING: Record<string, string> = {
  start: "Start",
  end: "End",
  stop: "Stops",
  return: "Returns early",
  if: "Decision",
  step: "Call",
};

// KIND_NOTE is what a stop and a decision mean, said beside them.
const KIND_NOTE: Record<string, [string, string]> = {
  stop: ["Ends", "the run fails here with this message"],
  if: ["Arms", "yes and no, which meet again after it"],
};

// StructDetail is a selected decision, exit or folded function (#1972).
export function StructDetail(props: Props & { id: string }) {
  const { graph, source, onShowLines, run, id } = props;
  // A folded function is selected as a node and read as its box.
  if (graph.structure.boxes.some((x) => x.id === id)) return <BoxDetail {...props} />;
  const n = graph.structure.nodes.find((x) => x.id === id);
  if (!n) return null;
  const note = KIND_NOTE[n.kind];
  return (
    <div className="space-y-3" data-testid="flow-struct-detail">
      <Heading kind={KIND_HEADING[n.kind] ?? n.kind} title={n.label ?? KIND_HEADING[n.kind] ?? n.kind} />
      <Rows>
        {note && <Row label={note[0]}>{note[1]}</Row>}
        <FailedRow run={run} id={n.id} />
        {n.line > 0 && <Row label="Line">{n.line}</Row>}
      </Rows>
      {n.line > 0 && <Excerpt text={excerpt(source, n.line, 1, 3)} />}
      {n.line > 0 && <ShowInSource onShowLines={onShowLines} line={n.line} />}
    </div>
  );
}

// BoxDetail is a selected loop or function box (#1972).
export function BoxDetail({ graph, source, onShowLines, run, id }: Props & { id: string }) {
  const b = graph.structure.boxes.find((x) => x.id === id);
  if (!b) return null;
  const fn = b.kind === "function" ? graph.structure.functions.find((f) => b.label.startsWith(`${f.name}(`)) : undefined;
  const line = fn?.line ?? b.line;
  return (
    <div className="space-y-3" data-testid="flow-box-detail">
      <Heading kind={b.kind === "loop" ? "Repeats" : "Function"} title={b.label} caption={b.caption} />
      <Rows>
        {fn && <Row label="Defined">line {fn.line}</Row>}
        <Row label={b.kind === "loop" ? "Line" : "Called"}>line {b.line}</Row>
        <FailedRow run={run} id={b.id} />
      </Rows>
      <Excerpt text={excerpt(source, line, 0, 6)} />
      <ShowInSource onShowLines={onShowLines} line={line} />
    </div>
  );
}

// Heading is what a selected node or box is, and its name.
function Heading({ kind, title, caption }: { kind: string; title: string; caption?: string }) {
  return (
    <div>
      <div className="text-[11px] font-bold tracking-wider text-muted-foreground uppercase">{kind}</div>
      <h4 className="font-mono font-medium break-words">{title}</h4>
      {caption && <p className="mt-2 text-xs">{caption}</p>}
    </div>
  );
}

// FailedRow says the drawn run failed here, with its message.
function FailedRow({ run, id }: { run?: ScriptRunFlow; id: string }) {
  if (run?.structure_failed !== id) return null;
  return <Row label="This run">{lastLine(run.error) || "failed here"}</Row>;
}

// ShowInSource opens a line on the Source tab, where there is one.
function ShowInSource({ onShowLines, line }: { onShowLines?: (lines: number[]) => void; line: number }) {
  if (!onShowLines) return null;
  return (
    <Button type="button" variant="outline" size="xs" onClick={() => onShowLines([line])}>
      Show in Source
    </Button>
  );
}

// lastLine is a run error's message: its last non-empty line.
function lastLine(text?: string): string {
  const lines = (text ?? "").trim().split("\n");
  return lines[lines.length - 1]?.trim() ?? "";
}

// OtherCalls is the calls no card made (#1972), one row per tool and outcome
// with failures first; each row opens to its calls.
export function OtherCalls({ run }: { run: ScriptRunFlow }) {
  const groups = groupCalls(run.other_calls);
  return (
    <div className="space-y-1.5 text-xs" data-testid="flow-other-calls">
      <p className="text-muted-foreground">
        {run.unplaced
          ? "Calls this run made, which it recorded without the line each came from:"
          : "Calls no card made, because the source computes which tool they call:"}
      </p>
      <ul className="space-y-1">
        {groups.map((g) => (
          <li key={g.tool}>
            <details>
              <summary className={cn("cursor-pointer font-mono break-words", g.failed > 0 && "text-destructive")}>
                {callGroupText(g)}
              </summary>
              <ul className="mt-1 max-h-48 space-y-0.5 overflow-auto pl-3 font-mono text-[11px] text-muted-foreground">
                {g.calls.map((c, i) => (
                  <li key={i}>
                    {formatDuration(c.duration_ms)}
                    {c.success ? "" : ` · ${c.error || "failed"}`}
                  </li>
                ))}
              </ul>
            </details>
          </li>
        ))}
      </ul>
    </div>
  );
}

