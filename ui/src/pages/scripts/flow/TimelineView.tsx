import { useEffect, useMemo, useRef, useState } from "react";
import { scaleLinear } from "d3-scale";
import { Maximize2, Minus, Plus } from "lucide-react";
import type { FlowNode, ScriptRunFlow } from "@/api/portal/hooks/scriptFlow";
import { Button } from "@/components/ui/button";
import { FAILED_COLOR, FlowLegend, RoleSwatches } from "./FlowCard";
import { FONT_BODY, ROLE_COLOR, SELECT_COLOR, fitText, formatDuration, type Selection } from "./flowModel";
import { buildTimeline, lineOf, type TimelineBar } from "./timelineModel";

// TimelineView is a run as a flame chart (#1972): every audited call placed
// when it was made and for as long as it took, under the helpers it was made
// through. The gaps between calls are the script's own work.

const ROW = 26;
const AXIS = 22;
const MIN_BAR = 2;
const MAX_ZOOM = 64;

interface Props {
  run: ScriptRunFlow;
  selection: Selection;
  fill?: boolean;
  onSelect: (s: Selection) => void;
  onShowLines?: (lines: number[]) => void;
}

export function TimelineView({ run, selection, fill, onSelect, onShowLines }: Props) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [zoom, setZoom] = useState(1);
  const base = useWidth(hostRef);
  const timeline = useMemo(
    () => buildTimeline(run.timeline, run.graph.structure.functions, run.run_ms),
    [run.timeline, run.graph.structure.functions, run.run_ms],
  );
  const cards = useMemo(() => new Map(run.graph.nodes.map((n) => [n.id, n])), [run.graph.nodes]);
  const width = Math.max(base - 2, base * zoom - 2);
  const x = scaleLinear().domain([0, timeline.span]).range([8, width - 8]);
  const height = AXIS + timeline.depth * ROW + 12;
  const selectedCard = selection?.kind === "node" ? selection.id : null;

  if (run.timeline.length === 0) {
    return (
      <p className="text-sm text-muted-foreground" data-testid="timeline-empty">
        This run made no calls, so there is nothing to place in time.
      </p>
    );
  }
  const own = Math.max(0, timeline.span - timeline.upstream);
  return (
    <div className={fill ? "flex h-full flex-col gap-2" : "space-y-2"} data-testid="timeline">
      <p className="text-xs text-muted-foreground">
        {run.timeline.length} calls over {formatDuration(timeline.span)}: calls in flight{" "}
        {formatDuration(timeline.upstream)}, the script's own work between them {formatDuration(own)}.
      </p>
      <div
        ref={hostRef}
        className={`relative overflow-x-auto overflow-y-auto rounded-md border ${fill ? "min-h-0 flex-1" : ""}`}
        style={{ maxHeight: fill ? undefined : 640, backgroundColor: "hsl(var(--muted) / 0.2)" }}
      >
        <svg width={width} height={height} role="group" aria-label="Timeline of the run's calls">
          <Axis x={x} height={height} />
          {timeline.bars.map((b) => (
            <Bar
              key={b.key}
              bar={b}
              x={x}
              card={b.call?.node ? cards.get(b.call.node) : undefined}
              selected={!!b.call?.node && b.call.node === selectedCard}
              onPick={() => pick(b, onSelect, onShowLines)}
            />
          ))}
        </svg>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <FlowLegendInline />
        <div className="flex gap-1">
          <Button type="button" variant="outline" size="icon-sm" aria-label="Zoom in" onClick={() => setZoom((z) => Math.min(MAX_ZOOM, z * 1.6))}>
            <Plus />
          </Button>
          <Button type="button" variant="outline" size="icon-sm" aria-label="Zoom out" onClick={() => setZoom((z) => Math.max(1, z / 1.6))}>
            <Minus />
          </Button>
          <Button type="button" variant="outline" size="icon-sm" aria-label="Fit the whole run" onClick={() => setZoom(1)}>
            <Maximize2 />
          </Button>
        </div>
      </div>
    </div>
  );
}

// useWidth is an element's width, followed as it resizes.
function useWidth(ref: React.RefObject<HTMLDivElement | null>): number {
  const [width, setWidth] = useState(900);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.clientWidth || 900);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth || 900));
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return width;
}

// pick selects a call's card, or, for a call no card made, shows its line.
function pick(b: TimelineBar, onSelect: (s: Selection) => void, onShowLines?: (lines: number[]) => void) {
  if (!b.call) return;
  if (b.call.node) {
    onSelect({ kind: "node", id: b.call.node });
    return;
  }
  const site = b.call.call_site;
  if (site && site.length > 0) onShowLines?.([lineOf(site[site.length - 1]!)]);
}

function FlowLegendInline() {
  return (
    <FlowLegend inline>
        <RoleSwatches />
        <span>bar = one call, when it ran and for how long</span>
        <span>gap = the script's own work</span>
        <span className="inline-flex items-center gap-1.5">
          <i className="inline-block size-2.5 rounded-sm border-2" style={{ borderColor: FAILED_COLOR }} />
          failed call
        </span>
    </FlowLegend>
  );
}

function Axis({ x, height }: { x: ReturnType<typeof scaleLinear<number, number>>; height: number }) {
  const ticks = x.ticks(8);
  const [left, right] = x.range() as [number, number];
  // A label at either end is anchored inward, so it is not cut by the edge.
  const anchor = (px: number) => (px - left < 24 ? "start" : right - px < 24 ? "end" : "middle");
  return (
    <g aria-hidden>
      {ticks.map((t) => (
        <g key={t}>
          <line x1={x(t)} x2={x(t)} y1={AXIS - 4} y2={height} stroke="hsl(var(--border))" strokeDasharray="2 4" />
          <text x={x(t)} y={13} fontSize={10.5} textAnchor={anchor(x(t))} fill="hsl(var(--muted-foreground))">
            {formatDuration(Math.round(t))}
          </text>
        </g>
      ))}
    </g>
  );
}

// barLook is how a bar is drawn: a call in its card's role color (muted when
// no card made it), outlined in the error color when it failed or the accent
// when selected; a helper's frame in the muted fill with a plain border.
function barLook(bar: TimelineBar, card: FlowNode | undefined, selected: boolean) {
  const call = bar.call;
  if (!call) {
    return { fill: "hsl(var(--muted))", fillOpacity: 1, stroke: "hsl(var(--border))", strokeWidth: 1, text: bar.label, failed: false };
  }
  const failed = !call.success;
  const accent = failed ? FAILED_COLOR : SELECT_COLOR;
  return {
    fill: card ? ROLE_COLOR[card.role] : "hsl(var(--muted-foreground))",
    fillOpacity: 0.85,
    stroke: failed || selected ? accent : "none",
    strokeWidth: failed || selected ? 2 : 1,
    text: card?.title ?? call.tool,
    failed,
  };
}

function Bar({
  bar,
  x,
  card,
  selected,
  onPick,
}: {
  bar: TimelineBar;
  x: ReturnType<typeof scaleLinear<number, number>>;
  card?: FlowNode;
  selected: boolean;
  onPick: () => void;
}) {
  const left = x(bar.start);
  const w = Math.max(MIN_BAR, x(bar.end) - left);
  const y = AXIS + bar.depth * ROW;
  const look = barLook(bar, card, selected);
  const call = bar.call;
  return (
    <g
      data-bar={bar.key}
      data-failed={look.failed ? "true" : undefined}
      role={call ? "button" : undefined}
      tabIndex={call ? 0 : undefined}
      aria-label={call ? `${look.text}, ${formatDuration(call.duration_ms)}` : bar.label}
      className={call ? "cursor-pointer outline-none" : undefined}
      onClick={onPick}
      onKeyDown={(e) => e.key === "Enter" && onPick()}
    >
      <rect
        x={left}
        y={y + 2}
        width={w}
        height={ROW - 4}
        rx={3}
        fill={look.fill}
        fillOpacity={look.fillOpacity}
        stroke={look.stroke}
        strokeWidth={look.strokeWidth}
      >
        <title>{hover(bar, look.text)}</title>
      </rect>
      {w > 40 && <BarLabel text={look.text} x={left} y={y} width={w} call={!!call} />}
    </g>
  );
}

// BarLabel is a bar's words, drawn where the bar is wide enough for them.
function BarLabel({ text, x, y, width, call }: { text: string; x: number; y: number; width: number; call: boolean }) {
  return (
    <text
      x={x + 6}
      y={y + ROW / 2 + 4}
      fontSize={11.5}
      fill={call ? "white" : "hsl(var(--foreground))"}
      pointerEvents="none"
      className={call ? undefined : "font-mono"}
    >
      {fitText(text, width - 10, FONT_BODY)}
    </text>
  );
}

// hover is what a bar says on hover: the call's tool, how long it took and
// what it answered, or the helper and how long it was running calls.
function hover(bar: TimelineBar, text: string): string {
  const c = bar.call;
  if (!c) return `${bar.label}, ${formatDuration(bar.end - bar.start)} from its first call to its last`;
  const parts = [text, `${c.tool} · ${formatDuration(c.duration_ms)}`, `${c.response_chars.toLocaleString()} characters answered`];
  if (!c.success) parts.push(`failed: ${c.error ?? "no message"}`);
  return parts.join("\n");
}
