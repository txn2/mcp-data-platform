import { CircleDot } from "lucide-react";
import type { ApplicationDiagramNode } from "@/api/portal/hooks/applications";
import { FlowLegend } from "@/pages/scripts/flow/FlowCard";
import { SELECT_COLOR, fitText, textWidth } from "@/pages/scripts/flow/flowModel";
import { Arrow, PanZoomCanvas } from "@/pages/scripts/flow/PanZoomCanvas";
import type { DiagramLayout, PlacedGroup, PlacedLink, PlacedNode } from "./diagramLayout";
import {
  FONT_BODY,
  FONT_CHIP,
  FONT_TITLE,
  NODE_COLOR,
  changeColor,
  nodeChips,
  nodeDetail,
  nodeSubtitle,
} from "./diagramModel";

// DiagramCanvas draws a laid-out application (#2540) on the pan/zoom canvas
// the script Flow tab draws on: a box per group, a card per node, and an arrow
// per link labeled with its kind.

interface Props {
  layout: DiagramLayout;
  selected: string | null;
  compared: boolean;
  onSelect: (id: string | null) => void;
}

const MUTED = "hsl(var(--muted-foreground))";

export function DiagramCanvas({ layout, selected, compared, onSelect }: Props) {
  const groups = [...layout.groups].sort((a, b) => a.depth - b.depth);
  return (
    <PanZoomCanvas
      width={layout.width}
      height={layout.height}
      label="Application diagram"
      testId="application-diagram-canvas"
      legend={<Legend compared={compared} />}
      defs={
        <>
          <Arrow id="app-arrow" color={MUTED} />
          <Arrow id="app-arrow-lit" color={SELECT_COLOR} />
          <Arrow id="app-arrow-added" color={changeColor("added")!} />
          <Arrow id="app-arrow-removed" color={changeColor("removed")!} />
          <Arrow id="app-arrow-changed" color={changeColor("changed")!} />
        </>
      }
      onBackground={() => onSelect(null)}
    >
      {groups.map((g) => (
        <GroupBox key={g.group.id} placed={g} />
      ))}
      {layout.links.map((l) => (
        <LinkPath key={l.key} placed={l} lit={selected !== null && (l.link.from === selected || l.link.to === selected)} />
      ))}
      {layout.nodes.map((p) => (
        <NodeCard key={p.node.id} placed={p} selected={selected === p.node.id} onSelect={() => onSelect(p.node.id)} />
      ))}
    </PanZoomCanvas>
  );
}

function Legend({ compared }: { compared: boolean }) {
  return (
    <FlowLegend>
      <span>arrow = what a part reaches</span>
      <span>dashed = named only when it runs</span>
      {compared &&
        (["added", "changed", "removed"] as const).map((c) => (
          <span key={c} className="inline-flex items-center gap-1.5">
            <i className="inline-block size-2.5 rounded-sm" style={{ background: changeColor(c) }} />
            {c}
          </span>
        ))}
    </FlowLegend>
  );
}

function GroupBox({ placed }: { placed: PlacedGroup }) {
  const { group, x, y, width, height, depth } = placed;
  return (
    <g data-group={group.id}>
      <rect
        x={x}
        y={y}
        width={width}
        height={height}
        rx={12}
        fill={depth > 1 ? "hsl(var(--muted) / 0.8)" : "hsl(var(--muted) / 0.5)"}
        stroke="hsl(var(--border))"
        strokeDasharray={group.id === "outside" ? "6 4" : undefined}
      />
      <text x={x + 14} y={y + 24} fontSize={13} fontWeight={650} fill="hsl(var(--foreground))">
        {fitText(group.label, width - 28, FONT_TITLE)}
      </text>
    </g>
  );
}

// linkStroke is a link's color and arrowhead: lit when its end is selected,
// else its comparison's color.
function linkStroke(placed: PlacedLink, lit: boolean): { color: string; marker: string } {
  if (lit) return { color: SELECT_COLOR, marker: "app-arrow-lit" };
  const change = placed.link.change;
  const color = changeColor(change);
  if (color) return { color, marker: `app-arrow-${change}` };
  return { color: MUTED, marker: "app-arrow" };
}

function LinkPath({ placed, lit }: { placed: PlacedLink; lit: boolean }) {
  const { color, marker } = linkStroke(placed, lit);
  const marked = lit || (placed.link.change !== undefined && placed.link.change !== "unchanged");
  return (
    <g data-link={`${placed.link.from}->${placed.link.to}`} data-change={placed.link.change}>
      <path
        d={placed.path}
        fill="none"
        stroke={color}
        strokeOpacity={marked ? 1 : 0.7}
        strokeWidth={marked ? 2 : 1.4}
        strokeDasharray={placed.link.change === "removed" ? "6 4" : undefined}
        markerEnd={`url(#${marker})`}
      />
      {placed.label && <LinkLabel at={placed.label} color={marked ? color : MUTED} />}
    </g>
  );
}

function LinkLabel({ at, color }: { at: { x: number; y: number; text: string }; color: string }) {
  const w = textWidth(at.text, FONT_CHIP) + 10;
  return (
    <g>
      <rect x={at.x} y={at.y} width={w} height={16} rx={4} fill="hsl(var(--card))" opacity={0.92} />
      <text x={at.x + 5} y={at.y + 12} fontSize={11} fill={color}>
        {at.text}
      </text>
    </g>
  );
}

function cardStroke(node: ApplicationDiagramNode, selected: boolean): string {
  if (selected) return SELECT_COLOR;
  return changeColor(node.change) ?? (node.computed ? MUTED : "hsl(var(--border))");
}

function NodeCard({ placed, selected, onSelect }: { placed: PlacedNode; selected: boolean; onSelect: () => void }) {
  const { node, x, y, width, height } = placed;
  const color = NODE_COLOR[node.kind] ?? MUTED;
  const marked = selected || changeColor(node.change) !== undefined;
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={`${nodeSubtitle(node)}: ${node.label}${node.change && node.change !== "unchanged" ? ` (${node.change})` : ""}`}
      aria-pressed={selected}
      data-node={node.id}
      data-change={node.change}
      className="cursor-pointer outline-none"
      opacity={node.change === "removed" ? 0.65 : 1}
      onPointerDown={(e) => e.stopPropagation()}
      onPointerUp={(e) => {
        e.stopPropagation();
        onSelect();
      }}
      onKeyDown={(e) => e.key === "Enter" && onSelect()}
    >
      <rect
        x={x}
        y={y}
        width={width}
        height={height}
        rx={9}
        fill="hsl(var(--card))"
        stroke={cardStroke(node, selected)}
        strokeWidth={marked ? 2 : 1}
        strokeDasharray={node.computed || node.change === "removed" ? "5 3" : undefined}
      />
      <rect x={x} y={y + 8} width={4} height={height - 16} rx={2} fill={color} />
      <CircleDot x={x + 14} y={y + 9} width={13} height={13} color={color} aria-hidden />
      <text x={x + 34} y={y + 20} fontSize={13} fontWeight={600} fill="hsl(var(--foreground))">
        {fitText(node.label, width - 46, FONT_TITLE)}
      </text>
      {[nodeSubtitle(node), ...nodeDetail(node)].map((line, i) => (
        <text key={i} x={x + 16} y={y + 40 + i * 17} fontSize={12} fill={MUTED}>
          {fitText(line, width - 28, FONT_BODY)}
        </text>
      ))}
      <Chips node={node} x={x} y={y + height - 24} />
    </g>
  );
}

function Chips({ node, x, y }: { node: ApplicationDiagramNode; x: number; y: number }) {
  let at = x + 14;
  return (
    <>
      {nodeChips(node).map((c) => {
        const w = textWidth(c, FONT_CHIP) + 14;
        const cx = at;
        at += w + 5;
        const fill = c === "computed" ? "hsl(var(--muted))" : (changeColor(node.change) ?? "hsl(var(--muted))");
        const solid = c !== "computed";
        return (
          <g key={c}>
            <rect x={cx} y={y} width={w} height={18} rx={9} fill={fill} stroke={solid ? "none" : "hsl(var(--border))"} />
            <text x={cx + 7} y={y + 13} fontSize={11} fill={solid ? "white" : MUTED}>
              {c}
            </text>
          </g>
        );
      })}
    </>
  );
}
