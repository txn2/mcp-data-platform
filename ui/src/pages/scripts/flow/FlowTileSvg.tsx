import type { ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import type { FlowLayout } from "./flowLayout";
import { FONT_TITLE, ROLE_COLOR, fitText } from "./flowModel";
import type { StructureLayout } from "./structureLayout";
import { boxText, nodeText, stepCard } from "./structureModel";

// FlowTileSvg is a script's flow diagram fit to a tile (#1909): the same
// layout and role colors the Flow tab draws, with no interaction. The whole
// graph is fit to the tile with padding; a small graph is drawn at full size,
// centered, rather than enlarged; and card text is drawn only where the fit
// leaves it legible. It is what the tile worker screenshots, so the tile and
// the Flow tab show the same cards and edges.

// TILE_PADDING is the margin around a fitted diagram, in tile pixels.
const TILE_PADDING = 12;

// LEGIBLE_SCALE is the smallest fit at which card titles are still read.
export const LEGIBLE_SCALE = 0.55;

export function tileFit(layout: { width: number; height: number }, width: number, height: number) {
  const w = Math.max(1, layout.width);
  const h = Math.max(1, layout.height);
  const scale = Math.min(1, (width - 2 * TILE_PADDING) / w, (height - 2 * TILE_PADDING) / h);
  return { scale, x: (width - w * scale) / 2, y: (height - h * scale) / 2 };
}

export function FlowTileSvg({
  layout,
  width,
  height,
}: {
  layout: FlowLayout;
  width: number;
  height: number;
}) {
  const fit = tileFit(layout, width, height);
  const text = fit.scale >= LEGIBLE_SCALE;
  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      style={{ display: "block", background: "hsl(var(--background))" }}
      data-testid="flow-tile"
      data-legible={text}
    >
      <g transform={`translate(${fit.x},${fit.y}) scale(${fit.scale})`}>
        {layout.groups.map((g) => (
          <rect
            key={g.group.id}
            x={g.x}
            y={g.y}
            width={g.width}
            height={g.height}
            rx={12}
            fill="hsl(var(--muted) / 0.6)"
            stroke="hsl(var(--border))"
          />
        ))}
        {layout.edges.map((e) => (
          <path
            key={e.key}
            d={e.path}
            fill="none"
            stroke="hsl(var(--muted-foreground))"
            strokeOpacity={0.7}
            strokeWidth={1.4 / Math.max(fit.scale, 0.25)}
            strokeDasharray={e.edge.kind === "state" ? "6 4" : undefined}
          />
        ))}
        {layout.nodes.map((p) => (
          <g key={p.node.id} data-node={p.node.id}>
            <rect
              x={p.x}
              y={p.y}
              width={p.width}
              height={p.height}
              rx={9}
              fill="hsl(var(--card))"
              stroke={p.node.computed ? "hsl(var(--muted-foreground))" : "hsl(var(--border))"}
              strokeDasharray={p.node.computed ? "5 3" : undefined}
            />
            <rect x={p.x} y={p.y + 8} width={4} height={p.height - 16} rx={2} fill={ROLE_COLOR[p.node.role]} />
            {text && (
              <text x={p.x + 16} y={p.y + 21} fontSize={13} fontWeight={600} fill="hsl(var(--foreground))">
                {fitText(p.node.title, p.width - 28, FONT_TITLE)}
              </text>
            )}
          </g>
        ))}
      </g>
    </svg>
  );
}

// StructureTileSvg is a script's Structure view fit to a tile (#1972): the
// view the Flow tab opens on, so the tile and the tab show the same picture.
export function StructureTileSvg({
  layout,
  graph,
  width,
  height,
}: {
  layout: StructureLayout;
  graph: ScriptFlow;
  width: number;
  height: number;
}) {
  const fit = tileFit(layout, width, height);
  const text = fit.scale >= LEGIBLE_SCALE;
  const stroke = 1.4 / Math.max(fit.scale, 0.25);
  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      style={{ display: "block", background: "hsl(var(--background))" }}
      data-testid="flow-tile"
      data-view="structure"
      data-legible={text}
    >
      <g transform={`translate(${fit.x},${fit.y}) scale(${fit.scale})`}>
        {layout.boxes.map((b) => (
          <g key={b.box.id}>
            <rect
              x={b.x}
              y={b.y}
              width={b.width}
              height={b.height}
              rx={12}
              fill={b.box.kind === "loop" ? "hsl(var(--muted) / 0.25)" : "hsl(var(--muted) / 0.6)"}
              stroke="hsl(var(--border))"
              strokeDasharray={b.box.kind === "loop" ? "6 4" : undefined}
            />
            {text && (
              <text x={b.x + 14} y={b.y + 22} fontSize={13} fontWeight={650} fill="hsl(var(--foreground))">
                {fitText(boxText(b.box), b.width - 28, FONT_TITLE)}
              </text>
            )}
          </g>
        ))}
        {layout.edges.map((e) => (
          <path key={e.key} d={e.path} fill="none" stroke="hsl(var(--muted-foreground))" strokeOpacity={0.7} strokeWidth={stroke} />
        ))}
        {layout.nodes.map((p) => {
          const card = stepCard(p.node, graph);
          const control = !card;
          return (
            <g key={p.node.id} data-struct={p.node.id}>
              <rect
                x={p.x}
                y={p.y}
                width={p.width}
                height={p.height}
                rx={control ? p.height / 2 : 9}
                fill="hsl(var(--card))"
                stroke={p.node.kind === "stop" ? "hsl(var(--destructive))" : "hsl(var(--border))"}
              />
              {card && <rect x={p.x} y={p.y + 8} width={4} height={p.height - 16} rx={2} fill={ROLE_COLOR[card.role]} />}
              {text && (
                <text x={p.x + 16} y={p.y + (control ? p.height / 2 + 4.5 : 21)} fontSize={13} fontWeight={600} fill="hsl(var(--foreground))">
                  {fitText(nodeText(p.node, graph), p.width - 28, FONT_TITLE)}
                </text>
              )}
            </g>
          );
        })}
      </g>
    </svg>
  );
}
