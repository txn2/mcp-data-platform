import type { ComponentType, ReactNode } from "react";
import {
  ArrowLeftRight,
  Bell,
  CircleDot,
  CornerDownRight,
  Database,
  FileDown,
  History,
  PencilLine,
  RefreshCw,
  Save,
  Send,
  Table2,
  Wrench,
  type LucideProps,
} from "lucide-react";
import type { FlowNode, FlowNodeRun } from "@/api/portal/hooks/scriptFlow";
import type { PlacedNode } from "./flowLayout";
import {
  FONT_CHIP,
  FONT_MONO,
  FONT_TITLE,
  CHANGE_COLOR,
  CHANGE_LABEL,
  ROLE_COLOR,
  ROLE_LABEL,
  SELECT_COLOR,
  cardText,
  fitText,
  runChip,
  textWidth,
} from "./flowModel";

// FlowCard is one platform call's card (#1906), drawn the same in the Calls
// and Structure views (#1972) so a reader recognizes a call in either, and the
// legend pieces both views share.

export const KIND_ICON: Record<string, ComponentType<LucideProps>> = {
  query: Database,
  api: ArrowLeftRight,
  tool: Wrench,
  write: PencilLine,
  export: FileDown,
  table: Table2,
  publish_data: RefreshCw,
  save_state: Save,
  notify: Bell,
  publish: Send,
  result: CornerDownRight,
  state: History,
};

// CardChips draws a card's chips along its bottom edge, as many as fit: the
// run's (#1907), the change marker's (#1908), the helper's and the loop's.
function CardChips({
  chips,
  node,
  stat,
  x,
  y,
  width,
}: {
  chips: string[];
  node: FlowNode;
  stat?: FlowNodeRun;
  x: number;
  y: number;
  width: number;
}) {
  const ran = runChip(stat);
  // A failed call on the card colors its run chip, and names itself on hover
  // (#1933).
  const runFailed = !!stat?.failed || (stat?.failed_calls ?? 0) > 0;
  let chipX = x + 14;
  return (
    <>
      {chips.map((c) => {
      const runStat = c === ran;
      // The run's chip reads as one sentence ("3 calls · 1 failed · 1.5 s"), so
      // it gets the room to say it whole; the others stay short.
      const text = fitText(c, runStat ? RUN_CHIP_MAX : 150, FONT_CHIP);
      const w = textWidth(text, FONT_CHIP) + 14;
      if (chipX + w > x + width - 8) return null;
      const cx = chipX;
      chipX += w + 5;
      const marked = node.change !== undefined && c === CHANGE_LABEL[node.change];
      return (
        <Chip
          key={c}
          text={text}
          x={cx}
          y={y}
          width={w}
          fill={chipFill(marked ? CHANGE_COLOR[node.change!] : undefined, runStat, runStat && runFailed)}
          solid={marked || runStat}
          hover={runStat ? stat?.last_error : undefined}
        />
      );
    })}
    </>
  );
}

// RUN_CHIP_MAX is the widest the run's chip is drawn, in pixels.
const RUN_CHIP_MAX = 200;

// Chip is one chip on a card's bottom edge: a pill with its words, and a hover
// title when there is more to say.
function Chip({
  text,
  x,
  y,
  width,
  fill,
  solid,
  hover,
}: {
  text: string;
  x: number;
  y: number;
  width: number;
  fill: string;
  solid: boolean;
  hover?: string;
}) {
  return (
    <g>
      <rect x={x} y={y} width={width} height={18} rx={9} fill={fill} stroke={solid ? "none" : "hsl(var(--border))"}>
        {hover ? <title>{hover}</title> : null}
      </rect>
      <text x={x + 7} y={y + 13} fontSize={11} fill={solid ? "white" : "hsl(var(--muted-foreground))"}>
        {text}
      </text>
    </g>
  );
}

// emphasized is a card drawn with a heavy border: selected, changed, or the
// one a run failed at.
function emphasized(node: FlowNode, selected: boolean, stat?: FlowNodeRun): boolean {
  return selected || node.change !== undefined || stat?.failed === true;
}

function cardLabel(node: FlowNode): string {
  return `${ROLE_LABEL[node.role]}: ${node.title}${node.change ? ` (${node.change})` : ""}`;
}

// runAttributes marks a card with what the drawn run did there, for tests and
// for a reader's tools.
function runAttributes(stat?: FlowNodeRun): Record<string, string> {
  if (!stat) return {};
  return { "data-reached": String(stat.reached), ...(stat.failed ? { "data-failed": "true" } : {}) };
}

function cardOpacity(node: FlowNode, dimmed: boolean): number {
  if (dimmed) return 0.35;
  return node.change === "removed" ? 0.6 : 1;
}

function cardDash(node: FlowNode): string | undefined {
  return node.computed || node.change === "removed" ? "5 3" : undefined;
}

// FAILED_COLOR marks the card a run failed at.
export const FAILED_COLOR = "hsl(var(--destructive))";

// chipFill is a chip's background: a change marker's color, a run's (the
// error color on the failed card), or the plain chip.
function chipFill(change: string | undefined, runStat: boolean, failed: boolean | undefined): string {
  if (change) return change;
  if (runStat) return failed ? FAILED_COLOR : "hsl(var(--chart-1))";
  return "hsl(var(--muted))";
}

// cardStroke is a card's border: the selection accent, then its change
// marker, then the computed and plain borders.
function cardStroke(node: FlowNode, selected: boolean): string {
  if (selected) return SELECT_COLOR;
  if (node.change) return CHANGE_COLOR[node.change];
  return node.computed ? "hsl(var(--muted-foreground))" : "hsl(var(--border))";
}

// FlowLegend is the legend box in a canvas's corner, or in the flow of the
// page when inline.
export function FlowLegend({ children, inline }: { children: ReactNode; inline?: boolean }) {
  const place = inline ? "" : "absolute bottom-3 left-3 max-w-[calc(100%-9rem)]";
  return (
    <div className={`${place} flex flex-wrap gap-x-3 gap-y-1 rounded-md border bg-card px-2.5 py-1.5 text-xs text-muted-foreground`}>
      {children}
    </div>
  );
}

// RoleSwatches names the color of each role's bar.
export function RoleSwatches() {
  return (
    <>
      {(Object.keys(ROLE_COLOR) as Array<keyof typeof ROLE_COLOR>).map((r) => (
        <span key={r} className="inline-flex items-center gap-1.5">
          <i className="inline-block size-2.5 rounded-sm" style={{ background: ROLE_COLOR[r] }} />
          {ROLE_LABEL[r]}
        </span>
      ))}
    </>
  );
}

// RunLegend is what a drawn run adds to the picture.
export function RunLegend() {
  return (
    <>
      <span>lighter = not run this time</span>
      <span className="inline-flex items-center gap-1.5">
        <i className="inline-block size-2.5 rounded-sm" style={{ background: FAILED_COLOR }} />
        red = where the run failed
      </span>
    </>
  );
}

export function Card({
  placed,
  selected,
  dimmed,
  stat,
  onSelect,
  onOpen,
}: {
  placed: PlacedNode;
  selected: boolean;
  dimmed: boolean;
  stat?: FlowNodeRun;
  onSelect: () => void;
  onOpen: () => void;
}) {
  const { node, x, y, width, height } = placed;
  const color = ROLE_COLOR[node.role];
  const Icon = KIND_ICON[node.kind] ?? CircleDot;
  const { lines, chips } = cardText(node, stat);
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={cardLabel(node)}
      aria-pressed={selected}
      data-node={node.id}
      className="cursor-pointer outline-none"
      data-change={node.change}
      {...runAttributes(stat)}
      opacity={cardOpacity(node, dimmed)}
      onPointerDown={(e) => e.stopPropagation()}
      onPointerUp={(e) => {
        e.stopPropagation();
        onSelect();
      }}
      onDoubleClick={(e) => {
        e.stopPropagation();
        onOpen();
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") onSelect();
      }}
    >
      <rect
        x={x}
        y={y}
        width={width}
        height={height}
        rx={9}
        fill="hsl(var(--card))"
        stroke={stat?.failed ? FAILED_COLOR : cardStroke(node, selected)}
        strokeWidth={emphasized(node, selected, stat) ? 2 : 1}
        strokeDasharray={cardDash(node)}
      />
      <rect x={x} y={y + 8} width={4} height={height - 16} rx={2} fill={color} />
      <Icon x={x + 14} y={y + 9} width={15} height={15} color={color} aria-hidden />
      <text x={x + 36} y={y + 21} fontSize={13} fontWeight={600} fill="hsl(var(--foreground))">
        {fitText(node.title, width - 48, FONT_TITLE)}
      </text>
      {lines.map((l, i) => (
        <text
          key={i}
          x={x + 16}
          y={y + 40 + i * 17}
          fontSize={l.font === FONT_MONO ? 11.5 : 12}
          fill={l.muted ? "hsl(var(--muted-foreground))" : "hsl(var(--foreground))"}
          className={l.mono ? "font-mono" : undefined}
        >
          {fitText(l.text, width - 28, l.font)}
        </text>
      ))}
      <CardChips chips={chips} node={node} stat={stat} x={x} y={y + height - 26} width={width} />
    </g>
  );
}
