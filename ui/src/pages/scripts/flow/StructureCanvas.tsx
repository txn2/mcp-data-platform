import { ChevronDown, ChevronRight, CornerUpLeft, Flag, OctagonX, Play, Split } from "lucide-react";
import type { FlowNode, FlowStructure, ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import { Card, FAILED_COLOR, FlowLegend, RoleSwatches, RunLegend } from "./FlowCard";
import { FONT_BODY, FONT_MONO, FONT_TITLE, SELECT_COLOR, fitText, type Selection } from "./flowModel";
import { Arrow, PanZoomCanvas } from "./PanZoomCanvas";
import type { PlacedBox, PlacedStructNode, StructureLayout } from "./structureLayout";
import {
  boxText,
  loopCalls,
  nodeText,
  runState,
  stepCard,
  untested,
  type DrawnRun,
  type RunState,
} from "./structureModel";

// StructureCanvas draws the Structure view (#1972): the script in the order it
// runs, from Start to its exits, with a decision for each if, a box for each
// loop and helper, and a card for each platform call, the same card the Calls
// view draws.

// UNTESTED_COLOR marks a node the version's tests do not reach.
const UNTESTED_COLOR = "hsl(var(--chart-4))";

// StructureRun is the drawn run, as the Structure view reads it.
export type StructureRun = DrawnRun;

interface Props {
  layout: StructureLayout;
  structure: FlowStructure;
  graph: ScriptFlow;
  selection: Selection;
  /** lit is the cards to mark, by the value graph's ids; dimOthers lightens
   * every other card. */
  lit: { nodes: Set<string>; dimOthers: boolean };
  run?: StructureRun;
  /** missed is the lines the version's tests do not reach. */
  missed?: Set<number>;
  fill?: boolean;
  onSelect: (s: Selection) => void;
  onOpen: (n: FlowNode) => void;
  onToggleFold: (boxId: string) => void;
}

export function StructureCanvas(props: Props) {
  const { layout, run, missed, fill, onSelect } = props;
  const boxes = [...layout.boxes].sort((a, b) => boxDepth(layout, a) - boxDepth(layout, b));
  return (
    <PanZoomCanvas
      width={layout.width}
      height={layout.height}
      fill={fill}
      label="Structure diagram"
      testId="structure-canvas"
      legend={<StructureLegend run={run !== undefined && !run.unplaced} tests={missed !== undefined} />}
      defs={
        <>
          <Arrow id="structure-arrow" color="hsl(var(--muted-foreground))" />
        </>
      }
      onBackground={() => onSelect(null)}
    >
      {boxes.map((b) => (
        <BoxShape key={b.box.id} placed={b} {...props} />
      ))}
      {layout.edges.map((e) => (
        <g key={e.key} data-edge={`${e.edge.from}->${e.edge.to}`}>
          <path
            d={e.path}
            fill="none"
            stroke="hsl(var(--muted-foreground))"
            strokeOpacity={0.75}
            strokeWidth={1.4}
            markerEnd="url(#structure-arrow)"
          />
          {e.edge.label && e.label && (
            <g>
              <rect x={e.label.x - 2} y={e.label.y - 1} width={30} height={17} rx={4} fill="hsl(var(--card))" />
              <text x={e.label.x + 13} y={e.label.y + 12} fontSize={11} textAnchor="middle" fill="hsl(var(--muted-foreground))">
                {e.edge.label}
              </text>
            </g>
          )}
        </g>
      ))}
      {layout.nodes.map((p) => (
        <NodeShape key={p.node.id} placed={p} {...props} />
      ))}
    </PanZoomCanvas>
  );
}

function boxDepth(layout: StructureLayout, b: PlacedBox): number {
  const parent = new Map(layout.boxes.map((x) => [x.box.id, x.box.parent]));
  let d = 0;
  for (let p = b.box.parent; p; p = parent.get(p)) d++;
  return d;
}

function StructureLegend({ run, tests }: { run: boolean; tests: boolean }) {
  return (
    <FlowLegend>
      <RoleSwatches />
      <span>arrow = runs next</span>
      {run && <RunLegend />}
      {tests && (
        <span className="inline-flex items-center gap-1.5">
          <i className="inline-block size-2.5 rotate-45" style={{ background: UNTESTED_COLOR }} />
          not reached by tests
        </span>
      )}
    </FlowLegend>
  );
}

// opacityOf is how a node reads with a run drawn.
function opacityOf(state: RunState, dimmed: boolean): number {
  return state === "skipped" || dimmed ? 0.35 : 1;
}

function NodeShape(props: Props & { placed: PlacedStructNode }) {
  const { placed, graph, structure, selection, lit, run, missed, onSelect, onToggleFold } = props;
  const n = placed.node;
  const state = runState(n, run, structure);
  if (stepCard(n, graph)) return <StepNode {...props} state={state} />;
  const selected = selection?.kind === "struct" && selection.id === n.id;
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={nodeText(n, graph)}
      data-struct={n.id}
      data-kind={n.kind}
      data-run={state}
      className="cursor-pointer outline-none"
      opacity={opacityOf(state, lit.dimOthers)}
      onPointerDown={(e) => e.stopPropagation()}
      onPointerUp={(e) => {
        e.stopPropagation();
        onSelect({ kind: "struct", id: n.id });
      }}
      onKeyDown={(e) => e.key === "Enter" && onSelect({ kind: "struct", id: n.id })}
    >
      <ControlShape placed={placed} graph={graph} selected={selected} failed={state === "failed"} onToggleFold={onToggleFold} />
      {untested(n, missed) && <UntestedMark x={placed.x + placed.width} y={placed.y} />}
    </g>
  );
}

// StepNode is a platform call, drawn as the Calls view's card for it.
function StepNode({ placed, graph, lit, run, missed, onSelect, onOpen, state }: Props & { placed: PlacedStructNode; state: RunState }) {
  const n = placed.node;
  const card = stepCard(n, graph)!;
  const drawn = run !== undefined && !run.unplaced;
  const stat = drawn ? run.nodes[card.id] : undefined;
  return (
    <g data-struct={n.id}>
      <Card
        placed={{ node: card, x: placed.x, y: placed.y, width: placed.width, height: placed.height }}
        selected={lit.nodes.has(card.id)}
        dimmed={(lit.dimOthers && !lit.nodes.has(card.id)) || (drawn && state === "skipped")}
        stat={stat && state === "failed" ? { ...stat, failed: true } : stat}
        onSelect={() => onSelect({ kind: "node", id: card.id })}
        onOpen={() => onOpen(card)}
      />
      {untested(n, missed) && <UntestedMark x={placed.x + placed.width} y={placed.y} />}
    </g>
  );
}

// UntestedMark is the small diamond on a node the tests do not reach.
function UntestedMark({ x, y }: { x: number; y: number }) {
  return (
    <rect
      x={x - 12}
      y={y + 2}
      width={8}
      height={8}
      transform={`rotate(45 ${x - 8} ${y + 6})`}
      fill={UNTESTED_COLOR}
      data-untested="true"
    >
      <title>not reached by tests</title>
    </rect>
  );
}

const CONTROL_ICON = {
  start: Play,
  end: Flag,
  stop: OctagonX,
  return: CornerUpLeft,
  if: Split,
} as const;

// ICON_COLOR is each control node's icon color: a stop in the error color,
// the ends in the output color, the rest muted.
const ICON_COLOR: Record<string, string> = {
  stop: FAILED_COLOR,
  start: "hsl(var(--chart-3))",
  end: "hsl(var(--chart-3))",
};

// outline is a shape's border: the error color where the run failed, the
// selection accent, or the plain border.
function outline(failed: boolean, selected: boolean): { stroke: string; strokeWidth: number } {
  if (failed) return { stroke: FAILED_COLOR, strokeWidth: 2 };
  if (selected) return { stroke: SELECT_COLOR, strokeWidth: 2 };
  return { stroke: "hsl(var(--border))", strokeWidth: 1 };
}

function ControlShape({
  placed,
  graph,
  selected,
  failed,
  onToggleFold,
}: {
  placed: PlacedStructNode;
  graph: ScriptFlow;
  selected: boolean;
  failed: boolean;
  onToggleFold: (boxId: string) => void;
}) {
  const { node: n, x, y, width, height } = placed;
  const line = outline(failed, selected);
  if (n.kind === "fn") return <FoldedBox placed={placed} {...line} onToggleFold={onToggleFold} />;
  const Icon = CONTROL_ICON[n.kind as keyof typeof CONTROL_ICON];
  const text = nodeText(n, graph);
  // A decision and a stop quote the source, so they are set in its face.
  const quoted = n.kind === "if" || n.kind === "stop";
  return (
    <>
      {n.kind === "if" ? (
        <polygon
          points={`${x + 14},${y} ${x + width - 14},${y} ${x + width},${y + height / 2} ${x + width - 14},${y + height} ${x + 14},${y + height} ${x},${y + height / 2}`}
          fill="hsl(var(--card))"
          {...line}
        />
      ) : (
        <rect x={x} y={y} width={width} height={height} rx={height / 2} fill="hsl(var(--card))" {...line} />
      )}
      {Icon && (
        <Icon
          x={x + 14}
          y={y + height / 2 - 7}
          width={14}
          height={14}
          color={ICON_COLOR[n.kind] ?? "hsl(var(--muted-foreground))"}
          aria-hidden
        />
      )}
      <text
        x={x + 34}
        y={y + height / 2 + 4.5}
        fontSize={quoted ? 11.5 : 13}
        fontWeight={quoted ? 500 : 600}
        fill="hsl(var(--foreground))"
        className={quoted ? "font-mono" : undefined}
      >
        {fitText(text, width - 46, quoted ? FONT_MONO : FONT_TITLE)}
        <title>{text}</title>
      </text>
    </>
  );
}

// FoldedBox is a helper drawn folded: its signature and caption, and the
// control that opens it.
function FoldedBox({
  placed,
  stroke,
  strokeWidth,
  onToggleFold,
}: {
  placed: PlacedStructNode;
  stroke: string;
  strokeWidth: number;
  onToggleFold: (boxId: string) => void;
}) {
  const { node: n, x, y, width, height } = placed;
  if (n.kind !== "fn") return null;
  return (
    <>
      <rect x={x} y={y} width={width} height={height} rx={10} fill="hsl(var(--muted) / 0.6)" stroke={stroke} strokeWidth={strokeWidth} />
      <FoldToggle x={x + 8} y={y + 9} open={false} label={`Open ${n.label}`} onToggle={() => onToggleFold(n.folded.id)} />
      <text x={x + 32} y={y + 22} fontSize={13} fontWeight={650} fill="hsl(var(--foreground))" className="font-mono">
        {fitText(n.label, width - 44, FONT_TITLE)}
      </text>
      {n.caption && <Caption text={n.caption} x={x} y={y} width={width} />}
    </>
  );
}

// Caption is a box's first comment sentence under its heading.
function Caption({ text, x, y, width }: { text: string; x: number; y: number; width: number }) {
  return (
    <text x={x + 14} y={y + 41} fontSize={12} fill="hsl(var(--muted-foreground))">
      {fitText(text, width - 28, FONT_BODY)}
    </text>
  );
}

// FoldToggle opens or folds a helper box.
function FoldToggle({ x, y, open, label, onToggle }: { x: number; y: number; open: boolean; label: string; onToggle: () => void }) {
  const Icon = open ? ChevronDown : ChevronRight;
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={label}
      aria-expanded={open}
      className="cursor-pointer outline-none"
      onPointerDown={(e) => e.stopPropagation()}
      onPointerUp={(e) => {
        e.stopPropagation();
        onToggle();
      }}
      onKeyDown={(e) => e.key === "Enter" && onToggle()}
    >
      <rect x={x} y={y} width={18} height={18} rx={4} fill="hsl(var(--card))" stroke="hsl(var(--border))" />
      <Icon x={x + 2} y={y + 2} width={14} height={14} color="hsl(var(--muted-foreground))" aria-hidden />
    </g>
  );
}

function BoxShape({ placed, structure, selection, run, onSelect, onToggleFold }: Props & { placed: PlacedBox }) {
  const { box, x, y, width, height } = placed;
  const loop = box.kind === "loop";
  const line = outline(run?.structure_failed === box.id, selection?.kind === "sbox" && selection.id === box.id);
  const select = () => onSelect({ kind: "sbox", id: box.id });
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={loop ? boxText(box) : `Function ${box.label}`}
      data-box={box.id}
      className="cursor-pointer outline-none"
      onPointerDown={(e) => e.stopPropagation()}
      onPointerUp={(e) => {
        e.stopPropagation();
        select();
      }}
      onKeyDown={(e) => e.key === "Enter" && select()}
    >
      <rect
        x={x}
        y={y}
        width={width}
        height={height}
        rx={12}
        fill={loop ? "hsl(var(--muted) / 0.25)" : "hsl(var(--muted) / 0.55)"}
        {...line}
        strokeDasharray={loop ? "6 4" : undefined}
      />
      {!loop && <FoldToggle x={x + 8} y={y + 9} open label={`Fold ${box.label}`} onToggle={() => onToggleFold(box.id)} />}
      <BoxHeading placed={placed} times={loop ? loopTimes(structure, box.id, run) : 0} />
      {box.caption && <Caption text={box.caption} x={x} y={y} width={width} />}
    </g>
  );
}

// loopTimes is how many passes a loop's calls made in the drawn run.
function loopTimes(structure: FlowStructure, boxId: string, run?: StructureRun): number {
  return run && !run.unplaced ? loopCalls(structure, boxId, run.nodes) : 0;
}

// BoxHeading is a box's heading, and a loop's pass count beside it.
function BoxHeading({ placed, times }: { placed: PlacedBox; times: number }) {
  const { box, x, y, width } = placed;
  const loop = box.kind === "loop";
  return (
    <>
      <text
        x={x + (loop ? 14 : 32)}
        y={y + 22}
        fontSize={13}
        fontWeight={650}
        fill="hsl(var(--foreground))"
        className={loop ? undefined : "font-mono"}
      >
        {fitText(boxText(box), width - (loop ? 80 : 100), FONT_TITLE)}
      </text>
      {times > 0 && (
        <text x={x + width - 14} y={y + 22} fontSize={12} textAnchor="end" fill="hsl(var(--muted-foreground))">
          ×{times}
        </text>
      )}
    </>
  );
}
