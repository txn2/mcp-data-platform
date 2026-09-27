import type { ReactNode } from "react";
import type {
  FlowChange,
  FlowGroup,
  FlowNode,
  FlowNodeRun,
  ScriptFlow,
  ScriptRunFlow,
} from "@/api/portal/hooks/scriptFlow";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  CHANGE_COLOR,
  CHANGE_LABEL,
  ROLE_COLOR,
  ROLE_LABEL,
  changeCounts,
  excerpt,
  formatDuration,
  nodeLabel,
  type Selection,
} from "./flowModel";

// FlowSidePanel is the reading beside the diagram (#1906): how to read it when
// nothing is selected, and what a selected card, box or parameter is.

interface Props {
  graph: ScriptFlow;
  source: string;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onShowLines?: (lines: number[]) => void;
  /** run is the run drawn on the diagram (#1907), when one is. */
  run?: ScriptRunFlow;
}

export function FlowSidePanel({ graph, source, selection, onSelect, onShowLines, run }: Props) {
  const body = panelBody({ graph, source, selection, onSelect, onShowLines, run });
  return (
    <aside className="min-w-0 space-y-4 text-sm" data-testid="flow-side-panel">
      {body}
      <ParamList graph={graph} selection={selection} onSelect={onSelect} />
    </aside>
  );
}

// panelBody is what the panel reads with nothing, a card, a box or a parameter
// selected.
function panelBody(props: Props): ReactNode {
  const { selection } = props;
  if (selection?.kind === "node") return nodeBody(props, selection.id);
  if (selection?.kind === "group") {
    const g = props.graph.groups.find((x) => x.id === selection.id);
    return g ? <GroupDetail group={g} source={props.source} onShowLines={props.onShowLines} /> : null;
  }
  if (selection?.kind === "param") return <ParamDetail graph={props.graph} name={selection.name} />;
  if (props.graph.compared_with) return <CompareSummary graph={props.graph} />;
  if (props.run) return <RunSummary run={props.run} />;
  return <HowToRead graph={props.graph} />;
}

// nodeBody is a selected card, with what the drawn run did there.
function nodeBody({ graph, source, onShowLines, run }: Props, id: string): ReactNode {
  const n = graph.nodes.find((x) => x.id === id);
  if (!n) return null;
  return (
    <NodeDetail
      graph={graph}
      node={n}
      source={source}
      onShowLines={onShowLines}
      stat={run?.nodes[n.id]}
      inRun={run !== undefined}
    />
  );
}

function Swatch({ role }: { role: keyof typeof ROLE_COLOR }) {
  return (
    <i
      className="mr-1 inline-block size-2.5 rounded-sm align-[-1px]"
      style={{ background: ROLE_COLOR[role] }}
    />
  );
}

// HowToRead is the explainer shown with nothing selected. A diagram a person
// has not seen before needs its conventions stated where they are looking.
function HowToRead({ graph }: { graph: ScriptFlow }) {
  const steps = graph.nodes.filter((n) => n.role !== "input").length;
  return (
    <div className="space-y-3">
      <h4 className="font-medium">How to read this</h4>
      <ul className="list-disc space-y-1.5 pl-4 text-xs text-muted-foreground">
        <li>
          <b className="text-foreground">Left to right</b> is the order data moves: where it comes
          from on the left, what the script produces on the right.
        </li>
        <li>
          <b className="text-foreground">Each card is one thing the script does</b> on the platform:
          a query, an API call, a write, an export. The colored bar says which:{" "}
          <Swatch role="input" />
          input <Swatch role="reads" />
          reads <Swatch role="writes" />
          writes <Swatch role="output" />
          output.
        </li>
        <li>
          A card's <b className="text-foreground">first line</b> is the action and where it goes.
          The next line is the author's stated purpose; grey lines are the tables, paths or files
          it touches.
        </li>
        <li>
          <b className="text-foreground">An arrow</b> means the result of one card is used by the
          next. Hovering an arrow names the functions that reshaped the data on the way.
        </li>
        <li>
          <b className="text-foreground">A grey box</b> is a function in the script; the cards
          inside run as part of it.
        </li>
        <li>
          <b className="text-foreground">Chips</b> on a card: the helper it runs through, and the
          loop it repeats in (↻).
        </li>
        <li>
          <b className="text-foreground">A dashed card</b> names something only known when the
          script runs, shown as <code className="font-mono">{"{the code}"}</code>.
        </li>
        <li>
          <b className="text-foreground">run.state</b> is what the previous run saved; the dashed
          arrow back to it is this run saving for the next.
        </li>
        <li>
          Arrows show where data <i>can</i> flow. A step inside an{" "}
          <code className="font-mono">if</code> may not run every time.
        </li>
      </ul>
      <p className="text-xs text-muted-foreground">
        Select a card for detail; double-click it to open its lines in Source.
      </p>
      <dl className="grid grid-cols-[5rem_1fr] gap-x-2 gap-y-1 text-xs">
        <dt className="text-muted-foreground">Source</dt>
        <dd>{graph.lines} lines</dd>
        <dt className="text-muted-foreground">Steps</dt>
        <dd>{steps}</dd>
        <dt className="text-muted-foreground">Arrows</dt>
        <dd>{graph.edges.length}</dd>
      </dl>
    </div>
  );
}

// RunSummary is the run drawn on the diagram (#1907): how it ended, its calls,
// and the calls no card made, so the calls read on the cards and here always
// add up to what the run made.
function RunSummary({ run }: { run: ScriptRunFlow }) {
  const onCards = Object.values(run.nodes).reduce((sum, n) => sum + n.calls, 0);
  return (
    <div className="space-y-3" data-testid="flow-run-summary">
      <h4 className="font-medium">
        Run of v{run.version}: {run.status}
      </h4>
      {run.cause && <p className="text-xs">Cause: {run.cause}</p>}
      {run.error && (
        <pre className="max-h-40 overflow-auto rounded-md border border-destructive/40 bg-destructive/5 p-2 font-mono text-[11px] whitespace-pre-wrap">
          {run.error}
        </pre>
      )}
      <Rows>
        <Row label="Calls">
          {run.calls}
          {run.calls_truncated ? " (the first ones are drawn)" : ""}
        </Row>
        <Row label="On cards">{onCards}</Row>
        <Row label="Other">{run.other_calls.length}</Row>
      </Rows>
      {run.other_calls.length > 0 && (
        <div className="space-y-1 text-xs">
          <p className="text-muted-foreground">
            Calls no card made: a tool the source computes, or a call recorded before call sites were.
          </p>
          <ul className="space-y-0.5 font-mono">
            {run.other_calls.map((c, i) => (
              <li key={i}>
                {c.tool} · {formatDuration(c.duration_ms)}
                {c.success ? "" : " · failed"}
              </li>
            ))}
          </ul>
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        Cards this run never reached are dimmed; the card it failed at is in the error color.
      </p>
    </div>
  );
}

// ChangeRows is how a card of a compared graph differs from the older version
// (#1908): what it was, and how it changed.
function ChangeRows({ graph, node }: { graph: ScriptFlow; node: FlowNode }) {
  if (!node.change) return null;
  return (
    <>
      {node.was && (
        <Row label="Was">
          <Lines items={[node.was.title, node.was.subtitle ?? "", ...node.was.detail].filter(Boolean)} />
        </Row>
      )}
      <Row label="Change">
        {node.change === "removed" ? `only in v${graph.compared_with}` : node.change}
      </Row>
    </>
  );
}

// RunRows is what the drawn run did at one card.
function RunRows({ stat }: { stat: FlowNodeRun | undefined }) {
  if (!stat?.reached) return <Row label="This run">never reached</Row>;
  return (
    <>
      {stat.calls > 0 && (
        <Row label="This run">
          {stat.calls} call{stat.calls === 1 ? "" : "s"}, {formatDuration(stat.duration_ms)}
        </Row>
      )}
      {stat.outputs > 0 && <Row label="Wrote">{stat.rows} rows</Row>}
      {stat.failed_calls > 0 && (
        <Row label="Refused">
          {stat.failed_calls}
          {stat.last_error ? `: ${stat.last_error}` : ""}
        </Row>
      )}
      {stat.failed && <Row label="Failed">{stat.error ?? "the run failed here"}</Row>}
    </>
  );
}

// CompareSummary is the reading of a compared graph (#1908): what changed since
// the older version, counted, and what each marker means.
function CompareSummary({ graph }: { graph: ScriptFlow }) {
  const counts = changeCounts(graph);
  const none = counts.added + counts.changed + counts.removed === 0;
  return (
    <div className="space-y-3" data-testid="flow-compare-summary">
      <h4 className="font-medium">
        v{graph.version} compared with v{graph.compared_with}
      </h4>
      {none ? (
        <p className="text-xs text-muted-foreground">
          Nothing this script reads, writes or produces changed. Code may have moved or been
          reworded; the text diff shows it.
        </p>
      ) : (
        <ul className="space-y-1.5 text-xs">
          {(Object.keys(CHANGE_LABEL) as FlowChange[]).map((c) => (
            <li key={c} className="flex items-center gap-2">
              <i className="inline-block size-2.5 rounded-sm" style={{ background: CHANGE_COLOR[c] }} />
              <span>
                {counts[c]} {CHANGE_LABEL[c]}
              </span>
            </li>
          ))}
        </ul>
      )}
      <p className="text-xs text-muted-foreground">
        A step is matched by what it reads, writes or produces, not by its line, so moved code is not
        a change. A changed card says what it was; a removed card is the older version's, dashed.
      </p>
    </div>
  );
}

function Rows({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-[5rem_1fr] gap-x-2 gap-y-1.5 text-xs">{children}</dl>;
}

function Row({ label, children, mono }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0 break-words", mono && "font-mono")}>{children}</dd>
    </>
  );
}

function Lines({ items }: { items: string[] }) {
  return (
    <>
      {items.map((t, i) => (
        <div key={i}>{t}</div>
      ))}
    </>
  );
}

function Excerpt({ text }: { text: string }) {
  if (!text) return null;
  return (
    <pre className="max-h-48 overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-[11px] leading-relaxed whitespace-pre">
      {text}
    </pre>
  );
}

function NodeDetail({
  graph,
  node,
  source,
  onShowLines,
  stat,
  inRun,
}: {
  graph: ScriptFlow;
  node: FlowNode;
  source: string;
  onShowLines?: (lines: number[]) => void;
  stat?: FlowNodeRun;
  /** inRun is true when a run is drawn, so the card says what the run did. */
  inRun: boolean;
}) {
  const lines = node.site ? [node.line, node.site] : [node.line];
  const canShow = onShowLines !== undefined && node.line > 0 && node.change !== "removed";
  return (
    <div className="space-y-3">
      <div>
        <div className="text-[11px] font-bold tracking-wider uppercase" style={{ color: ROLE_COLOR[node.role] }}>
          {ROLE_LABEL[node.role]}
        </div>
        <h4 className="font-medium break-words">{node.title}</h4>
        {node.subtitle && <div className="font-mono text-xs break-words">{node.subtitle}</div>}
        {node.purpose && <p className="mt-2 text-xs">{node.purpose}</p>}
      </div>
      <NodeRows graph={graph} node={node} />
      {inRun ? (
        <Rows>
          <RunRows stat={stat} />
        </Rows>
      ) : null}
      <Excerpt text={excerpt(source, node.line, 1, 4)} />
      {canShow && (
        <Button type="button" variant="outline" size="xs" onClick={() => onShowLines(lines)}>
          Show in Source
        </Button>
      )}
    </div>
  );
}

// NodeRows is what a card reaches, runs through and repeats in, and the cards
// on either side of it.
function NodeRows({ graph, node }: { graph: ScriptFlow; node: FlowNode }) {
  const from = graph.edges.filter((e) => e.to === node.id).map((e) => nodeLabel(graph, e.from));
  const feeds = graph.edges.filter((e) => e.from === node.id).map((e) => nodeLabel(graph, e.to));
  return (
      <Rows>
      {node.detail.length > 0 && (
        <Row label="Reaches" mono>
          <Lines items={node.detail} />
        </Row>
      )}
      {node.wrapper && (
        <Row label="Through" mono>
          {node.wrapper}(){node.site ? `, called on line ${node.site}` : ""}
        </Row>
      )}
      {node.loops.length > 0 && (
        <Row label="Repeats" mono>
          <Lines items={node.loops} />
        </Row>
      )}
      {from.length > 0 && (
        <Row label="From">
          <Lines items={from} />
        </Row>
      )}
      {feeds.length > 0 && (
        <Row label="Feeds">
          <Lines items={feeds} />
        </Row>
      )}
      <ChangeRows graph={graph} node={node} />
      {node.computed && (
        <Row label="Note">
          Part of what this step names is computed when the script runs, shown as{" "}
          <code className="font-mono">{"{its source}"}</code>.
        </Row>
      )}
      {node.line > 0 && <Row label="Line">{node.line}</Row>}
    </Rows>
  );
}

function GroupDetail({
  group,
  source,
  onShowLines,
}: {
  group: FlowGroup;
  source: string;
  onShowLines?: (lines: number[]) => void;
}) {
  return (
    <div className="space-y-3">
      <div>
        <div className="text-[11px] font-bold tracking-wider text-muted-foreground uppercase">Function</div>
        <h4 className="font-mono font-medium break-words">{group.label}</h4>
        {group.caption && <p className="mt-2 text-xs">{group.caption}</p>}
      </div>
      <Rows>
        <Row label="Defined">line {group.def_line}</Row>
        <Row label="Called">
          {group.called_from.length > 1 ? "lines " : "line "}
          {group.called_from.join(", ")}
        </Row>
      </Rows>
      <Excerpt text={excerpt(source, group.def_line, 0, 8)} />
      {onShowLines && (
        <Button type="button" variant="outline" size="xs" onClick={() => onShowLines([group.def_line])}>
          Show in Source
        </Button>
      )}
    </div>
  );
}

function ParamDetail({ graph, name }: { graph: ScriptFlow; name: string }) {
  const p = graph.params.find((x) => x.name === name);
  if (!p) return null;
  const reaches = p.reaches.map((id) => nodeLabel(graph, id));
  return (
    <div className="space-y-3">
      <div>
        <div className="text-[11px] font-bold tracking-wider uppercase" style={{ color: ROLE_COLOR.input }}>
          Parameter
        </div>
        <h4 className="font-mono font-medium">{p.name}</h4>
      </div>
      <Rows>
        <Row label="First read">line {p.line}</Row>
        <Row label="Reaches">
          {reaches.length > 0 ? <Lines items={reaches} /> : "no step's arguments"}
        </Row>
        {p.decides && <Row label="Decides">which steps run: a condition reads it</Row>}
      </Rows>
    </div>
  );
}

// ParamList is every parameter, each a toggle that lights up the steps its
// value reaches. Parameters are listed here rather than drawn as wires: a
// parameter that reaches every step is a wire to every card.
function ParamList({
  graph,
  selection,
  onSelect,
}: {
  graph: ScriptFlow;
  selection: Selection;
  onSelect: (s: Selection) => void;
}) {
  if (graph.params.length === 0) return null;
  return (
    <div className="space-y-2 border-t pt-3">
      <p className="text-xs text-muted-foreground">
        Parameters. Select one to see the steps its value reaches.
      </p>
      <div className="flex flex-wrap gap-1.5">
        {graph.params.map((p) => {
          const on = selection?.kind === "param" && selection.name === p.name;
          return (
            <Button
              key={p.name}
              type="button"
              size="xs"
              variant={on ? "secondary" : "outline"}
              aria-pressed={on}
              className="font-mono"
              onClick={() => onSelect(on ? null : { kind: "param", name: p.name })}
            >
              {p.name}
              {p.reaches.length === 0 && p.decides && (
                <span className="font-sans text-muted-foreground">· decides which steps run</span>
              )}
            </Button>
          );
        })}
      </div>
    </div>
  );
}
