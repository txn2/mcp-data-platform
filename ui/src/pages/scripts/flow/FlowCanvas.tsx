import type { FlowNode, FlowNodeRun } from "@/api/portal/hooks/scriptFlow";
import type { FlowLayout, PlacedGroup } from "./flowLayout";
import { Arrow, PanZoomCanvas } from "./PanZoomCanvas";
import { Card, FlowLegend, RoleSwatches, RunLegend } from "./FlowCard";
import { FONT_BODY, FONT_TITLE, SELECT_COLOR, fitText, type Lit, type Selection } from "./flowModel";

// FlowCanvas draws the laid-out value graph (#1906), the Calls view (#1972): a
// card per platform call, and an arrow where one call's result feeds another.

interface Props {
  layout: FlowLayout;
  selection: Selection;
  lit: Lit;
  /** run is what one run did at each card (#1907), when a run is drawn. */
  run?: Record<string, FlowNodeRun>;
  /** fill makes the canvas as tall as its container (full screen). */
  fill?: boolean;
  onSelect: (s: Selection) => void;
  onOpen: (n: FlowNode) => void;
}

export function FlowCanvas({ layout, selection, lit, run, fill, onSelect, onOpen }: Props) {
  const groups = [...layout.groups].sort((a, b) => depth(a) - depth(b));
  const selectedGroup = selection?.kind === "group" ? selection.id : null;

  return (
    <PanZoomCanvas
      width={layout.width}
      height={layout.height}
      fill={fill}
      label="Flow diagram"
      testId="flow-canvas"
      legend={<Legend run={run !== undefined} />}
      defs={
        <>
          <Arrow id="flow-arrow" color="hsl(var(--muted-foreground))" />
          <Arrow id="flow-arrow-lit" color={SELECT_COLOR} />
        </>
      }
      onBackground={() => onSelect(null)}
    >
      {groups.map((g) => (
        <GroupBox
          key={g.group.id}
          placed={g}
          selected={selectedGroup === g.group.id}
          onSelect={() => onSelect({ kind: "group", id: g.group.id })}
        />
      ))}
      {layout.edges.map((e) => {
        const hot = lit.edges.has(e.edge);
        const marker = `url(#${hot ? "flow-arrow-lit" : "flow-arrow"})`;
        return (
          <path
            key={e.key}
            d={e.path}
            fill="none"
            stroke={hot ? SELECT_COLOR : "hsl(var(--muted-foreground))"}
            strokeOpacity={hot ? 1 : 0.7}
            strokeWidth={hot ? 2.2 : 1.4}
            strokeDasharray={e.edge.kind === "state" || e.edge.change ? "6 4" : undefined}
            markerStart={e.reversed ? marker : undefined}
            markerEnd={e.reversed ? undefined : marker}
            data-edge={`${e.edge.from}->${e.edge.to}`}
          >
            {e.edge.via.length > 0 && <title>{`reshaped by ${e.edge.via.join(", ")}`}</title>}
          </path>
        );
      })}
      {layout.nodes.map((p) => (
        <Card
          key={p.node.id}
          placed={p}
          selected={lit.nodes.has(p.node.id)}
          dimmed={(lit.dimOthers && !lit.nodes.has(p.node.id)) || (run !== undefined && !run[p.node.id]?.reached)}
          stat={run?.[p.node.id]}
          onSelect={() => onSelect({ kind: "node", id: p.node.id })}
          onOpen={() => onOpen(p.node)}
        />
      ))}
    </PanZoomCanvas>
  );
}

function depth(g: PlacedGroup): number {
  return g.group.id.split("/").length;
}

function Legend({ run }: { run: boolean }) {
  return (
    <FlowLegend>
      <RoleSwatches />
      <span>arrow = result feeds the next call</span>
      <span>dashed = known only when it runs</span>
      {run && <RunLegend />}
    </FlowLegend>
  );
}

function GroupBox({
  placed,
  selected,
  onSelect,
}: {
  placed: PlacedGroup;
  selected: boolean;
  onSelect: () => void;
}) {
  const { group, x, y, width, height } = placed;
  const calls =
    group.called_from.length > 1
      ? `called from lines ${group.called_from.join(", ")}`
      : `line ${group.called_from[0] ?? group.def_line}`;
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={`Function ${group.label}`}
      className="cursor-pointer outline-none"
      onPointerUp={(e) => {
        e.stopPropagation();
        onSelect();
      }}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.key === "Enter" && onSelect()}
    >
      <rect
        x={x}
        y={y}
        width={width}
        height={height}
        rx={12}
        fill="hsl(var(--muted) / 0.6)"
        stroke={selected ? SELECT_COLOR : "hsl(var(--border))"}
        strokeWidth={selected ? 2 : 1}
      />
      <text x={x + 14} y={y + 22} fontSize={13} fontWeight={650} fill="hsl(var(--foreground))" className="font-mono">
        {fitText(group.label, width - 150, FONT_TITLE)}
      </text>
      <text x={x + width - 14} y={y + 22} fontSize={11.5} textAnchor="end" fill="hsl(var(--muted-foreground))">
        {calls}
      </text>
      {group.caption && (
        <text x={x + 14} y={y + 41} fontSize={12} fill="hsl(var(--muted-foreground))">
          {fitText(group.caption, width - 28, FONT_BODY)}
        </text>
      )}
    </g>
  );
}

