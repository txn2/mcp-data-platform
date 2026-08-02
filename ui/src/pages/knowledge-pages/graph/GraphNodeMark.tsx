import { nodeStyle, typeLabel } from "./graphModel";
import { LABEL_OFFSET_Y } from "./labelPlacement";
import type { SimNode } from "./useForceLayout";

interface GraphNodeMarkProps {
  node: SimNode;
  dimmed: boolean;
  matched: boolean;
  /** The node open in the inspector. */
  selected: boolean;
  /** The node lies on the traced shortest path. */
  onPath: boolean;
  showLabel: boolean;
  onHover: (id: string | null) => void;
  onDragStart: (e: React.PointerEvent, id: string) => void;
  onSelect: (node: SimNode) => void;
}

/**
 * GraphNodeMark draws one vertex: a shape encoding its type, a radius encoding
 * how much of the graph it bridges, an optional label, and the rings that mark
 * it as selected, matched by the search, or on a traced path.
 *
 * Every node is activatable, because activating one selects it for the inspector
 * rather than navigating away — reading a node no longer costs leaving the graph.
 */
export function GraphNodeMark({
  node,
  dimmed,
  matched,
  selected,
  onPath,
  showLabel,
  onHover,
  onDragStart,
  onSelect,
}: GraphNodeMarkProps) {
  const style = nodeStyle(node.type);
  const r = node.radius;
  return (
    <g
      transform={`translate(${node.x ?? 0},${node.y ?? 0})`}
      opacity={dimmed ? 0.15 : 1}
      role="button"
      tabIndex={0}
      aria-label={`${typeLabel(node.type)}: ${node.label}`}
      aria-pressed={selected}
      className="cursor-pointer outline-none"
      onPointerDown={(e) => onDragStart(e, node.id)}
      onPointerEnter={() => onHover(node.id)}
      onPointerLeave={() => onHover(null)}
      onFocus={() => onHover(node.id)}
      onBlur={() => onHover(null)}
      onClick={() => onSelect(node)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect(node);
        }
      }}
    >
      {/* An invisible hit area. A node's own mark is only a few pixels across,
          which is a poor pointer target, and an SVG group is only hittable where
          its children actually paint — so without this, a click aimed at a small
          node lands on the empty canvas behind it and pans instead. */}
      <circle r={r + 8} fill="transparent" />
      {selected && (
        <circle
          r={r + 7}
          fill="none"
          className="stroke-primary"
          strokeWidth={2.5}
          strokeOpacity={0.95}
        />
      )}
      {onPath && !selected && (
        <circle r={r + 5} fill="none" className="stroke-primary" strokeWidth={2} strokeOpacity={0.7} />
      )}
      {matched && !selected && (
        <circle r={r + 5} fill="none" stroke={style.fill} strokeWidth={2} strokeOpacity={0.9} />
      )}
      <NodeShape shape={style.shape} r={r} fill={style.fill} broken={!node.exists} />
      {showLabel && (
        // A halo in the canvas colour, painted under the glyphs, so a label
        // crossing an edge stays readable instead of being cut by it.
        <text
          y={r + LABEL_OFFSET_Y}
          textAnchor="middle"
          className="pointer-events-none fill-foreground text-[10px]"
          stroke="hsl(var(--card))"
          strokeWidth={3}
          strokeLinejoin="round"
          style={{ paintOrder: "stroke" }}
        >
          {truncateLabel(node.label)}
        </text>
      )}
    </g>
  );
}

/**
 * NodeShape renders the type mark. A broken reference (its target is gone) is
 * drawn hollow with a dashed outline, so it reads as a gap in the corpus rather
 * than as another entity.
 */
function NodeShape({
  shape,
  r,
  fill,
  broken,
}: {
  shape: "circle" | "square" | "diamond";
  r: number;
  fill: string;
  broken: boolean;
}) {
  const paint = broken
    ? { fill: "none", stroke: fill, strokeWidth: 1.5, strokeDasharray: "3 2" }
    : { fill, stroke: "hsl(0, 0%, 100%)", strokeWidth: 1, strokeOpacity: 0.35 };
  if (shape === "circle") return <circle r={r} {...paint} />;
  const side = r * 1.8;
  return (
    <rect
      x={-side / 2}
      y={-side / 2}
      width={side}
      height={side}
      rx={2}
      transform={shape === "diamond" ? "rotate(45)" : undefined}
      {...paint}
    />
  );
}

/** MAX_LABEL bounds a node label so one long title cannot blanket its cluster. */
const MAX_LABEL = 18;

/** truncateLabel shortens a label for display, keeping the full text in the
 * node's accessible name. */
export function truncateLabel(label: string): string {
  return label.length > MAX_LABEL ? `${label.slice(0, MAX_LABEL - 1)}…` : label;
}
